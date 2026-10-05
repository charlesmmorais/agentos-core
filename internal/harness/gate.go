package harness

import (
	"errors"
	"strings"
)

type GateInput struct {
	Task     Task
	Result   *Result
	Reviews  []Verdict
	Verified bool
	Approved bool
}

func EvaluateGate(in GateInput) error {
	if in.Task.Status != TaskApproval {
		return errors.New("task is not awaiting approval")
	}
	if in.Result == nil || in.Result.TaskID != in.Task.ID ||
		strings.TrimSpace(in.Result.Producer) == "" ||
		strings.TrimSpace(in.Result.Artifact) == "" ||
		strings.TrimSpace(in.Result.Digest) == "" {
		return errors.New("valid task result required")
	}
	if len(in.Reviews) == 0 {
		return errors.New("at least one review required")
	}
	for _, review := range in.Reviews {
		if review.TaskID != in.Task.ID || strings.TrimSpace(review.Reviewer) == "" || !review.Pass {
			return errors.New("all reviews must pass for the same task")
		}
	}
	if !in.Verified {
		return errors.New("verification required")
	}
	if !in.Approved {
		return errors.New("explicit approval required")
	}
	return nil
}
