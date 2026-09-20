// Package core implements a single-host durable protocol runner. Host adapters enforce platform capabilities.
package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/charlesmmorais/agentos-core/internal/kernel"
	"github.com/charlesmmorais/agentos-core/internal/platform"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Protocol = kernel.Protocol
type Event = kernel.Event
type State struct {
	Version       int              `json:"version"`
	AgentID       string           `json:"agent_id"`
	Protocol      Protocol         `json:"protocol"`
	Status        string           `json:"status"`
	Completed     int              `json:"completed"`
	NextRun       time.Time        `json:"next_run"`
	Pending       string           `json:"pending,omitempty"`
	Memory        json.RawMessage  `json:"memory,omitempty"`
	Events        []Event          `json:"events"`
	Execution     *Manifest        `json:"execution,omitempty"`
	Artifacts     []Artifact       `json:"artifacts,omitempty"`
	Cognition     *CognitionConfig `json:"cognition,omitempty"`
	ModelAttempts int              `json:"model_attempts"`
	Retrieval     *RetrievalConfig `json:"retrieval,omitempty"`
	Writes        *WritePolicy     `json:"writes,omitempty"`
	Actions       []Intent         `json:"actions,omitempty"`
	Recovery      *RecoveryState   `json:"recovery,omitempty"`
}
type Store struct {
	Dir  string
	lock *os.File
}

func Open(dir string) (*Store, error) {
	if err := platform.Current().Require(platform.DurableState); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockFile(f); err != nil {
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
	return decodeState(b)
}
func decodeState(b []byte) (*State, error) {
	var err error
	var st State
	if err = json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	if st.Version != 1 || st.AgentID == "" || st.ModelAttempts < 0 {
		return nil, errors.New("unsupported or invalid state")
	}
	if err = validateHostProtocol(st.Protocol); err != nil {
		return nil, err
	}
	if st.Cognition != nil {
		if err = st.Cognition.Validate(); err != nil {
			return nil, err
		}
	}
	if st.Retrieval != nil {
		if st.Cognition == nil {
			return nil, errors.New("retrieval requires cognition")
		}
		if err = st.Retrieval.Validate(); err != nil {
			return nil, err
		}
	}
	switch st.Status {
	case "active", "paused", "cancelled", "completed":
	default:
		return nil, errors.New("invalid status")
	}
	if err = st.validateActions(); err != nil {
		return nil, err
	}
	return &st, nil
}
func (s *Store) Save(st *State) error {
	if err := platform.Current().Require(platform.DurableState); err != nil {
		return err
	}
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
func New(p Protocol) (*State, error) {
	if err := validateHostProtocol(p); err != nil {
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
	if err := platform.Current().Require(platform.NativeProcess); err != nil {
		return nil, err
	}
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
	if err := configureProcess(cmd); err != nil {
		return nil, err
	}
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
	if st.Status == "active" {
		if err := RecoveryReady(st); err != nil {
			return err
		}
	}
	if st.Status == "active" && nextAction(st) >= 0 {
		return NewController(s, st, e).Step(ctx, now)
	}
	if !kernel.Due(st.Status, st.NextRun, now) {
		return nil
	}
	if st.Completed >= st.Protocol.MaxCycles {
		st.Status = "completed"
		return s.Save(st)
	}
	if st.Pending == "" {
		st.Pending = fmt.Sprintf("%s:%d", st.AgentID, st.Completed+1)
		st.Events = append(st.Events, Event{At: now, Kind: "started", Cycle: st.Completed + 1})
	}
	if err := reserveModel(s, st); err != nil {
		return err
	}
	if err := s.Save(st); err != nil {
		return err
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
	st.Events = append(st.Events, Event{At: time.Now().UTC(), Kind: "completed", Cycle: st.Completed})
	if st.Completed >= st.Protocol.MaxCycles {
		st.Status = "completed"
	}
	return s.Save(st)
}

func validateHostProtocol(p Protocol) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(p.Workspace) || !filepath.IsAbs(p.Script) {
		return errors.New("protocol paths are not absolute on this host")
	}
	return nil
}
