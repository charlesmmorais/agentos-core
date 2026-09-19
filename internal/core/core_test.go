package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fake struct {
	fail bool
	ids  []string
}

func (f *fake) Execute(_ context.Context, _ Protocol, r Request) (json.RawMessage, error) {
	f.ids = append(f.ids, r.CycleID)
	if f.fail {
		return nil, errors.New("interrupted")
	}
	return json.RawMessage(`{"ok":true}`), nil
}
func setup(t *testing.T) (*Store, *State) {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	st, e := New(Protocol{"test", t.TempDir(), "/trusted.py", 1, 1, 2})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Save(st); e != nil {
		t.Fatal(e)
	}
	return s, st
}
func TestRestart(t *testing.T) {
	s, st := setup(t)
	f := &fake{fail: true}
	now := time.Now().Add(time.Second)
	if Tick(context.Background(), s, st, f, now) == nil {
		t.Fatal("expected failure")
	}
	restored, e := s.Load()
	if e != nil {
		t.Fatal(e)
	}
	if restored.Completed != 0 || restored.Pending == "" {
		t.Fatal("missing pending checkpoint")
	}
	f.fail = false
	if e = Tick(context.Background(), s, restored, f, now); e != nil {
		t.Fatal(e)
	}
	if f.ids[0] != f.ids[1] || restored.Completed != 1 {
		t.Fatal("incorrect replay")
	}
	again, e := s.Load()
	if e != nil || again.AgentID != st.AgentID || again.Pending != "" {
		t.Fatal("state not preserved")
	}
}
func TestPausedAndCancelled(t *testing.T) {
	s, st := setup(t)
	f := &fake{}
	for _, status := range []string{"paused", "cancelled"} {
		st.Status = status
		if e := Tick(context.Background(), s, st, f, time.Now().Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
	}
	if len(f.ids) != 0 {
		t.Fatal("executed while stopped")
	}
}
func TestLockAndCorruption(t *testing.T) {
	s, _ := setup(t)
	if other, e := Open(s.Dir); e == nil {
		other.Close()
		t.Fatal("double writer allowed")
	}
	if e := os.WriteFile(filepath.Join(s.Dir, "state.json"), []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Load(); e == nil {
		t.Fatal("accepted corrupt state")
	}
}
func TestBudget(t *testing.T) {
	s, st := setup(t)
	f := &fake{}
	for i := 0; i < 4; i++ {
		if e := Tick(context.Background(), s, st, f, time.Now().Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
	}
	if len(f.ids) != 2 || st.Status != "completed" {
		t.Fatal("budget not enforced")
	}
}
func TestPython(t *testing.T) {
	p := Protocol{Workspace: t.TempDir(), Script: filepath.Join(t.TempDir(), "test.py"), TimeoutSeconds: 1}
	if e := os.WriteFile(p.Script, []byte("import json,sys\nprint(json.dumps({'ok':json.load(sys.stdin)['cycle_id']}))\n"), 0600); e != nil {
		t.Fatal(e)
	}
	b, e := (Python{}).Execute(context.Background(), p, Request{CycleID: "test"})
	if e != nil || !json.Valid(b) {
		t.Fatalf("python: %s %v", b, e)
	}
}
func TestPythonTimeout(t *testing.T) {
	p := Protocol{Workspace: t.TempDir(), Script: filepath.Join(t.TempDir(), "sleep.py"), TimeoutSeconds: 1}
	os.WriteFile(p.Script, []byte("import time\ntime.sleep(60)\n"), 0600)
	start := time.Now()
	if _, e := (Python{}).Execute(context.Background(), p, Request{}); e == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout not bounded")
	}
}
