package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMCPReadLifecycleJSONAndSSE(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprint(sse), func(t *testing.T) {
			var mu sync.Mutex
			var methods []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Header.Get("Authorization") != "Bearer mcp-secret" || r.Header.Get("Accept") != "application/json, text/event-stream" {
					t.Error("missing authentication or accept")
				}
				if r.Method == "DELETE" {
					methods = append(methods, "DELETE")
					w.WriteHeader(204)
					return
				}
				var request struct {
					ID     int
					Method string
					Params struct {
						URI             string
						ProtocolVersion string
					}
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("invalid request")
					return
				}
				methods = append(methods, request.Method)
				var result any
				switch request.Method {
				case "initialize":
					if request.Params.ProtocolVersion != mcpVersion {
						t.Error("wrong proposed version")
					}
					w.Header().Set("Mcp-Session-Id", "test-session")
					result = map[string]any{"protocolVersion": mcpVersion, "capabilities": map[string]any{"resources": map[string]any{}}}
				case "notifications/initialized":
					w.WriteHeader(202)
					return
				case "resources/read":
					if request.Params.URI != "data:allowed" || r.Header.Get("Mcp-Session-Id") != "test-session" || r.Header.Get("MCP-Protocol-Version") != mcpVersion {
						t.Error("scope or session violated")
					}
					result = map[string]any{"contents": []any{map[string]string{"uri": "data:allowed", "text": "memory evidence from a remote source"}}}
				default:
					t.Error("unexpected method", request.Method)
					w.WriteHeader(400)
					return
				}
				body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
				if sse {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n")
					fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
				} else {
					w.Header().Set("Content-Type", "application/json")
					w.Write(body)
				}
			}))
			defer server.Close()
			docs, err := readMCP(context.Background(), MCPConfig{server.URL, []string{"data:allowed"}}, "mcp-secret")
			if err != nil {
				t.Fatal(err)
			}
			if len(docs) != 1 || docs[0].Origin != "mcp" || docs[0].URI != "data:allowed" || docs[0].Endpoint != server.URL {
				t.Fatal("missing provenance")
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(methods, []string{"initialize", "notifications/initialized", "resources/read", "DELETE"}) {
				t.Fatal("unexpected lifecycle", methods)
			}
		})
	}
}

func TestMCPRejectsUntrustedResults(t *testing.T) {
	for _, mode := range []string{"uri", "blob", "oversize", "multiple", "id", "version", "capability", "expired", "server-request", "stream-limit"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     int
					Method string
				}
				json.NewDecoder(r.Body).Decode(&req)
				if req.Method == "notifications/initialized" {
					w.WriteHeader(202)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				result := map[string]any{"protocolVersion": mcpVersion, "capabilities": map[string]any{"resources": map[string]any{}}}
				if mode == "version" {
					result["protocolVersion"] = "unknown"
				}
				if mode == "capability" {
					result["capabilities"] = map[string]any{"tools": map[string]any{}}
				}
				if req.Method == "resources/read" {
					item := map[string]any{"uri": "data:allowed", "text": "memory evidence"}
					switch mode {
					case "uri":
						item["uri"] = "file:///not-authorized"
					case "blob":
						item["blob"] = "YmluYXJ5"
					case "oversize":
						item["text"] = strings.Repeat("x", 256*1024+1)
					case "id":
						req.ID++
					case "expired":
						w.WriteHeader(404)
						return
					case "server-request":
						fmt.Fprint(w, `{"jsonrpc":"2.0","id":20,"method":"sampling/createMessage"}`)
						return
					case "stream-limit":
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, strings.Repeat(":comment\n", maxMCPResponse/8+1))
						return
					}
					items := []any{item}
					if mode == "multiple" {
						items = append(items, item)
					}
					result = map[string]any{"contents": items}
				}
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
			}))
			defer server.Close()
			if _, err := readMCP(context.Background(), MCPConfig{server.URL, []string{"data:allowed"}}, ""); err == nil {
				t.Fatal("invalid remote result accepted")
			}
		})
	}
}

func TestMCPRedirectAndCancellation(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	if _, err := readMCP(context.Background(), MCPConfig{redirect.URL, []string{"data:allowed"}}, "secret"); err == nil || reached {
		t.Fatal("redirect not blocked")
	}
	waiting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer waiting.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := readMCP(ctx, MCPConfig{waiting.URL, []string{"data:allowed"}}, ""); err == nil || time.Since(start) > time.Second {
		t.Fatal("cancellation not bounded")
	}
}

func TestMCPConfigScope(t *testing.T) {
	for _, config := range []MCPConfig{
		{"http://example.com/mcp", []string{"data:ok"}},
		{"https://example.com/mcp", nil},
		{"https://example.com/mcp", []string{"data:ok", "data:ok"}},
		{"https://user:secret@example.com/mcp", []string{"data:ok"}},
		{"https://example.com/mcp", []string{"not-a-uri"}},
	} {
		if config.Validate() == nil {
			t.Fatal("invalid scope accepted")
		}
	}
}
