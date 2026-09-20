// Package kernel contains deterministic protocol rules without host I/O.
package kernel

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
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

// Absolute paths are syntax checked for both supported host families. Host adapters
// remain responsible for interpreting paths and authorizing actual filesystem access.
func AbsolutePath(s string) bool {
	if strings.ContainsRune(s, 0) {
		return false
	}
	if strings.HasPrefix(s, "/") {
		return true
	}
	if len(s) >= 3 && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) && s[1] == ':' && (s[2] == '/' || s[2] == '\\') {
		return true
	}
	if strings.HasPrefix(s, `\\`) {
		parts := strings.Split(strings.TrimPrefix(s, `\\`), `\`)
		return len(parts) >= 2 && parts[0] != "" && parts[0] != "?" && parts[0] != "." && parts[1] != ""
	}
	return false
}
func (p Protocol) Validate() error {
	if strings.TrimSpace(p.Mission) == "" || !AbsolutePath(p.Workspace) || !AbsolutePath(p.Script) || p.IntervalSeconds < 1 || p.TimeoutSeconds < 1 || p.MaxCycles < 1 || p.MaxCycles > 10000 {
		return errors.New("invalid protocol: absolute paths, positive interval/timeout, max_cycles 1..10000 required")
	}
	return nil
}
func Due(status string, next, now time.Time) bool { return status == "active" && !now.Before(next) }
func Transition(status, action string) (string, error) {
	if status != "active" && status != "paused" {
		return "", errors.New("terminal or invalid state")
	}
	switch action {
	case "pause":
		return "paused", nil
	case "resume":
		return "active", nil
	case "cancel":
		return "cancelled", nil
	}
	return "", errors.New("unknown action")
}

// Simulate is read-only: it creates no state, runs no script and approves no action.
func Simulate(p Protocol) (map[string]any, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return map[string]any{"valid": true, "mission": p.Mission, "max_cycles": p.MaxCycles, "execution": false, "persistence": false}, nil
}

// DecodeProtocol gives every frontend identical strict, bounded decoding.
func DecodeProtocol(raw []byte) (Protocol, error) {
	var p Protocol
	if len(raw) > 65536 {
		return p, errors.New("protocol exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return p, errors.New("expected one protocol object")
	}
	return p, p.Validate()
}
