package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Controller is the sole snapshot writer. Executors never hold the state lock.
type Controller struct {
	mu       sync.Mutex
	store    *Store
	state    *State
	executor Executor
	cancel   context.CancelFunc
	running  bool
	epoch    uint64
	fatal    error
}

func NewController(s *Store, st *State, e Executor) *Controller {
	return &Controller{store: s, state: st, executor: e}
}
func (c *Controller) Snapshot() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(c.state)
	var result State
	json.Unmarshal(b, &result)
	return result
}
func (c *Controller) Control(action string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fatal != nil {
		return c.fatal
	}
	if c.state.Status == "cancelled" || c.state.Status == "completed" {
		return errors.New("terminal state")
	}
	status, ok := map[string]string{"pause": "paused", "resume": "active", "cancel": "cancelled"}[action]
	if !ok {
		return errors.New("unknown action")
	}
	c.state.Status = status
	c.state.Events = append(c.state.Events, Event{time.Now().UTC(), action, c.state.Completed})
	c.epoch++
	if c.cancel != nil {
		c.cancel()
	}
	c.fatal = c.store.Save(c.state)
	return c.fatal
}
func (c *Controller) Step(ctx context.Context, now time.Time) error {
	c.mu.Lock()
	if c.fatal != nil {
		err := c.fatal
		c.mu.Unlock()
		return err
	}
	if c.running || c.state.Status != "active" {
		c.mu.Unlock()
		return nil
	}
	st := c.state
	if index := nextAction(st); index >= 0 {
		if st.Actions[index].Status == "proposed" || st.Actions[index].Status == "retry_required" {
			c.mu.Unlock()
			return nil
		}
		return c.stepActionLocked(ctx, index, false)
	}
	if now.Before(st.NextRun) {
		c.mu.Unlock()
		return nil
	}
	if st.Completed >= st.Protocol.MaxCycles {
		st.Status = "completed"
		c.fatal = c.store.Save(st)
		err := c.fatal
		c.mu.Unlock()
		return err
	}
	if st.Pending == "" {
		st.Pending = fmt.Sprintf("%s:%d", st.AgentID, st.Completed+1)
		st.Events = append(st.Events, Event{now, "started", st.Completed + 1})
	}
	if err := reserveModel(c.store, st); err != nil {
		if !errors.Is(err, errModelBudget) {
			c.fatal = err
		}
		c.mu.Unlock()
		return err
	}
	if err := c.store.Save(st); err != nil {
		c.fatal = err
		c.mu.Unlock()
		return err
	}
	runctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.running = true
	epoch := c.epoch
	request := Request{st.AgentID, st.Protocol.Mission, st.Pending, st.Protocol.Workspace, append(json.RawMessage(nil), st.Memory...)}
	protocol := st.Protocol
	c.mu.Unlock()
	result, err := c.executor.Execute(runctx, protocol, request)
	cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	c.cancel = nil
	if epoch != c.epoch || ctx.Err() != nil {
		return nil
	}
	if err != nil {
		st.Status = "paused"
		st.Events = append(st.Events, Event{time.Now().UTC(), "execution_failed", st.Completed + 1})
		if saveErr := c.store.Save(st); saveErr != nil {
			c.fatal = saveErr
			return saveErr
		}
		return err
	}
	artifact, err := c.store.PutArtifact(st.Completed+1, result)
	if err != nil {
		c.fatal = err
		return err
	}
	st.Artifacts = append(st.Artifacts, artifact)
	st.Memory = result
	st.Completed++
	st.Pending = ""
	st.NextRun = time.Now().UTC().Add(time.Duration(protocol.IntervalSeconds) * time.Second)
	st.Events = append(st.Events, Event{time.Now().UTC(), "completed", st.Completed})
	if st.Completed >= protocol.MaxCycles {
		st.Status = "completed"
	}
	c.fatal = c.store.Save(st)
	return c.fatal
}
func (c *Controller) Artifact(cycle int) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.state.Artifacts {
		if a.Cycle == cycle {
			return c.store.ReadArtifact(a)
		}
	}
	return nil, errors.New("artifact not found")
}
