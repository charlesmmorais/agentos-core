package harness

import "errors"

var transitions = map[TaskStatus]map[TaskStatus]bool{
	TaskPending:  {TaskReady: true, TaskRejected: true},
	TaskReady:    {TaskRunning: true, TaskRejected: true},
	TaskRunning:  {TaskReview: true, TaskRejected: true},
	TaskReview:   {TaskRunning: true, TaskVerify: true, TaskRejected: true},
	TaskVerify:   {TaskRunning: true, TaskApproval: true, TaskRejected: true},
	TaskApproval: {TaskCompleted: true, TaskRejected: true},
}

func Transition(from, to TaskStatus) error {
	if transitions[from][to] {
		return nil
	}
	return errors.New("invalid harness task transition")
}

func Ready(task Task, tasks []Task) bool {
	if task.Status != TaskPending {
		return false
	}
	done := make(map[string]bool, len(tasks))
	for _, candidate := range tasks {
		if candidate.Status == TaskCompleted {
			done[candidate.ID] = true
		}
	}
	for _, dependency := range task.Dependencies {
		if !done[dependency] {
			return false
		}
	}
	return true
}
