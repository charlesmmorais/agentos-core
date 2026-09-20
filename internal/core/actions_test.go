package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func proposeAction(t *testing.T, s *Store, st *State, endpoint string) (*Controller, Intent) {
	t.Helper()
	st.Writes = &WritePolicy{endpoint, 3}
	c := NewController(s, st, &fake{})
	a, err := c.ActionControl("propose", "", "", RecordPayload{"report", "approved content"})
	if err != nil {
		t.Fatal(err)
	}
	return c, a
}

func TestActionCancelDuringWriteStillReconciles(t *testing.T) {
	t.Setenv("AGENTOS_WRITE_TOKEN", "test-token")
	entered, release := make(chan struct{}), make(chan struct{})
	var committed atomic.Bool
	var receipt WriteReceipt
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if !committed.Load() {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(receipt)
			return
		}
		var req struct {
			ActionID string `json:"action_id"`
			Digest   string `json:"digest"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		receipt = WriteReceipt{req.ActionID, req.Digest, req.ActionID, "committed"}
		committed.Store(true)
		close(entered)
		<-release
		json.NewEncoder(w).Encode(receipt)
	}))
	defer server.Close()
	s, st := setup(t)
	c, a := proposeAction(t, s, st, server.URL)
	c.ActionControl("approve", a.ID, a.Digest, RecordPayload{})
	done := make(chan error, 1)
	go func() { done <- c.RunAction(context.Background(), a.ID, false) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("write did not start")
	}
	if err := c.Control("cancel"); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not settle")
	}
	if c.Snapshot().Actions[0].Status != "succeeded" {
		if err := c.RunAction(context.Background(), a.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	if c.Snapshot().Status != "cancelled" || c.Snapshot().Actions[0].Status != "succeeded" {
		t.Fatal("lost effect or reversed cancellation")
	}
}

func TestActionAPIExactApproval(t *testing.T) {
	s, st := setup(t)
	c, a := proposeAction(t, s, st, "http://127.0.0.1:1")
	token := strings.Repeat("a", 32)
	handler := c.Handler(token)
	for _, test := range []struct {
		token, digest string
		status        int
	}{{"", a.Digest, 401}, {token, "wrong", 409}, {token, a.Digest, 200}} {
		body, _ := json.Marshal(map[string]string{"id": a.ID, "digest": test.digest})
		req := httptest.NewRequest("POST", "/v1/actions/approve", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+test.token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != test.status {
			t.Fatalf("approval status %d; want %d", recorder.Code, test.status)
		}
	}
	if c.Snapshot().Actions[0].Status != "approved" {
		t.Fatal("approval missing")
	}
}
func TestActionApprovalAndTamper(t *testing.T) {
	s, st := setup(t)
	c, a := proposeAction(t, s, st, "http://127.0.0.1:1")
	if c.RunAction(context.Background(), a.ID, false) == nil {
		t.Fatal("unapproved action executed")
	}
	if _, err := c.ActionControl("approve", a.ID, "wrong", RecordPayload{}); err == nil {
		t.Fatal("wrong hash approved")
	}
	if err := c.Step(context.Background(), time.Now().Add(time.Hour)); err != nil || c.Snapshot().Completed != 0 {
		t.Fatal("pending approval did not block cycles")
	}
	if _, err := c.ActionControl("approve", a.ID, a.Digest, RecordPayload{}); err != nil {
		t.Fatal(err)
	}
	st.Actions[0].Payload.Content = "changed after approval"
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("modified content accepted")
	}
	if c.RunAction(context.Background(), a.ID, false) == nil {
		t.Fatal("tampered action executed")
	}
}

func TestActionLostReceiptReconcilesWithoutWrite(t *testing.T) {
	t.Setenv("AGENTOS_WRITE_TOKEN", strings.Repeat("w", 32))
	var committed *WriteReceipt
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("w", 32) {
			t.Error("missing write token")
		}
		if r.Method == "GET" {
			if committed == nil {
				w.WriteHeader(404)
			} else {
				json.NewEncoder(w).Encode(committed)
			}
			return
		}
		if r.Method != "PUT" {
			t.Error("unexpected method")
			return
		}
		puts++
		var request struct {
			ActionID string `json:"action_id"`
			Digest   string `json:"digest"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		committed = &WriteReceipt{request.ActionID, request.Digest, request.ActionID, "committed"}
		w.WriteHeader(503) // committed, but caller cannot know this yet
	}))
	defer server.Close()
	s, st := setup(t)
	c, a := proposeAction(t, s, st, server.URL)
	c.ActionControl("approve", a.ID, a.Digest, RecordPayload{})
	if c.RunAction(context.Background(), a.ID, false) == nil {
		t.Fatal("missing receipt accepted")
	}
	restored, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if restored.Actions[0].Status != "unknown" || restored.Status != "paused" {
		t.Fatal("uncertain effect not preserved")
	}
	c = NewController(s, restored, &fake{})
	if _, err := c.ActionControl("retry", a.ID, a.Digest, RecordPayload{}); err == nil {
		t.Fatal("retry before reconciliation")
	}
	if err := c.Control("cancel"); err != nil {
		t.Fatal(err)
	}
	if err := c.RunAction(context.Background(), a.ID, true); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().Status != "cancelled" || c.Snapshot().Actions[0].Status != "succeeded" || puts != 1 {
		t.Fatal("reconciliation repeated effect or undid cancellation")
	}
}

