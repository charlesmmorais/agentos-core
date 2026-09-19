package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type blocking struct{ started chan struct{} }

func (b blocking) Execute(ctx context.Context, _ Protocol, _ Request) (json.RawMessage, error) {
	close(b.started)
	<-ctx.Done()
	return json.RawMessage(`{"late":true}`), nil
}
func TestControllerCancelDiscardsLateResult(t *testing.T) {
	s, st := setup(t)
	e := blocking{make(chan struct{})}
	c := NewController(s, st, e)
	done := make(chan error, 1)
	go func() { done <- c.Step(context.Background(), time.Now().Add(time.Second)) }()
	<-e.started
	if c.Snapshot().Pending == "" {
		t.Fatal("missing pending state")
	}
	if err := c.Control("cancel"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	saved, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "cancelled" || saved.Completed != 0 || len(saved.Artifacts) != 0 {
		t.Fatal("late result committed")
	}
	if c.Control("resume") == nil {
		t.Fatal("revived cancelled mission")
	}
}
func TestControllerPauseResume(t *testing.T) {
	s, st := setup(t)
	e := blocking{make(chan struct{})}
	c := NewController(s, st, e)
	done := make(chan error, 1)
	go func() { done <- c.Step(context.Background(), time.Now().Add(time.Second)) }()
	<-e.started
	if err := c.Control("pause"); err != nil {
		t.Fatal(err)
	}
	if err := c.Control("resume"); err != nil {
		t.Fatal(err)
	}
	<-done
	if c.Snapshot().Completed != 0 {
		t.Fatal("stale result committed after resume")
	}
	c.executor = &fake{}
	if err := c.Step(context.Background(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().Completed != 1 {
		t.Fatal("resume failed")
	}
}
func TestHTTPAuthorizationAndArtifact(t *testing.T) {
	s, st := setup(t)
	c := NewController(s, st, &fake{})
	token := strings.Repeat("x", 32)
	h := c.Handler(token)
	req := httptest.NewRequest("POST", "/v1/control/cancel", nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 401 || c.Snapshot().Status != "active" {
		t.Fatal("unauthenticated mutation")
	}
	if err := c.Step(context.Background(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("GET", "/v1/artifacts/1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 200 || !json.Valid(res.Body.Bytes()) {
		t.Fatal("artifact unavailable")
	}
	a := c.Snapshot().Artifacts[0]
	os.WriteFile(filepath.Join(s.Dir, "artifacts", a.SHA256+".json"), []byte("corrupt"), 0600)
	if _, err := c.Artifact(1); err == nil {
		t.Fatal("corruption not detected")
	}
}
func TestPinnedScriptAndEnvironment(t *testing.T) {
	p := Protocol{Workspace: t.TempDir(), Script: filepath.Join(t.TempDir(), "script.py"), TimeoutSeconds: 3}
	os.WriteFile(p.Script, []byte("print('{}')\n"), 0600)
	m, err := Inspect(p)
	if err != nil {
		t.Fatal(err)
	}
	e := PinnedPython{m}
	if _, err = e.Execute(context.Background(), p, Request{}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p.Script, []byte("print('{\"changed\":true}')\n"), 0600)
	if _, err = e.Execute(context.Background(), p, Request{}); err == nil {
		t.Fatal("script drift accepted")
	}
	os.WriteFile(p.Script, []byte("print('{}')\n"), 0600)
	m.EnvironmentSHA256 = "changed"
	if _, err = e.Execute(context.Background(), p, Request{}); err == nil {
		t.Fatal("environment drift accepted")
	}
}
