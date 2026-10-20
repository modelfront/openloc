package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
)

// stubPipeline records calls and returns the configured errors.
type stubPipeline struct {
	translateCalled bool
	aiStepCalled    bool
	lastPhases      pipeline.TranslatePhases
	translateErr    error  // when set, TranslateFile returns it
	aiStepErr       error  // when set, RunAIStep returns it
	onTranslate     func() // runs inside TranslateFile — a mid-run event (e.g. a user stop)
	onAIStep        func() // same, inside RunAIStep
}

func (s *stubPipeline) TranslateFile(ctx context.Context, tenantID, fileID int64, src, tgt string, onProgress pipeline.ProgressFunc, phases pipeline.TranslatePhases) (pipeline.TranslateOutcome, error) {
	s.translateCalled = true
	s.lastPhases = phases
	if s.onTranslate != nil {
		s.onTranslate()
	}
	return pipeline.TranslateOutcome{}, s.translateErr
}

func (s *stubPipeline) RunAIStep(ctx context.Context, tenantID, jobID int64, _ pipeline.ProgressFunc) (pipeline.AIStepResult, error) {
	s.aiStepCalled = true
	if s.onAIStep != nil {
		s.onAIStep()
	}
	return pipeline.AIStepResult{}, s.aiStepErr
}

// stubAIEnqueuer records EnqueueAIStep calls so tests can assert the
// predict-on-start chain fired (or didn't) after a pipeline run.
type stubAIEnqueuer struct {
	calls []aiStepCall
	err   error
}

type aiStepCall struct {
	jobID       int64
	tenantID    int64
	triggeredBy *int64
}

func (s *stubAIEnqueuer) EnqueueAIStep(jobID, tenantID int64, triggeredBy *int64) error {
	s.calls = append(s.calls, aiStepCall{jobID, tenantID, triggeredBy})
	return s.err
}

// stubAnalysisEnqueuer records EnqueueAnalysis calls so tests can assert the
// coalesced auto-analysis was promoted.
type stubAnalysisEnqueuer struct{ calls []int64 }

func (s *stubAnalysisEnqueuer) EnqueueAnalysis(analysisID, _ int64) error {
	s.calls = append(s.calls, analysisID)
	return nil
}

func makeTask(taskType string, payload PipelinePayload) *asynq.Task {
	data, _ := json.Marshal(payload)
	return asynq.NewTask(taskType, data)
}

// A permanently-failed pipeline (asynq's final delivery, or a
// pipeline.Permanent error on any attempt) must promote the project's pending
// auto analysis, so it isn't stranded in 'pending' forever. Any other
// non-final failure must NOT promote — asynq will retry and the normal success
// path handles it.
//
// Anti-tautology RED proof: remove the `h.promoteAfterFailure(...)` call from
// HandlePipeline's failure branch → the final-attempt EnqueueAnalysis assertion
// flips from 1 to 0.
func TestHandlePipeline_PromotesAutoAnalysisOnFinalFailure(t *testing.T) {
	orig := isLastAttempt
	defer func() { isLastAttempt = orig }()

	newHandler := func(runErr error) (*Handler, *stubAnalysisEnqueuer) {
		sp := &stubPipeline{translateErr: runErr}
		mq := &workerMockQuerier{
			autoFileProjectID:     55,
			autoPendingAnalysisID: 999, // a pending auto analysis exists for the project
			autoSettingsProfileID: 3,
			// tm/mt-on-upload both false → the readiness gate trivially passes,
			// so the (failed) job doesn't block promotion.
			autoProjectJobs: []db.ListProjectJobsForAutoAnalysisRow{{ID: 1, Status: "failed"}},
		}
		anEnq := &stubAnalysisEnqueuer{}
		return NewHandler(sp, mq).WithAnalysisEnqueuer(anEnq), anEnq
	}
	task := makeTask(TaskPipeline, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		JobID: 42, TenantID: 7, Phases: pipeline.TranslatePhases{UseMT: true},
	})

	// Final attempt → promote fires despite the failure.
	isLastAttempt = func(context.Context) bool { return true }
	h, anEnq := newHandler(fmt.Errorf("mt boom"))
	if err := h.HandlePipeline(context.Background(), task); err == nil {
		t.Fatal("expected the translate error to propagate")
	}
	if len(anEnq.calls) != 1 || anEnq.calls[0] != 999 {
		t.Fatalf("final-failure must promote the pending auto analysis (999), got %v", anEnq.calls)
	}

	// Non-final attempt → let asynq retry; do NOT promote yet.
	isLastAttempt = func(context.Context) bool { return false }
	h, anEnq = newHandler(fmt.Errorf("mt boom"))
	_ = h.HandlePipeline(context.Background(), task)
	if len(anEnq.calls) != 0 {
		t.Fatalf("a non-final failure must not promote (asynq will retry), got %v", anEnq.calls)
	}

	// A permanent failure is final on any attempt: asynq will not retry it.
	h, anEnq = newHandler(pipeline.Permanent(fmt.Errorf("pair refused")))
	_ = h.HandlePipeline(context.Background(), task)
	if len(anEnq.calls) != 1 || anEnq.calls[0] != 999 {
		t.Fatalf("a permanent failure must promote the pending auto analysis (999) on a non-final attempt, got %v", anEnq.calls)
	}
}

