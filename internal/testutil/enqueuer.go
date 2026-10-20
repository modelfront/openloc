package testutil

import (
	"sync"

	"openloc/internal/pipeline"
)

// RecordingEnqueuer is a TaskEnqueuer + workflow.AIStepEnqueuer stub that
// records every enqueue instead of touching Redis. Pass it to
// NewTestRouterWithEnqueuer to assert what a handler tried to schedule.
type RecordingEnqueuer struct {
	mu        sync.Mutex
	Pipelines []PipelineCall
	Analyses  []int64
	AISteps   []AIStepCall
	// Err, when set, fails every pipeline and AI-step enqueue without
	// recording it, the way a Redis outage would.
	Err error
}

// PipelineCall is one recorded EnqueuePipeline invocation.
type PipelineCall struct {
	FileID      int64
	SourceLang  string
	TargetLang  string
	JobID       int64
	TenantID    int64
	Phases      pipeline.TranslatePhases
	TriggeredBy *int64
}

// AIStepCall is one recorded EnqueueAIStep invocation.
type AIStepCall struct {
	JobID       int64
	TenantID    int64
	TriggeredBy *int64
}

func (e *RecordingEnqueuer) EnqueuePipeline(fileID int64, sourceLang, targetLang string, jobID, tenantID int64, phases pipeline.TranslatePhases, triggeredBy *int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return e.Err
	}
	e.Pipelines = append(e.Pipelines, PipelineCall{fileID, sourceLang, targetLang, jobID, tenantID, phases, triggeredBy})
	return nil
}

func (e *RecordingEnqueuer) EnqueueAnalysis(analysisID, _ int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Analyses = append(e.Analyses, analysisID)
	return nil
}

func (e *RecordingEnqueuer) EnqueueAIStep(jobID, tenantID int64, triggeredBy *int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Err != nil {
		return e.Err
	}
	e.AISteps = append(e.AISteps, AIStepCall{jobID, tenantID, triggeredBy})
	return nil
}
