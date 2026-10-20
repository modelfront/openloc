package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"openloc/internal/pipeline"
	"openloc/internal/timing"
)

// decodeStages unpacks the JSONB payload a flush wrote.
func decodeStages(t *testing.T, raw []byte) map[string]timing.Stage {
	t.Helper()
	var stages map[string]timing.Stage
	if err := json.Unmarshal(raw, &stages); err != nil {
		t.Fatalf("decode stages %s: %v", raw, err)
	}
	return stages
}

// TestHandleTranslate_FlushesTaskTiming pins the worker half of
// task timing: a task must open a whole-task span, charge the enqueue →
// pickup gap to queue wait, and flush exactly once against the job's tenant.
//
// Anti-tautology RED proof: delete the `defer func() { finish(err) }()` line
// from HandleTranslate → len(flushes) drops to 0 and the test fails.
func TestHandleTranslate_FlushesTaskTiming(t *testing.T) {
	mq := &workerMockQuerier{}
	h := NewHandler(&stubPipeline{}, mq)

	// A task that sat in Redis for 250ms before pickup.
	task := makeTask(TaskTranslate, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		JobID: 42, TenantID: 7,
		Phases:     pipeline.TranslatePhases{UseMT: true},
		EnqueuedAt: time.Now().Add(-250 * time.Millisecond),
	})

	if err := h.HandleTranslate(context.Background(), task); err != nil {
		t.Fatalf("HandleTranslate: %v", err)
	}

	flushes := mq.timings()
	if len(flushes) != 1 {
		t.Fatalf("want exactly 1 timing flush, got %d", len(flushes))
	}
	got := flushes[0]

	if got.JobID != 42 || got.TenantID != 7 {
		t.Errorf("flushed against job=%d tenant=%d, want job=42 tenant=7", got.JobID, got.TenantID)
	}
	// 250ms measured, but the handler runs after that, so allow the real gap
	// to be slightly larger. The point is that it isn't zero and isn't epoch.
	if got.QueueWaitMs < 250 || got.QueueWaitMs > 5000 {
		t.Errorf("queue_wait_ms = %d, want ~250 (enqueue → pickup gap)", got.QueueWaitMs)
	}

	stages := decodeStages(t, got.Stages)
	span, ok := stages[timing.StageTaskTranslate]
	if !ok {
		t.Fatalf("no %s span recorded; got %v", timing.StageTaskTranslate, stages)
	}
	if span.N != 1 {
		t.Errorf("%s n = %d, want 1", timing.StageTaskTranslate, span.N)
	}
	if span.Err != 0 {
		t.Errorf("%s err = %d, want 0 on a successful task", timing.StageTaskTranslate, span.Err)
	}
}

// TestHandleTranslate_RecordsTaskFailure covers the other branch: a failing
// task must still flush, and its error must land in the span's err counter
// rather than being lost with the task.
//
// This is the case that makes the err counter worth having — a job that fails
// fast looks identical to a fast job unless the failure is counted.
func TestHandleTranslate_RecordsTaskFailure(t *testing.T) {
	mq := &workerMockQuerier{}
	h := NewHandler(&stubPipeline{translateErr: fmt.Errorf("mt boom")}, mq)

	task := makeTask(TaskTranslate, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		JobID: 42, TenantID: 7,
		Phases:     pipeline.TranslatePhases{UseMT: true},
		EnqueuedAt: time.Now(),
	})

	if err := h.HandleTranslate(context.Background(), task); err == nil {
		t.Fatal("expected the translate error to propagate")
	}

	flushes := mq.timings()
	if len(flushes) != 1 {
		t.Fatalf("a failed task must still flush its timings, got %d flushes", len(flushes))
	}
	span := decodeStages(t, flushes[0].Stages)[timing.StageTaskTranslate]
	if span.Err != 1 {
		t.Errorf("%s err = %d, want 1 — the task's error must reach the counter",
			timing.StageTaskTranslate, span.Err)
	}
}

// TestNewPipelineTask_StampsEnqueuedAt guards the queue-wait measurement at its
// source. The stamp lives in NewPipelineTask so no enqueue path can forget it;
// if it were per-call-site, a new enqueuer would silently report zero wait.
func TestNewPipelineTask_StampsEnqueuedAt(t *testing.T) {
	before := time.Now()
	task, err := NewPipelineTask(TaskTranslate, PipelinePayload{JobID: 1, TenantID: 1})
	if err != nil {
		t.Fatalf("NewPipelineTask: %v", err)
	}
	var p PipelinePayload
	if err := json.Unmarshal(task.Payload(), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.EnqueuedAt.IsZero() {
		t.Fatal("EnqueuedAt not stamped — queue wait would measure from the epoch")
	}
	if p.EnqueuedAt.Before(before.Add(-time.Second)) {
		t.Errorf("EnqueuedAt = %v, want ~now", p.EnqueuedAt)
	}

	// A caller that set one keeps it — tests pin a known wait this way.
	pinned := time.Now().Add(-time.Hour)
	task, err = NewPipelineTask(TaskTranslate, PipelinePayload{JobID: 1, TenantID: 1, EnqueuedAt: pinned})
	if err != nil {
		t.Fatalf("NewPipelineTask: %v", err)
	}
	if err := json.Unmarshal(task.Payload(), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !p.EnqueuedAt.Equal(pinned) {
		t.Errorf("EnqueuedAt = %v, want the caller's %v", p.EnqueuedAt, pinned)
	}
}