func TestHandleTranslate_RejectsMissingTenantID(t *testing.T) {
	sp := &stubPipeline{}
	h := NewHandler(sp, nil) // nil querier — updateStep handles nil gracefully

	task := makeTask(TaskTranslate, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de", JobID: 0,
		TenantID: 0, // missing!
	})

	err := h.HandleTranslate(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing tenant_id, got nil")
	}
	if sp.translateCalled {
		t.Error("pipeline.TranslateFile should NOT have been called")
	}
}

func TestHandleAIStep_RejectsMissingTenantID(t *testing.T) {
	sp := &stubPipeline{}
	h := NewHandler(sp, nil)

	task := makeTask(TaskAIStep, PipelinePayload{
		JobID:    1,
		TenantID: 0,
	})

	err := h.HandleAIStep(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing tenant_id, got nil")
	}
	if sp.aiStepCalled {
		t.Error("pipeline.RunAIStep should NOT have been called")
	}
}

func TestHandlePipeline_RejectsMissingTenantID(t *testing.T) {
	sp := &stubPipeline{}
	h := NewHandler(sp, nil)

	task := makeTask(TaskPipeline, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		TenantID: 0,
	})

	err := h.HandlePipeline(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing tenant_id, got nil")
	}
	if sp.translateCalled {
		t.Error("pipeline.TranslateFile should NOT have been called")
	}
}

func TestHandleTranslate_AcceptsValidTenantID(t *testing.T) {
	sp := &stubPipeline{}
	h := NewHandler(sp, nil)

	task := makeTask(TaskTranslate, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		TenantID: 42, // valid
	})

	err := h.HandleTranslate(context.Background(), task)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if !sp.translateCalled {
		t.Error("pipeline.TranslateFile should have been called")
	}
}

// --- failJob uses fresh context (not the expired task context) ---

func TestFailJob_UsesFreshContext(t *testing.T) {
	sp := &stubPipeline{}
	mq := &workerMockQuerier{}
	h := NewHandler(sp, mq)

	// Create an already-cancelled context (simulates expired asynq task ctx)
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel

	h.failJob(cancelledCtx, 99, fmt.Errorf("translate timed out"))

	if len(mq.errorCalls) != 1 {
		t.Fatalf("expected 1 UpdateJobError call, got %d", len(mq.errorCalls))
	}
	if mq.errorCalls[0].ID != 99 {
		t.Errorf("expected job ID 99, got %d", mq.errorCalls[0].ID)
	}
	if mq.errorCalls[0].ErrorMessage == nil || *mq.errorCalls[0].ErrorMessage != "translate timed out" {
		t.Errorf("expected error message 'translate timed out', got %v", mq.errorCalls[0].ErrorMessage)
	}

	// The critical assertion: context should NOT be cancelled at the time of the DB call
	if mq.errorCtxErrs[0] != nil {
		t.Errorf("failJob should use a fresh context for DB calls, but context was cancelled at call time: %v", mq.errorCtxErrs[0])
	}
}

func TestUpdateStep_UsesFreshContext(t *testing.T) {
	sp := &stubPipeline{}
	mq := &workerMockQuerier{}
	h := NewHandler(sp, mq)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	h.updateStep(cancelledCtx, 99, "completed", "Translation complete")

	if len(mq.statusCalls) != 1 {
		t.Fatalf("expected 1 UpdateJobStatus call, got %d", len(mq.statusCalls))
	}

	if mq.statusCtxErrs[0] != nil {
		t.Errorf("updateStep should use a fresh context for DB calls, but context was cancelled at call time: %v", mq.statusCtxErrs[0])
	}
}

