package worker

import (
	"encoding/json"
	"time"

	"github.com/hibiken/asynq"

	"openloc/internal/pipeline"
)

// Task type constants for pipeline operations.
const (
	TaskTranslate = "pipeline:translate"
	// TaskAIStep runs QE+APE for a job's active AI workflow step — enqueued
	// when such a step becomes active.
	TaskAIStep   = "pipeline:ai-step"
	TaskPipeline = "pipeline:full"
	// Saved analyses. One task per
	// analysis row; the worker computes per-(job, target_language)
	// breakdowns from stored translations.tm_match_percent + status.
	TaskAnalysis = "analysis:run"
)

// PipelinePayload carries the data needed to run a pipeline step. The
// worker passes Phases through to TranslateFile verbatim; the zero value
// results in a logged no-op at the pipeline layer
// (see TranslateFile's early-return).
//
// TriggeredBy is the user who initiated the work (uploader on
// upload-auto, the clicker on manual Pre-translate). The worker stamps it
// on history rows via pipeline.WithTriggeredBy(ctx, ...) so
// translation_history.created_by gets populated on TM/MT/APE events.
// `nil` means system-initiated — no user attribution.
//
// EnqueuedAt stamps when the task was built, so the handler can charge the
// enqueue → pickup gap to job_timings.queue_wait_ms. asynq
// doesn't surface an enqueue time on the handler context, so it rides in the
// payload. Zero on tasks enqueued before that ADR shipped — handlers must check
// IsZero rather than record a queue wait measured from the epoch. It is also
// the run's identity for job_run_outcomes: asynq retries the same payload, so
// it must stay the same across attempts and be new for each enqueue.
type PipelinePayload struct {
	FileID         int64                    `json:"file_id"`
	SourceLanguage string                   `json:"source_language"`
	TargetLanguage string                   `json:"target_language"`
	TenantID       int64                    `json:"tenant_id"`
	JobID          int64                    `json:"job_id"`
	Phases         pipeline.TranslatePhases `json:"phases"`
	TriggeredBy    *int64                   `json:"triggered_by,omitempty"`
	EnqueuedAt     time.Time                `json:"enqueued_at,omitempty"`
}

// NewPipelineTask creates an asynq task for a pipeline operation.
//
// Stamps EnqueuedAt here rather than at each call site so no enqueue path can
// forget it; a caller that already set one (tests pinning a known wait) keeps
// its value.
func NewPipelineTask(taskType string, payload PipelinePayload) (*asynq.Task, error) {
	if payload.EnqueuedAt.IsZero() {
		payload.EnqueuedAt = time.Now()
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(taskType, data), nil
}

// AnalysisPayload carries the data needed to run one saved analysis. The
// analysis row already exists in the DB (created by the POST handler with
// status=pending); the worker loads it tenant-scoped, computes breakdowns,
// writes job_analysis rows, and transitions the row to completed/failed.
type AnalysisPayload struct {
	AnalysisID int64 `json:"analysis_id"`
	TenantID   int64 `json:"tenant_id"`
}

// NewAnalysisTask wraps the payload in an asynq task with type TaskAnalysis.
func NewAnalysisTask(payload AnalysisPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TaskAnalysis, data), nil
}
