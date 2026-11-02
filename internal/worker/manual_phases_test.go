package worker

import (
	"context"
	"testing"

	"openloc/internal/pipeline"
)

// A queued Apply MT (Phases.Manual) never chains a QE+APE run, as the job's
// own button never does; the same run from the upload hook does
// (TestHandlePipeline_ChainsAIStepWhenActiveStepIsAI).
func TestHandlePipeline_ManualRunChainsNoAIStep(t *testing.T) {
	sp := &stubPipeline{}
	engineID := int64(77)
	mq := &workerMockQuerier{
		activeStepWorkflowStepID: 900,
		activeStepAIRouterID:     &engineID,
	}
	enq := &stubAIEnqueuer{}
	h := NewHandler(sp, mq).WithAIStepEnqueuer(enq)

	task := makeTask(TaskPipeline, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		JobID: 42, TenantID: 7, Phases: pipeline.TranslatePhases{UseMT: true, Manual: true},
	})
	if err := h.HandlePipeline(context.Background(), task); err != nil {
		t.Fatalf("HandlePipeline: %v", err)
	}
	if !sp.translateCalled {
		t.Fatal("TranslateFile should have run")
	}
	if len(enq.calls) != 0 {
		t.Errorf("a queued Apply MT chained %d AI step(s), want none", len(enq.calls))
	}
}