// --- Cooperative cancellation: progress callback aborts when job deleted ---

func TestMakeProgressFunc_CancelsWhenJobDeleted(t *testing.T) {
	sp := &stubPipeline{}
	mq := &workerMockQuerier{
		deletedJobIDs: map[int64]struct{}{42: {}},
	}
	h := NewHandler(sp, mq)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	onProgress := h.makeProgressFunc(42, cancel)
	if onProgress == nil {
		t.Fatal("expected non-nil progress func")
	}

	// Call progress — job 42 is "deleted", should cancel context
	onProgress("mt", 50, 100)

	// Context should be cancelled
	if ctx.Err() == nil {
		t.Error("expected context to be cancelled when job is deleted, but it wasn't")
	}

	// Progress should NOT be updated (callback returns early on delete)
	if len(mq.progressCalls) != 0 {
		t.Errorf("expected 0 progress calls (job deleted), got %d", len(mq.progressCalls))
	}
}

func TestMakeProgressFunc_CancelsOnFirstCallWhenDeletedBeforeStart(t *testing.T) {
	// Job deleted before pipeline even starts — first progress callback should cancel immediately
	sp := &stubPipeline{}
	mq := &workerMockQuerier{
		deletedJobIDs: map[int64]struct{}{99: {}},
	}
	h := NewHandler(sp, mq)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	onProgress := h.makeProgressFunc(99, cancel)
	onProgress("mt", 0, 100) // first callback, 0 translated

	if ctx.Err() == nil {
		t.Error("expected context cancelled on first callback when job is deleted before start")
	}
	if len(mq.progressCalls) != 0 {
		t.Errorf("expected 0 progress DB writes, got %d", len(mq.progressCalls))
	}
}

func TestMakeProgressFunc_ContinuesWhenJobIsLive(t *testing.T) {
	sp := &stubPipeline{}
	mq := &workerMockQuerier{} // no deleted jobs
	h := NewHandler(sp, mq)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	onProgress := h.makeProgressFunc(42, cancel)
	onProgress("mt", 50, 100)

	// Context should NOT be cancelled
	if ctx.Err() != nil {
		t.Error("context should not be cancelled when job exists")
	}

	// Progress should be updated
	if len(mq.progressCalls) != 1 {
		t.Fatalf("expected 1 progress call, got %d", len(mq.progressCalls))
	}
	if *mq.progressCalls[0].TranslatedCount != 50 {
		t.Errorf("expected translated_count=50, got %d", *mq.progressCalls[0].TranslatedCount)
	}
}

func TestHandlePipeline_AcceptsValidTenantID(t *testing.T) {
	sp := &stubPipeline{}
	h := NewHandler(sp, nil)

	task := makeTask(TaskPipeline, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		TenantID: 42,
	})

	err := h.HandlePipeline(context.Background(), task)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if !sp.translateCalled {
		t.Error("pipeline.TranslateFile should have been called")
	}
}

// Predict-on-start runs AFTER pre-translation, never in parallel with
// it, so it scores written output. When the job's active step carries a QE+APE
// engine, finishing the pipeline enqueues exactly one TaskAIStep for that job,
// attributed to the same user.
//
// Anti-tautology RED proof: remove the `if h.aiEnqueuer != nil &&
// h.jobHasActiveAIStep(...)` chain block from HandlePipeline — EnqueueAIStep is
// never called and the len(calls)==1 assertion flips to 0.
func TestHandlePipeline_ChainsAIStepWhenActiveStepIsAI(t *testing.T) {
	sp := &stubPipeline{}
	engineID := int64(77)
	mq := &workerMockQuerier{
		activeStepWorkflowStepID: 900,
		activeStepAIRouterID:     &engineID, // active step has an AI router
	}
	enq := &stubAIEnqueuer{}
	h := NewHandler(sp, mq).WithAIStepEnqueuer(enq)

	uploader := int64(5)
	task := makeTask(TaskPipeline, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		JobID: 42, TenantID: 7, Phases: pipeline.TranslatePhases{UseMT: true},
		TriggeredBy: &uploader,
	})

	if err := h.HandlePipeline(context.Background(), task); err != nil {
		t.Fatalf("HandlePipeline: %v", err)
	}
	if !sp.translateCalled {
		t.Fatal("TranslateFile should have run before chaining the AI step")
	}
	if len(enq.calls) != 1 {
		t.Fatalf("expected exactly 1 chained EnqueueAIStep, got %d", len(enq.calls))
	}
	got := enq.calls[0]
	if got.jobID != 42 || got.tenantID != 7 {
		t.Errorf("chained AI step targeted wrong job/tenant: %+v", got)
	}
	if got.triggeredBy == nil || *got.triggeredBy != uploader {
		t.Errorf("chained AI step must carry the uploader attribution, got %v", got.triggeredBy)
	}
}

