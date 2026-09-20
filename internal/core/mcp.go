package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const mcpVersion = "2025-06-18"
const maxMCPResponse = 2 * 1024 * 1024

// The client reads only exact operator-authorized resources. It never calls tools.
type MCPConfig struct {
	Endpoint string   `json:"endpoint"`
	URIs     []string `json:"uris"`
}

func (c MCPConfig) Validate() error {
	// Reuse the same endpoint policy as the model adapter, without model settings.
	if err := (CognitionConfig{BaseURL: c.Endpoint, Model: "validation", MaxCalls: 1, MaxTokens: 128}).Validate(); err != nil {
		return errors.New("MCP requires a credential-free HTTPS endpoint or literal loopback HTTP")
	}
	if len(c.URIs) < 1 || len(c.URIs) > 8 {
		return errors.New("MCP requires 1..8 explicit resource URIs")
	}
	seen := map[string]bool{}
	for _, uri := range c.URIs {
		u, err := url.Parse(uri)
		if err != nil || u.Scheme == "" || u.User != nil || len(uri) > 2048 || seen[uri] || strings.ContainsAny(uri, "\r\n\x00") {
			return errors.New("invalid or duplicate MCP resource URI")
		}
		seen[uri] = true
	}
	return nil
}

type mcpClient struct {
	config         MCPConfig
	token, session string
	client         *http.Client
	next           int
	initialized    bool
}

func readMCP(ctx context.Context, config MCPConfig, token string) ([]sourceDocument, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	c := mcpClient{config: config, token: token, client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("MCP redirect blocked") }}}
	defer c.close()
	result, err := c.call(ctx, "initialize", map[string]any{"protocolVersion": mcpVersion, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "agentos-core", "version": "0.5.0"}})
	if err != nil {
		return nil, err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Resources json.RawMessage `json:"resources"`
		} `json:"capabilities"`
	}
	if json.Unmarshal(result, &init) != nil || init.ProtocolVersion != mcpVersion || len(init.Capabilities.Resources) == 0 || string(init.Capabilities.Resources) == "null" {
		return nil, errors.New("MCP version or resources capability unsupported")
	}
	var resources map[string]any
	if json.Unmarshal(init.Capabilities.Resources, &resources) != nil {
		return nil, errors.New("invalid MCP resources capability")
	}
	c.initialized = true
	if _, err = c.post(ctx, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, 0); err != nil {
		return nil, err
	}
	var docs []sourceDocument
	for _, uri := range config.URIs {
		result, err = c.call(ctx, "resources/read", map[string]string{"uri": uri})
		if err != nil {
			return nil, err
		}
		var read struct {
			Contents []struct {
				URI  string          `json:"uri"`
				Text *string         `json:"text"`
				Blob json.RawMessage `json:"blob"`
			} `json:"contents"`
		}
		if json.Unmarshal(result, &read) != nil || len(read.Contents) != 1 {
			return nil, errors.New("MCP requires exactly one text resource per authorized URI")
		}
		content := read.Contents[0]
		if content.URI != uri || content.Text == nil || len(content.Blob) > 0 || len(*content.Text) > 256*1024 || strings.TrimSpace(*content.Text) == "" || strings.ContainsRune(*content.Text, 0) || !utf8.ValidString(*content.Text) {
			return nil, errors.New("MCP returned unauthorized, binary, empty or oversized resource")
		}
		docs = append(docs, sourceDocument{Name: uri, Origin: "mcp", Endpoint: config.Endpoint, URI: uri, Text: *content.Text})
	}
	return docs, nil
}
func (c *mcpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.next++
	return c.post(ctx, map[string]any{"jsonrpc": "2.0", "id": c.next, "method": method, "params": params}, c.next)
}
func (c *mcpClient) headers(r *http.Request) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.session != "" {
		r.Header.Set("Mcp-Session-Id", c.session)
	}
	if c.initialized {
		r.Header.Set("MCP-Protocol-Version", mcpVersion)
	}
}
func (c *mcpClient) post(ctx context.Context, payload any, id int) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.config.Endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("invalid MCP request")
	}
	c.headers(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errors.New("MCP request failed or cancelled")
	}
	defer resp.Body.Close()
	if id == 0 {
		if resp.StatusCode != 202 {
			return nil, errors.New("MCP notification not accepted")
		}
		return nil, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("MCP returned HTTP %d; cycle stopped", resp.StatusCode)
	}
	if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
		if c.initialized && session != c.session {
			return nil, errors.New("MCP session unexpectedly changed")
		}
		if len(session) > 1024 {
			return nil, errors.New("MCP session ID too long")
		}
		for _, r := range session {
			if r < 0x21 || r > 0x7e {
				return nil, errors.New("invalid MCP session ID")
			}
		}
		c.session = session
	}
	kind, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, errors.New("invalid MCP content type")
	}
	if kind == "application/json" {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxMCPResponse+1))
		if err != nil || len(body) > maxMCPResponse {
			return nil, errors.New("MCP response exceeds limit or is unreadable")
		}
		result, done, err := decodeMCP(body, id)
		if err != nil {
			return nil, err
		}
		if !done {
			return nil, errors.New("MCP response missing result")
		}
		return result, nil
	}
	if kind != "text/event-stream" {
		return nil, errors.New("unsupported MCP content type")
	}
	// Bound the entire stream, including comments/notifications, not just each event.
	reader := &io.LimitedReader{R: resp.Body, N: maxMCPResponse + 1}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxMCPResponse+1)
	var data []string
	events := 0
	for scanner.Scan() {
		if reader.N == 0 {
			return nil, errors.New("MCP stream exceeds limit")
		}
		line := scanner.Text()
		if line == "" && len(data) > 0 {
			events++
			if events > 32 {
				return nil, errors.New("too many MCP events")
			}
			result, done, err := decodeMCP([]byte(strings.Join(data, "\n")), id)
			data = nil
			if err != nil {
				return nil, err
			}
			if done {
				return result, nil
			}
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return nil, errors.New("MCP stream ended without a complete response")
}
func decodeMCP(body []byte, id int) (json.RawMessage, bool, error) {
	var message struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if !utf8.Valid(body) || json.Unmarshal(body, &message) != nil || message.JSONRPC != "2.0" {
		return nil, false, errors.New("invalid MCP JSON-RPC message")
	}
	if message.Method != "" {
		if len(message.ID) == 0 && strings.HasPrefix(message.Method, "notifications/") && len(message.Result) == 0 && len(message.Error) == 0 {
			return nil, false, nil
		}
		return nil, false, errors.New("MCP server-initiated requests are not supported")
	}
	if string(message.ID) != fmt.Sprint(id) || len(message.Result) == 0 || string(message.Result) == "null" || len(message.Error) > 0 {
		return nil, false, errors.New("MCP response ID, result or error rejected")
	}
	return message.Result, true, nil
}
func (c *mcpClient) close() {
	if c.session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.config.Endpoint, nil)
	if err != nil {
		return
	}
	c.headers(req)
	resp, err := c.client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}
