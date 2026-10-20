package pipeline

import (
	"context"
	"errors"
	"testing"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/enginehealth"
)

// An unusable QE+APE engine must not review a job.
//
// Switching an engine off does not unwire it: the workflow step still carries
// the AI router, and the router still routes to the engine. So the step reaches
// an engine that may not run, and the honest answer is to fail the job and name
// it — the same answer the MT path gives. Skipping instead would report the job
// as reviewed with nothing reviewed.

func aiStepJobWithSegment(mq *mockQuerier) {
	mq.addFileProject(1, 10, nil, "en", []string{"ja"})
	mq.setAIProviderConn(1, "openai", "sk-test")
	mq.addLLMAIStepJob(5, 1, 1, "ja", "qe_ape", "LLM APE", "openai", "reject")

	mt := "こんにちは"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: "ja", SourceText: "Hello", ActiveText: &mt},
	}
}

func TestRunAIStep_SwitchedOffEngineFailsTheJob(t *testing.T) {
	fp := &fakePredictor{results: map[string]ai.PredictResult{"1": {Approved: true}}}
	mq := newMockQuerier()
	aiStepJobWithSegment(mq)
	mq.setAIEngineEnabled(901, false)

	_, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil)

	var unusable *EngineUnusableError
	if !errors.As(err, &unusable) {
		t.Fatalf("a switched-off engine must fail the job, got err=%v", err)
	}
	if unusable.Reason != enginehealth.Disabled {
		t.Errorf("Reason = %q, want %q", unusable.Reason, enginehealth.Disabled)
	}
	if len(fp.gotCfgs) != 0 {
		t.Errorf("a switched-off engine may not call the provider, got %d predict batches", len(fp.gotCfgs))
	}
	if len(mq.updatePredictCalls) != 0 {
		t.Errorf("a switched-off engine may not touch a segment, got %d predict updates", len(mq.updatePredictCalls))
	}
}

// A row's token is required at save (aiengine.validateRow), so a null one means
// the key was deleted from AI settings and the FK set it null. The AI settings
// page already flags the engine; the pipeline has to agree.
func TestRunAIStep_DeletedKeyFailsTheJob(t *testing.T) {
	fp := &fakePredictor{results: map[string]ai.PredictResult{"1": {Approved: true}}}
	mq := newMockQuerier()
	aiStepJobWithSegment(mq)
	mq.setAIEngineRowToken(901, nil)

	_, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil)

	var unusable *EngineUnusableError
	if !errors.As(err, &unusable) {
		t.Fatalf("an engine whose key was deleted must fail the job, got err=%v", err)
	}
	if unusable.Reason != enginehealth.NoKey {
		t.Errorf("Reason = %q, want %q", unusable.Reason, enginehealth.NoKey)
	}
	want := `The AI step could not complete: the engine "` + unusable.EngineName + `" has no API key. Choose a key for it in AI → Engines.`
	if err.Error() != want {
		t.Errorf("message = %q\nwant %q", err.Error(), want)
	}
	if len(fp.gotCfgs) != 0 {
		t.Errorf("an engine with no key may not call the provider, got %d predict batches", len(fp.gotCfgs))
	}
	if len(mq.updatePredictCalls) != 0 {
		t.Errorf("an engine with no key may not touch a segment, got %d predict updates", len(mq.updatePredictCalls))
	}
}

// The switch guards the extension backend too. That row has no system
// prompt, so it never reaches the LLM runner — without a check on the engine
// itself it would run the extension behind its key.
func TestRunAIStep_SwitchedOffExtensionEngineFailsTheJob(t *testing.T) {
	pred := &fakeExtPredictor{ape: true}
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.addAIStepJob(5, 1, 1, "de", "APE review")
	mq.setAIEngineEnabled(901, false)

	mt := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mt},
	}

	svc, _ := newExtStepService(mq, pred)
	_, err := svc.RunAIStep(context.Background(), 1, 5, nil)

	var unusable *EngineUnusableError
	if !errors.As(err, &unusable) {
		t.Fatalf("a switched-off extension engine must fail the job, got err=%v", err)
	}
	if len(pred.calls) != 0 {
		t.Errorf("a switched-off engine may not call the extension, got %d batches", len(pred.calls))
	}
	if len(mq.updatePredictCalls) != 0 {
		t.Errorf("a switched-off engine may not touch a segment, got %d predict updates", len(mq.updatePredictCalls))
	}
}