// A plain (non-AI) active step must NOT chain a predict run — otherwise every
// pre-translation would enqueue a no-op TaskAIStep and briefly flash a
// misleading "Running QE+APE" status.
//
// Anti-tautology RED proof: drop the `ws.AiRouterID != nil` check in
// jobHasActiveAIStep (return true unconditionally) — this fires an unwanted
// enqueue and the len(calls)==0 assertion flips.
func TestHandlePipeline_NoChainWhenPlainStep(t *testing.T) {
	sp := &stubPipeline{}
	mq := &workerMockQuerier{
		activeStepWorkflowStepID: 900,
		activeStepAIRouterID:     nil, // plain human step
	}
	enq := &stubAIEnqueuer{}
	h := NewHandler(sp, mq).WithAIStepEnqueuer(enq)

	task := makeTask(TaskPipeline, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		JobID: 42, TenantID: 7, Phases: pipeline.TranslatePhases{UseMT: true},
	})

	if err := h.HandlePipeline(context.Background(), task); err != nil {
		t.Fatalf("HandlePipeline: %v", err)
	}
	if len(enq.calls) != 0 {
		t.Fatalf("plain step must not chain a predict run, got %d EnqueueAIStep calls", len(enq.calls))
	}
}

// A failed pipeline must NOT chain predict — there's no MT/TM output to score,
// and the job is already marked errored.
//
// Anti-tautology RED proof: move the chain block above the TranslateFile error
// return in HandlePipeline — the chain fires despite the failure and the
// len(calls)==0 assertion flips.
func TestHandlePipeline_NoChainOnTranslateError(t *testing.T) {
	sp := &stubPipeline{translateErr: fmt.Errorf("mt boom")}
	engineID := int64(77)
	mq := &workerMockQuerier{
		activeStepWorkflowStepID: 900,
		activeStepAIRouterID:     &engineID,
	}
	enq := &stubAIEnqueuer{}
	h := NewHandler(sp, mq).WithAIStepEnqueuer(enq)

	task := makeTask(TaskPipeline, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		JobID: 42, TenantID: 7, Phases: pipeline.TranslatePhases{UseMT: true},
	})

	if err := h.HandlePipeline(context.Background(), task); err == nil {
		t.Fatal("expected HandlePipeline to return the translate error")
	}
	if len(enq.calls) != 0 {
		t.Fatalf("a failed pipeline must not chain predict, got %d EnqueueAIStep calls", len(enq.calls))
	}
}

// Phases on the payload must be passed through to TranslateFile verbatim.
// Pins the contract that the worker is a thin transport — it doesn't
// second-guess what the enqueuer asked for, in either direction:
//
//   - A partial phases payload (MT only) must stay MT only, not get
//     "helpfully" upgraded to FullPipeline.
//   - A zero-value Phases payload must stay zero — the pipeline's
//     TranslateFile early-return logs the no-op (see internal/pipeline/
//     translate.go), which is the right place for the warning. A worker-
//     side auto-promote to FullPipeline would convert "deliberate no-op" into "ran the full pipeline",
//     which is wrong if a future caller ever needs to enqueue an
//     intentional empty payload.
//
// Anti-tautology RED proofs:
//   - Hardcode `p.Phases = pipeline.FullPipeline` at the top of
//     HandlePipeline → the partial-phases assertion fails (`lastPhases`
//     becomes {UseTM: true, UseMT: true} instead of {UseMT: true}).
//   - Reintroduce a `if p.Phases == (pipeline.TranslatePhases{}) { p.Phases
//     = pipeline.FullPipeline }` block → the zero-phases assertion fails.
func TestHandlePipeline_PassesPhasesThrough(t *testing.T) {
	cases := []struct {
		name   string
		phases pipeline.TranslatePhases
	}{
		{"MT only", pipeline.TranslatePhases{UseMT: true}},
		{"TM only", pipeline.TranslatePhases{UseTM: true}},
		{"full pipeline", pipeline.FullPipeline},
		{"zero (no-op)", pipeline.TranslatePhases{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sp := &stubPipeline{}
			h := NewHandler(sp, nil)
			task := makeTask(TaskPipeline, PipelinePayload{
				FileID: 1, SourceLanguage: "en", TargetLanguage: "de", TenantID: 42,
				Phases: c.phases,
			})
			if err := h.HandlePipeline(context.Background(), task); err != nil {
				t.Fatalf("HandlePipeline: %v", err)
			}
			if !reflect.DeepEqual(sp.lastPhases, c.phases) {
				t.Errorf("phases must be preserved, want %+v, got %+v", c.phases, sp.lastPhases)
			}
		})
	}
}