func TestActionAbsentRequiresExplicitRetry(t *testing.T) {
	t.Setenv("AGENTOS_WRITE_TOKEN", "test-token")
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.WriteHeader(404)
			return
		}
		puts++
		var req struct {
			ActionID string `json:"action_id"`
			Digest   string `json:"digest"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(WriteReceipt{req.ActionID, req.Digest, req.ActionID, "committed"})
	}))
	defer server.Close()
	s, st := setup(t)
	c, a := proposeAction(t, s, st, server.URL)
	c.ActionControl("approve", a.ID, a.Digest, RecordPayload{})
	st.Actions[0].Status = "in_flight"
	st.Actions[0].Attempts = 1
	st.Actions[0].Checks = 1
	s.Save(st)
	restored, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	c = NewController(s, restored, &fake{})
	if err = c.Step(context.Background(), time.Now()); err == nil {
		t.Fatal("absence should require explicit retry")
	}
	if puts != 0 || c.Snapshot().Actions[0].Status != "retry_required" {
		t.Fatal("uncertain operation was resent")
	}
	if _, err = c.ActionControl("retry", a.ID, a.Digest, RecordPayload{}); err != nil {
		t.Fatal(err)
	}
	if err = c.Control("resume"); err != nil {
		t.Fatal(err)
	}
	if err = c.Step(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if puts != 1 || c.Snapshot().Actions[0].Status != "succeeded" {
		t.Fatal("authorized retry did not complete")
	}
}

func TestActionRedirectReceiptAndBudget(t *testing.T) {
	t.Setenv("AGENTOS_WRITE_TOKEN", "test-token")
	for _, mode := range []string{"redirect", "receipt", "budget"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "redirect" {
					http.Redirect(w, r, "http://127.0.0.1:1", 307)
				} else {
					json.NewEncoder(w).Encode(WriteReceipt{"wrong", "wrong", "wrong", "committed"})
				}
			}))
			defer server.Close()
			s, st := setup(t)
			c, a := proposeAction(t, s, st, server.URL)
			c.ActionControl("approve", a.ID, a.Digest, RecordPayload{})
			if mode == "budget" {
				st.Actions[0].Checks = 10
			}
			if c.RunAction(context.Background(), a.ID, false) == nil {
				t.Fatal("unsafe action accepted")
			}
			if mode == "budget" && calls != 0 {
				t.Fatal("budget allowed I/O")
			}
			if c.Snapshot().Actions[0].Receipt != nil {
				t.Fatal("invalid receipt persisted")
			}
		})
	}
}
