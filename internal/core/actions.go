package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type WritePolicy struct {
	Endpoint    string `json:"endpoint"`
	MaxAttempts int    `json:"max_attempts"`
}

func (p WritePolicy) Validate() error {
	if err := (CognitionConfig{BaseURL: p.Endpoint, Model: "validation", MaxCalls: 1, MaxTokens: 128}).Validate(); err != nil {
		return errors.New("invalid write endpoint: HTTPS or literal loopback HTTP required")
	}
	if p.MaxAttempts < 1 || p.MaxAttempts > 5 {
		return errors.New("write attempts must be 1..5")
	}
	return nil
}

type RecordPayload struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

func (p RecordPayload) Validate() error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 120 || strings.TrimSpace(p.Content) == "" || len(p.Content) > 8192 {
		return errors.New("record requires name (1..120 bytes) and content (1..8192 bytes)")
	}
	return nil
}

type WriteReceipt struct {
	ActionID string `json:"action_id"`
	Digest   string `json:"digest"`
	RecordID string `json:"record_id"`
	Status   string `json:"status"`
}
type Intent struct {
	ID             string        `json:"id"`
	Operation      string        `json:"operation"`
	Endpoint       string        `json:"endpoint"`
	Payload        RecordPayload `json:"payload"`
	Digest         string        `json:"digest"`
	ApprovedDigest string        `json:"approved_digest,omitempty"`
	Status         string        `json:"status"`
	Attempts       int           `json:"attempts"`
	Checks         int           `json:"checks"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	Receipt        *WriteReceipt `json:"receipt,omitempty"`
	LastError      string        `json:"last_error,omitempty"`
}

func (a Intent) hash(agentID string) string {
	b, _ := json.Marshal(struct {
		AgentID, ID, Operation, Endpoint string
		Payload                          RecordPayload
	}{agentID, a.ID, a.Operation, a.Endpoint, a.Payload})
	return digest(b)
}
func (a Intent) validReceipt(r *WriteReceipt) bool {
	return r != nil && r.ActionID == a.ID && r.Digest == a.Digest && r.RecordID == a.ID && r.Status == "committed"
}
func (st *State) validateActions() error {
	if st.Writes != nil {
		if err := st.Writes.Validate(); err != nil {
			return err
		}
	}
	if len(st.Actions) > 32 || len(st.Actions) > 0 && st.Writes == nil {
		return errors.New("invalid action policy or count")
	}
	seen := map[string]bool{}
	for _, a := range st.Actions {
		id, err := hex.DecodeString(a.ID)
		if err != nil || len(id) != 16 || seen[a.ID] || a.Operation != "record.create" || a.Endpoint != st.Writes.Endpoint || a.Digest != a.hash(st.AgentID) || a.Payload.Validate() != nil || a.Attempts < 0 || a.Attempts > st.Writes.MaxAttempts || a.Checks < 0 || a.Checks > 10 {
			return errors.New("invalid or modified action")
		}
		seen[a.ID] = true
		if a.ApprovedDigest != "" && a.ApprovedDigest != a.Digest {
			return errors.New("action approval mismatch")
		}
		switch a.Status {
		case "proposed", "rejected":
		case "approved", "in_flight", "unknown", "retry_required", "succeeded":
			if a.ApprovedDigest != a.Digest {
				return errors.New("action lacks exact approval")
			}
		default:
			return errors.New("invalid action status")
		}
		if a.Status == "succeeded" && !a.validReceipt(a.Receipt) || a.Status != "succeeded" && a.Receipt != nil {
			return errors.New("invalid action receipt")
		}
	}
	return nil
}
func nextAction(st *State) int {
	for i, a := range st.Actions {
		if a.Status != "succeeded" && a.Status != "rejected" {
			return i
		}
	}
	return -1
}
func (c *Controller) saveAction(a *Intent) error {
	a.UpdatedAt = time.Now().UTC()
	c.state.Events = append(c.state.Events, Event{At: a.UpdatedAt, Kind: "action_" + a.Status + ":" + a.ID, Cycle: c.state.Completed})
	c.fatal = c.store.Save(c.state)
	return c.fatal
}

// ActionControl accepts only explicit operator requests, never model output.
func (c *Controller) ActionControl(command, id, hash string, payload RecordPayload) (Intent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := RecoveryReady(c.state); err != nil {
		return Intent{}, err
	}
	if c.fatal != nil {
		return Intent{}, c.fatal
	}
	if c.running {
		return Intent{}, errors.New("execution in progress; try again after it settles")
	}
	if c.state.Status == "cancelled" || c.state.Status == "completed" {
		return Intent{}, errors.New("terminal mission cannot authorize actions")
	}
	if c.state.Writes == nil {
		return Intent{}, errors.New("write policy not configured")
	}
	if err := c.state.validateActions(); err != nil {
		return Intent{}, err
	}
	if command == "propose" {
		if err := payload.Validate(); err != nil {
			return Intent{}, err
		}
		if len(c.state.Actions) >= 32 {
			return Intent{}, errors.New("mission action limit reached")
		}
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return Intent{}, err
		}
		a := Intent{ID: hex.EncodeToString(b), Operation: "record.create", Endpoint: c.state.Writes.Endpoint, Payload: payload, Status: "proposed", CreatedAt: time.Now().UTC()}
		a.Digest = a.hash(c.state.AgentID)
		c.state.Actions = append(c.state.Actions, a)
		aPtr := &c.state.Actions[len(c.state.Actions)-1]
		err := c.saveAction(aPtr)
		return *aPtr, err
	}
	for i := range c.state.Actions {
		a := &c.state.Actions[i]
		if a.ID != id {
			continue
		}
		if hash != a.Digest {
			return Intent{}, errors.New("exact reviewed digest required")
		}
		switch command {
		case "approve":
			if a.Status != "proposed" {
				return Intent{}, errors.New("only proposed actions can be approved")
			}
			a.ApprovedDigest = hash
			a.Status = "approved"
		case "retry":
			if a.Status != "retry_required" || a.Attempts >= c.state.Writes.MaxAttempts || a.Checks >= 10 {
				return Intent{}, errors.New("retry requires reconciled absence and remaining budget")
			}
			a.Status = "approved"
		case "reject":
			if (a.Status != "proposed" && a.Status != "approved") || a.Attempts > 0 {
				return Intent{}, errors.New("uncertain or completed effects must be reconciled")
			}
			a.Status = "rejected"
		default:
			return Intent{}, errors.New("unknown action command")
		}
		a.LastError = ""
		err := c.saveAction(a)
		return *a, err
	}
	return Intent{}, errors.New("action not found")
}

// RunAction is also used for read-only reconciliation after pause/cancel.
func (c *Controller) RunAction(ctx context.Context, id string, readOnly bool) error {
	c.mu.Lock()
	if !readOnly {
		if err := RecoveryReady(c.state); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	if c.fatal != nil {
		err := c.fatal
		c.mu.Unlock()
		return err
	}
	if c.running || !readOnly && c.state.Status != "active" {
		c.mu.Unlock()
		return errors.New("action execution is blocked")
	}
	for i, a := range c.state.Actions {
		if a.ID == id {
			return c.stepActionLocked(ctx, i, readOnly)
		}
	}
	c.mu.Unlock()
	return errors.New("action not found")
}

// Called holding mu; always releases it. The persisted in_flight marker precedes
// any network I/O. On restart it permits GET only, never an automatic PUT replay.
func (c *Controller) stepActionLocked(ctx context.Context, index int, readOnly bool) error {
	if err := c.state.validateActions(); err != nil {
		c.mu.Unlock()
		return err
	}
	a := &c.state.Actions[index]
	if a.Status != "approved" && a.Status != "in_flight" && a.Status != "unknown" && !(readOnly && a.Status == "retry_required") {
		c.mu.Unlock()
		return errors.New("action is not approved or reconcilable")
	}
	allowWrite := !readOnly && a.Status == "approved"
	if a.Checks >= 10 || allowWrite && a.Attempts >= c.state.Writes.MaxAttempts {
		if c.state.Status == "active" {
			c.state.Status = "paused"
		}
		a.LastError = "persistent action budget exhausted"
		err := c.saveAction(a)
		c.mu.Unlock()
		if err != nil {
			return err
		}
		return errors.New("persistent action budget exhausted")
	}
	a.Checks++
	if allowWrite {
		a.Attempts++
	}
	a.Status = "in_flight"
	a.LastError = ""
	if err := c.saveAction(a); err != nil {
		c.mu.Unlock()
		return err
	}
	copy := *a
	runctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.running = true
	c.mu.Unlock()
	receipt, absent, err := executeRecord(runctx, copy, allowWrite)
	cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancel = nil
	c.running = false
	a = &c.state.Actions[index]
	// Unlike read-only cycles, receipts cannot be discarded after a control change:
	// pause/cancel does not undo an external effect. Mission status is preserved.
	if err == nil && copy.validReceipt(receipt) {
		a.Status = "succeeded"
		a.Receipt = receipt
	} else if err == nil && absent {
		a.Status = "retry_required"
		a.LastError = "destination reported absence; explicit retry required"
	} else {
		a.Status = "unknown"
		a.LastError = "result unknown; reconciliation required"
		if err == nil {
			err = errors.New("invalid receipt")
		}
	}
	if (err != nil || absent) && c.state.Status == "active" {
		c.state.Status = "paused"
	}
	if saveErr := c.saveAction(a); saveErr != nil {
		return saveErr
	}
	if absent {
		return errors.New(a.LastError)
	}
	if err != nil {
		return fmt.Errorf("action %s outcome unknown; reconcile before retry", a.ID)
	}
	return nil
}