// --- Cooperative stop (POST /jobs/{id}/stop) ---

// stopAbortErr is what TranslateFile returns when the progress writer cancelled
// the run context at a chunk boundary.
func stopAbortErr() error {
	return fmt.Errorf("mt translate aborted (translated 30/100): %w", context.Canceled)
}

// The progress writer must persist this chunk's counts BEFORE cancelling on a
// stop request, so the kept work is reported in full.
func TestMakeProgressFunc_WritesProgressThenCancelsOnStopRequest(t *testing.T) {
	mq := &workerMockQuerier{stopRequestedJobIDs: map[int64]struct{}{42: {}}}
	h := NewHandler(&stubPipeline{}, mq)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.makeProgressFunc(42, cancel)("mt", 60, 100)

	if ctx.Err() == nil {
		t.Fatal("expected the run context to be cancelled when a stop is requested")
	}
	if len(mq.progressCalls) != 1 || *mq.progressCalls[0].TranslatedCount != 60 {
		t.Fatalf("expected the chunk's progress (60) written before cancelling, got %+v", mq.progressCalls)
	}
}

// A user stop ends the task as 'stopped' and returns nil — a non-nil return
// would make asynq retry the run the user just stopped.
func TestHandlePipeline_StopRequested_MarksStoppedAndReturnsNil(t *testing.T) {
	engineID := int64(77)
	mq := &workerMockQuerier{activeStepWorkflowStepID: 900, activeStepAIRouterID: &engineID}
	sp := &stubPipeline{translateErr: stopAbortErr()}
	// The stop lands while the run is in flight.
	sp.onTranslate = func() {
		mq.mu.Lock()
		defer mq.mu.Unlock()
		mq.stopRequestedJobIDs = map[int64]struct{}{42: {}}
	}
	enq := &stubAIEnqueuer{}
	h := NewHandler(sp, mq).WithAIStepEnqueuer(enq)

	task := makeTask(TaskPipeline, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		JobID: 42, TenantID: 7, Phases: pipeline.TranslatePhases{UseMT: true},
	})
	if err := h.HandlePipeline(context.Background(), task); err != nil {
		t.Fatalf("a user stop must not surface as a task failure (asynq would retry it): %v", err)
	}
	if len(mq.startRunCalls) != 1 {
		t.Errorf("expected the in_progress start write, got %d", len(mq.startRunCalls))
	}
	if len(mq.stoppedCalls) != 1 {
		t.Fatalf("expected MarkJobStopped once, got %d", len(mq.stoppedCalls))
	}
	label := mq.stoppedCalls[0].CurrentStep
	if !strings.HasPrefix(label, "Stopped") || strings.HasSuffix(strings.ToLower(label), "complete") {
		t.Errorf("stop label = %q; must start with Stopped and must not read as a finished phase", label)
	}
	if len(mq.errorCalls) != 0 {
		t.Errorf("a stopped run must not be marked failed, got %+v", mq.errorCalls)
	}
	if len(enq.calls) != 0 {
		t.Errorf("a stopped pipeline must not chain the AI step, got %d calls", len(enq.calls))
	}
}

// A cancelled run WITHOUT a stop request (drain, deleted job) keeps today's
// failure path.
func TestHandlePipeline_CanceledWithoutStopRequest_StillFails(t *testing.T) {
	mq := &workerMockQuerier{}
	sp := &stubPipeline{translateErr: stopAbortErr()}
	h := NewHandler(sp, mq)

	task := makeTask(TaskPipeline, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de", JobID: 42, TenantID: 7,
		Phases: pipeline.TranslatePhases{UseMT: true},
	})
	if err := h.HandlePipeline(context.Background(), task); err == nil {
		t.Fatal("a plain cancellation must still be reported as a failure")
	}
	if len(mq.errorCalls) != 1 {
		t.Errorf("expected the job marked failed once, got %d", len(mq.errorCalls))
	}
	if len(mq.stoppedCalls) != 0 {
		t.Errorf("no stop was requested — MarkJobStopped must not run, got %+v", mq.stoppedCalls)
	}
}

