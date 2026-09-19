// Package core implements a single-host durable protocol runner. Linux only.
package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type Protocol struct {
	Mission         string `json:"mission"`
	Workspace       string `json:"workspace"`
	Script          string `json:"script"`
	IntervalSeconds int    `json:"interval_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	MaxCycles       int    `json:"max_cycles"`
}
type Event struct {
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	Cycle int       `json:"cycle"`
}
type State struct {
	Version   int             `json:"version"`
	AgentID   string          `json:"agent_id"`
	Protocol  Protocol        `json:"protocol"`
	Status    string          `json:"status"`
	Completed int             `json:"completed"`
	NextRun   time.Time       `json:"next_run"`
	Pending   string          `json:"pending,omitempty"`
	Memory    json.RawMessage `json:"memory,omitempty"`
	Events    []Event         `json:"events"`
	Execution *Manifest       `json:"execution,omitempty"`
	Artifacts []Artifact      `json:"artifacts,omitempty"`
}
type Store struct {
	Dir  string
	lock *os.File
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("state already in use: %w", err)
	}
	return &Store{Dir: dir, lock: f}, nil
}
func (s *Store) Close() { s.lock.Close() }
func (s *Store) Load() (*State, error) {
	b, err := os.ReadFile(filepath.Join(s.Dir, "state.json"))
	if err != nil {
		return nil, err
	}
	var st State
	if err = json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	if st.Version != 1 || st.AgentID == "" {
		return nil, errors.New("unsupported or invalid state")
	}
	if err = st.Protocol.Validate(); err != nil {
		return nil, err
	}
	switch st.Status {
	case "active", "paused", "cancelled", "completed":
	default:
		return nil, errors.New("invalid status")
	}
	return &st, nil
}
func (s *Store) Save(st *State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(s.Dir, "state.json")); err != nil {
		return err
	}
	d, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (p Protocol) Validate() error {
	if p.Mission == "" || !filepath.IsAbs(p.Workspace) || !filepath.IsAbs(p.Script) || p.IntervalSeconds < 1 || p.TimeoutSeconds < 1 || p.MaxCycles < 1 || p.MaxCycles > 10000 {
		return errors.New("invalid protocol: absolute paths, positive interval/timeout, max_cycles 1..10000 required")
	}
	return nil
}
func New(p Protocol) (*State, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	return &State{Version: 1, AgentID: hex.EncodeToString(id), Protocol: p, Status: "active", NextRun: time.Now().UTC()}, nil
}

type Request struct {
	AgentID   string          `json:"agent_id"`
	Mission   string          `json:"mission"`
	CycleID   string          `json:"cycle_id"`
	Workspace string          `json:"workspace"`
	Memory    json.RawMessage `json:"memory,omitempty"`
}
type Executor interface {
	Execute(context.Context, Protocol, Request) (json.RawMessage, error)
}
type Python struct{}
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1024*1024 {
		return 0, errors.New("executor output exceeds 1 MiB")
	}
	return b.Buffer.Write(p)
}
func (Python) Execute(ctx context.Context, p Protocol, r Request) (json.RawMessage, error) {
	return executePython(ctx, p, r, "python3", []string{"-I", p.Script})
}
func executePython(ctx context.Context, p Protocol, r Request, python string, args []string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	input, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir = p.Workspace
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	cmd.Stdin = bytes.NewReader(input)
	var out, stderr limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("python execution failed: %w", err)
	}
	var result map[string]json.RawMessage
	if err = json.Unmarshal(out.Bytes(), &result); err != nil || result == nil {
		return nil, errors.New("executor must return a JSON object")
	}
	return append(json.RawMessage(nil), out.Bytes()...), nil
}

// Tick retries an interrupted read-only cycle with the same identifier.
// Arbitrary external writes are NOT safe to replay through this runner.
func Tick(ctx context.Context, s *Store, st *State, e Executor, now time.Time) error {
	if st.Status != "active" || now.Before(st.NextRun) {
		return nil
	}
	if st.Completed >= st.Protocol.MaxCycles {
		st.Status = "completed"
		return s.Save(st)
	}
	if st.Pending == "" {
		st.Pending = fmt.Sprintf("%s:%d", st.AgentID, st.Completed+1)
		st.Events = append(st.Events, Event{now, "started", st.Completed + 1})
		if err := s.Save(st); err != nil {
			return err
		}
	}
	result, err := e.Execute(ctx, st.Protocol, Request{st.AgentID, st.Protocol.Mission, st.Pending, st.Protocol.Workspace, st.Memory})
	if err != nil {
		return err
	}
	artifact, err := s.PutArtifact(st.Completed+1, result)
	if err != nil {
		return err
	}
	st.Artifacts = append(st.Artifacts, artifact)
	st.Memory = result
	st.Completed++
	st.Pending = ""
	st.NextRun = time.Now().UTC().Add(time.Duration(st.Protocol.IntervalSeconds) * time.Second)
	st.Events = append(st.Events, Event{time.Now().UTC(), "completed", st.Completed})
	if st.Completed >= st.Protocol.MaxCycles {
		st.Status = "completed"
	}
	return s.Save(st)
}
