// Package harness defines deterministic planning and verification primitives.
// It contains no model or host I/O. Models may propose artifacts; the harness
// validates their structure and controls state transitions.
package harness

import (
	"errors"
	"strings"
)

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskReady     TaskStatus = "ready"
	TaskRunning   TaskStatus = "running"
	TaskReview    TaskStatus = "review"
	TaskVerify    TaskStatus = "verify"
	TaskApproval  TaskStatus = "approval"
	TaskCompleted TaskStatus = "completed"
	TaskRejected  TaskStatus = "rejected"
)

type Spec struct {
	ID         string   `json:"id"`
	Goal       string   `json:"goal"`
	Invariants []string `json:"invariants,omitempty"`
}

type Task struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Dependencies []string   `json:"dependencies,omitempty"`
	Status       TaskStatus `json:"status"`
}

type Plan struct {
	SpecID string `json:"spec_id"`
	Tasks  []Task `json:"tasks"`
}

type Result struct {
	TaskID   string `json:"task_id"`
	Producer string `json:"producer"`
	Artifact string `json:"artifact"`
	Digest   string `json:"digest"`
}

type Verdict struct {
	TaskID   string `json:"task_id"`
	Reviewer string `json:"reviewer"`
	Pass     bool   `json:"pass"`
	Reason   string `json:"reason,omitempty"`
}

func (s Spec) Validate() error {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Goal) == "" {
		return errors.New("spec requires id and goal")
	}
	for _, invariant := range s.Invariants {
		if strings.TrimSpace(invariant) == "" {
			return errors.New("spec invariant cannot be empty")
		}
	}
	return nil
}

func (p Plan) Validate(spec Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if p.SpecID != spec.ID || len(p.Tasks) == 0 || len(p.Tasks) > 256 {
		return errors.New("plan must reference spec and contain 1..256 tasks")
	}
	seen := make(map[string]bool, len(p.Tasks))
	for _, task := range p.Tasks {
		if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.Title) == "" ||
			strings.TrimSpace(task.Description) == "" || seen[task.ID] {
			return errors.New("task requires unique id, title and description")
		}
		if task.Status != TaskPending && task.Status != TaskReady {
			return errors.New("new plan tasks must be pending or ready")
		}
		seen[task.ID] = true
	}
	for _, task := range p.Tasks {
		for _, dependency := range task.Dependencies {
			if dependency == task.ID || !seen[dependency] {
				return errors.New("task dependency is invalid")
			}
		}
	}
	return nil
}