// A stop requested while the task waited in the queue skips the run entirely.
func TestHandlePipeline_StoppedWhileQueued_SkipsRun(t *testing.T) {
	mq := &workerMockQuerier{stopRequestedJobIDs: map[int64]struct{}{42: {}}}
	sp := &stubPipeline{}
	h := NewHandler(sp, mq)

	task := makeTask(TaskPipeline, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de", JobID: 42, TenantID: 7,
		Phases: pipeline.TranslatePhases{UseMT: true},
	})
	if err := h.HandlePipeline(context.Background(), task); err != nil {
		t.Fatalf("expected nil (no retry), got %v", err)
	}
	if sp.translateCalled {
		t.Error("TranslateFile must not run for a job stopped while queued")
	}
	if len(mq.stoppedCalls) != 1 {
		t.Fatalf("expected MarkJobStopped once, got %d", len(mq.stoppedCalls))
	}
	if len(mq.statusCalls) != 0 {
		t.Errorf("no status transition may be written for a skipped run, got %+v", mq.statusCalls)
	}
}

func TestHandleAIStep_StopRequested_MarksStopped(t *testing.T) {
	mq := &workerMockQuerier{}
	sp := &stubPipeline{aiStepErr: fmt.Errorf("qe/ape aborted (reviewed 30/100): %w", context.Canceled)}
	sp.onAIStep = func() {
		mq.mu.Lock()
		defer mq.mu.Unlock()
		mq.stopRequestedJobIDs = map[int64]struct{}{42: {}}
	}
	h := NewHandler(sp, mq)

	task := makeTask(TaskAIStep, PipelinePayload{JobID: 42, TenantID: 7})
	if err := h.HandleAIStep(context.Background(), task); err != nil {
		t.Fatalf("a user stop must not surface as a task failure: %v", err)
	}
	if len(mq.stoppedCalls) != 1 || !strings.Contains(mq.stoppedCalls[0].CurrentStep, "Re-run AI step") {
		t.Fatalf("expected a QE+APE stop label pointing at Re-run AI step, got %+v", mq.stoppedCalls)
	}
	if len(mq.errorCalls) != 0 {
		t.Errorf("a stopped QE+APE run must not be marked failed, got %+v", mq.errorCalls)
	}
}

// The QE+APE progress label is load-bearing, not cosmetic: the editor lock
// matches the literal "QE+APE" inside current_step (web/src/lib/project-logic.ts
// isAIStepRunning). Asserting the phase constant against itself would pin
// nothing, so this pins the constant's VALUE and the exact string the writer
// persists — a rename or a format change has to come here and to the frontend
// matcher together.
func TestMakeProgressFunc_PersistsQEAPELabelTheEditorLockMatches(t *testing.T) {
	if pipeline.PhaseQEAPE != "QE+APE" {
		t.Fatalf("PhaseQEAPE = %q, want %q — the frontend editor lock matches this literal", pipeline.PhaseQEAPE, "QE+APE")
	}

	mq := &workerMockQuerier{}
	h := NewHandler(&stubPipeline{}, mq)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	h.makeProgressFunc(42, cancel)(pipeline.PhaseQEAPE, 30, 35)

	if len(mq.progressCalls) != 1 {
		t.Fatalf("expected 1 progress write, got %d", len(mq.progressCalls))
	}
	step := mq.progressCalls[0].CurrentStep
	if step != "QE+APE: 30 / 35" {
		t.Errorf("persisted current_step = %q, want %q", step, "QE+APE: 30 / 35")
	}
	// The two properties the lock actually depends on, asserted separately from
	// the exact format so a deliberate format change still fails loudly above
	// while these keep saying WHY the format matters.
	if !strings.Contains(step, "QE+APE") {
		t.Errorf("current_step %q lacks the QE+APE substring — the editor would unlock mid-run", step)
	}
	if !pipeline.IsPipelineRunning("in_progress", step) {
		t.Errorf("current_step %q does not read as running — the job would look finished mid-run", step)
	}
}
