package worker_test

import (
	"testing"

	"github.com/hibiken/asynq"

	"openloc/internal/worker"
)

// pipelineTask is the task the server enqueues for p, enqueue time included.
func pipelineTask(t *testing.T, taskType string, p worker.PipelinePayload) *asynq.Task {
	t.Helper()
	task, err := worker.NewPipelineTask(taskType, p)
	if err != nil {
		t.Fatalf("NewPipelineTask: %v", err)
	}
	return task
}
