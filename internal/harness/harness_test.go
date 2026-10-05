package harness

import "testing"

func TestPlanValidationAndDependencies(t *testing.T) {
	spec := Spec{ID: "spec-1", Goal: "Build deterministic harness", Invariants: []string{"kernel owns authority"}}
	plan := Plan{SpecID: "spec-1", Tasks: []Task{
		{ID: "T1", Title: "Types", Description: "Define contracts", Status: TaskReady},
		{ID: "T2", Title: "Engine", Description: "Implement engine", Dependencies: []string{"T1"}, Status: TaskPending},
	}}
	if err := plan.Validate(spec); err != nil {
		t.Fatal(err)
	}
	if Ready(plan.Tasks[1], plan.Tasks) {
		t.Fatal("dependent task became ready too early")
	}
	plan.Tasks[0].Status = TaskCompleted
	if !Ready(plan.Tasks[1], plan.Tasks) {
		t.Fatal("dependent task did not become ready")
	}
}

func TestTransitions(t *testing.T) {
	valid := []TaskStatus{TaskReady, TaskRunning, TaskReview, TaskVerify, TaskApproval, TaskCompleted}
	from := TaskPending
	for _, to := range valid {
		if err := Transition(from, to); err != nil {
			t.Fatalf("%s -> %s: %v", from, to, err)
		}
		from = to
	}
	if Transition(TaskCompleted, TaskRunning) == nil {
		t.Fatal("terminal task reopened")
	}
}

func TestGateRequiresReviewVerificationAndApproval(t *testing.T) {
	task := Task{ID: "T1", Title: "Harness", Description: "Implement", Status: TaskApproval}
	result := &Result{TaskID: "T1", Producer: "builder", Artifact: "patch", Digest: "sha256:test"}
	reviews := []Verdict{{TaskID: "T1", Reviewer: "reviewer", Pass: true}}
	if err := EvaluateGate(GateInput{Task: task, Result: result, Reviews: reviews, Verified: true, Approved: true}); err != nil {
		t.Fatal(err)
	}
	if EvaluateGate(GateInput{Task: task, Result: result, Reviews: reviews, Verified: true}) == nil {
		t.Fatal("gate passed without explicit approval")
	}
}
