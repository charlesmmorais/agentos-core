package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCognitiveMemoryAndBudget(t *testing.T) {
	s, st := setup(t)
	if err := os.WriteFile(filepath.Join(st.Protocol.Workspace, "report.txt"), []byte("Revenue increased by ten percent."), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer private-test-key" {
			t.Error("incorrect request")
		}
		var payload struct {
			Messages  []struct{ Content string }
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if len(payload.Messages) != 2 || payload.MaxTokens != 256 {
			t.Error("incorrect context budget")
			return
		}
		var input struct{ Sources []Source }
		if err := json.Unmarshal([]byte(payload.Messages[1].Content), &input); err != nil || len(input.Sources) != 1 {
			t.Error("missing sources")
			return
		}
		content, _ := json.Marshal(Analysis{"Revenue observation", []Claim{{"Revenue grew", input.Sources[0].ID, "Revenue increased by ten percent."}}})
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(content)}}}})
	}))
	defer server.Close()
	st.Cognition = &CognitionConfig{server.URL + "/v1", "mock", 1, 256}
	executor := CognitiveExecutor{&fake{}, *st.Cognition, "private-test-key"}
	if err := Tick(context.Background(), s, st, executor, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	restored, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if restored.Completed != 1 || restored.ModelAttempts != 1 || len(restored.Artifacts) != 1 {
		t.Fatal("memory checkpoint missing")
	}
	artifact, err := s.ReadArtifact(restored.Artifacts[0])
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	json.Compact(&compact, restored.Memory)
	if string(artifact) != compact.String() || !strings.Contains(string(artifact), "quotes_verified_claims_unverified") || strings.Contains(string(artifact), "private-test-key") {
		t.Fatal("incorrect memory artifact")
	}
	if err := Tick(context.Background(), s, restored, executor, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("budget bypassed")
	}
	again, err := s.Load()
	if err != nil || again.Status != "paused" || again.ModelAttempts != 1 || calls != 1 {
		t.Fatal("budget not durable")
	}
}

func TestFailedModelAttemptSurvivesRestart(t *testing.T) {
	s, st := setup(t)
	st.Cognition = &CognitionConfig{"http://127.0.0.1:1/v1", "mock", 1, 256}
	st.Memory = json.RawMessage(`{"previous":true}`)
	c := NewController(s, st, &fake{fail: true})
	if c.Step(context.Background(), time.Now().Add(time.Hour)) == nil {
		t.Fatal("expected failure")
	}
	restored, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	var previous map[string]bool
	json.Unmarshal(restored.Memory, &previous)
	if restored.ModelAttempts != 1 || restored.Completed != 0 || !previous["previous"] {
		t.Fatal("lost reservation or prior memory")
	}
	c = NewController(s, restored, &fake{})
	if err := c.Control("resume"); err != nil {
		t.Fatal(err)
	}
	if c.Step(context.Background(), time.Now().Add(time.Hour)) == nil || c.Snapshot().Completed != 0 {
		t.Fatal("restart reset budget")
	}
}

func TestAnalysisEvidenceRejection(t *testing.T) {
	sources := []Source{{ID: "known", Text: "Original source sentence."}}
	for _, a := range []Analysis{
		{"ok", []Claim{{"claim", "unknown", "Original source sentence."}}},
		{"ok", []Claim{{"claim", "known", "Fabricated quote."}}},
		{"ok", nil},
	} {
		b, _ := json.Marshal(a)
		if _, err := validateAnalysis(string(b), sources); err == nil {
			t.Fatal("invalid evidence accepted")
		}
	}
	for _, content := range []string{`{"summary":"ok","claims":[],"command":"execute"}`, `{} {}`, "not json"} {
		if _, err := validateAnalysis(content, sources); err == nil {
			t.Fatal("invalid schema accepted")
		}
	}
}

func TestModelRejectsProviderFailures(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "source.md"), []byte("Original source sentence."), 0600)
	for _, body := range []string{
		`{"choices":[{"finish_reason":"length","message":{"content":"{}"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"content":"{}","tool_calls":[{}]}}]}`,
		`{"choices":[]}`, strings.Repeat("x", 512*1024+1),
	} {
		t.Run(body[:10], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer server.Close()
			e := CognitiveExecutor{&fake{}, CognitionConfig{server.URL, "mock", 1, 256}, ""}
			if _, err := e.Execute(context.Background(), Protocol{Workspace: workspace}, Request{}); err == nil {
				t.Fatal("invalid provider response accepted")
			}
		})
	}
}

func TestModelRedirectAndCancellation(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("Original source sentence."), 0600)
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	e := CognitiveExecutor{&fake{}, CognitionConfig{redirect.URL, "mock", 1, 256}, "secret"}
	if _, err := e.Execute(context.Background(), Protocol{Workspace: workspace}, Request{}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("unsafe redirect result")
	}
	if reached {
		t.Fatal("redirect forwarded credentials")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Execute(ctx, Protocol{Workspace: workspace}, Request{}); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestSourceBoundsAndConfig(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte(strings.Repeat("é", 2000)), 0600)
	sources, err := collectSources(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || !sources[0].Truncated || len(sources[0].Text) != 2048 || sources[0].SHA256 == sources[0].ExcerptSHA256 {
		t.Fatal("incorrect source bounds")
	}
	if _, err := collectSources(t.TempDir()); err == nil {
		t.Fatal("missing evidence accepted")
	}
	for _, url := range []string{"http://example.com/v1", "http://localhost/v1", "https://user:secret@example.com", "https://example.com?key=secret", "file:///tmp/model"} {
		if (CognitionConfig{url, "mock", 1, 256}).Validate() == nil {
			t.Fatal("unsafe config accepted", url)
		}
	}
}
