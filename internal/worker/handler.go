package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"openloc/internal/analysis"
	"openloc/internal/autostep"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/llmdebug"
	"openloc/internal/logging"
	"openloc/internal/notification"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/stepdef"
	"openloc/internal/timing"
	"openloc/internal/workflow"
)

// PipelineService defines the pipeline methods the worker calls. Every
// method takes tenantID up-front so the pipeline's GetFileTenant lookup
// rejects stale or cross-tenant fileIDs at the DB layer.
type PipelineService interface {
	TranslateFile(ctx context.Context, tenantID, fileID int64, sourceLang, targetLang string, onProgress pipeline.ProgressFunc, phases pipeline.TranslatePhases) (pipeline.TranslateOutcome, error)
	RunAIStep(ctx context.Context, tenantID, jobID int64, onProgress pipeline.ProgressFunc) (pipeline.AIStepResult, error)
}

// AnalysisService runs one saved analysis end-to-end. Same tenant-up-front
// pattern as PipelineService so the worker can't accidentally run an
// analysis it didn't authorize.
type AnalysisService interface {
	Run(ctx context.Context, analysisID, tenantID int64) error
}

// AIStepEnqueuer chains a QE+APE run after pre-translation
// completes (predict-on-start). Satisfied by *Enqueuer; nil when async
// chaining isn't wired (tests / no-op). See HandlePipeline for why predict must
// run AFTER the pipeline rather than in parallel with it.
type AIStepEnqueuer interface {
	EnqueueAIStep(jobID, tenantID int64, triggeredBy *int64) error
}

// Handler processes asynq pipeline tasks.
//
// Tenant scoping: every payload includes `TenantID`. Before writing
// anything, each handler checks that the payload's job belongs to that tenant
// and file (verifyPayloadJob), then threads the tenant through to the
// pipeline service, whose file lookup uses GetFileTenant — so a malformed
// payload naming another tenant's job or file is rejected instead of
// operating on the wrong row.
type Handler struct {
	pipeline         PipelineService
	analysis         AnalysisService // nil-safe: HandleAnalysis logs + drops when unset
	queries          db.Querier
	aiEnqueuer       AIStepEnqueuer                  // nil-safe: HandlePipeline skips predict-on-start chaining when unset
	analysisEnqueuer analysis.Enqueuer               // nil-safe: skips auto-analysis-on-upload when unset
	workflow         *workflow.Service               // nil-safe: an ai step then stays open for a manager to push
	failures         notification.JobFailureNotifier // nil-safe: a failed job then sends no bell notification
}

func NewHandler(p PipelineService, q db.Querier) *Handler {
	return &Handler{pipeline: p, queries: q}
}

// trackTask installs a timing Recorder on ctx, charges the enqueue → pickup gap
// to queue wait, and opens the whole-task span. The returned closure
// closes the span and flushes; every handler defers it against its named return
// so the task's own error lands in the stage's err counter:
//
//	ctx, finish := h.trackTask(ctx, p, timing.StageTaskTranslate)
//	defer func() { finish(err) }()
//
// Everything the pipeline instruments downstream accumulates into the same
// recorder via the context, so one flush covers the whole task.
func (h *Handler) trackTask(ctx context.Context, p PipelinePayload, stage string) (context.Context, func(error)) {
	ctx, rec := timing.WithRecorder(ctx)
	if !p.EnqueuedAt.IsZero() {
		rec.SetQueueWait(time.Since(p.EnqueuedAt))
	}
	// The task span is the ROOT: every stage the handler goes on to record
	// branches off the context returned here, so their time is subtracted from
	// this span and its self time is exactly the uninstrumented overhead.
	ctx, done := timing.Start(ctx, stage)
	return ctx, func(err error) {
		done(err)
		rec.Flush(ctx, h.queries, p.TenantID, p.JobID)
	}
}

// WithAIStepEnqueuer wires the predict-on-start chain: when
// a pre-translation pipeline finishes and the job's active step carries a QE+APE
// engine, HandlePipeline enqueues a follow-up TaskAIStep so QE+APE scores the
// freshly-written MT/TM output. Optional — nil skips the chaining.
func (h *Handler) WithAIStepEnqueuer(e AIStepEnqueuer) *Handler {
	h.aiEnqueuer = e
	return h
}

// WithFailureNotifier sends a bell notification when a job fails for good.
func (h *Handler) WithFailureNotifier(n notification.JobFailureNotifier) *Handler {
	h.failures = n
	return h
}

// WithWorkflow wires the step-transition service so HandleAIStep can complete
// an `ai` step once its QE+APE run has finished. Optional —
// nil leaves the step in_progress for a manager to advance by hand.
func (h *Handler) WithWorkflow(svc *workflow.Service) *Handler {
	h.workflow = svc
	return h
}

// WithAnalysisService attaches the analysis service so the worker can
// process TaskAnalysis. Production wiring (cmd/server/main.go) calls this
// after constructing the analysis.Service; tests that don't exercise
// analyses leave it nil and HandleAnalysis short-circuits with a log line.
func (h *Handler) WithAnalysisService(a AnalysisService) *Handler {
	h.analysis = a
	return h
}

// WithAnalysisEnqueuer wires auto-analyze-on-upload: when a job's
// pretranslate finishes, the terminal completion point promotes the project's
// pending coalesced auto analysis (if any) once every job is ready. Optional —
// nil skips the auto-analysis trigger entirely.
func (h *Handler) WithAnalysisEnqueuer(e analysis.Enqueuer) *Handler {
	h.analysisEnqueuer = e
	return h
}

// maybeAutoAnalyzeProject promotes the project's pending coalesced auto analysis
// at a pipeline terminal completion point. It resolves the project
// from the just-finished file, then calls the self-gating promotion helper — a
// no-op unless a pending auto analysis exists AND all the project's jobs are
// past pretranslate, so calling it on every job completion is safe.
func (h *Handler) maybeAutoAnalyzeProject(ctx context.Context, fileID, tenantID int64) {
	if h.analysisEnqueuer == nil || fileID == 0 || h.queries == nil {
		return
	}
	file, err := h.queries.GetFile(ctx, fileID)
	if err != nil {
		logging.Warn(ctx, err, "auto-analysis: project lookup failed", "worker-auto-analysis-project-failed", logging.FileID(fileID))
		return
	}
	analysis.PromoteProjectAutoAnalysisIfReady(ctx, h.queries, h.analysisEnqueuer, tenantID, file.ProjectID)
}

// isLastAttempt reports whether asynq will NOT retry this task again (final
// delivery). Extracted as a var so tests can force either branch — asynq's
// retry-count context is built inside the asynq package and can't be
// constructed from here. When the metadata is absent (a synchronous call, or a
// test's plain context) we treat it as the last attempt so a stranded pending
// analysis still gets promoted.
var isLastAttempt = func(ctx context.Context) bool {
	retried, ok1 := asynq.GetRetryCount(ctx)
	maxRetry, ok2 := asynq.GetMaxRetry(ctx)
	if !ok1 || !ok2 {
		return true
	}
	return retried >= maxRetry
}

// promoteAfterFailure handles the case where a permanently-failed job
// would otherwise strand the project's pending auto analysis in 'pending'
// forever (nothing else ever calls the promotion). failRun calls it on the
// final attempt only — so a transient failure still retries and promotes via
// the normal success path, while a permanent failure lets the analysis run
// over whatever succeeded (a failed job doesn't block the readiness gate).
// Uses a fresh context: the task context is usually cancelled or expired on
// the failure path.
func (h *Handler) promoteAfterFailure(fileID, tenantID int64) {
	if h.analysisEnqueuer == nil || fileID == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.maybeAutoAnalyzeProject(ctx, fileID, tenantID)
}

func (h *Handler) HandleTranslate(ctx context.Context, t *asynq.Task) (err error) {
	var p PipelinePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	if p.TenantID == 0 {
		return fmt.Errorf("missing tenant_id in payload")
	}
	if err := h.verifyPayloadJob(ctx, p); err != nil {
		return err
	}

	ctx, finish := h.trackTask(ctx, p, timing.StageTaskTranslate)
	defer func() { finish(err) }()

	if !h.startRun(ctx, p, pipeline.PhaseDescription(p.Phases), pipeline.PhaseNoun(p.Phases)) {
		return nil
	}

	// Propagate the user who initiated this work via context so the
	// pipeline's emission sites (applyExactMatch / applyFuzzyMatch /
	// saveMTChunk / predict APE) can stamp translation_history.created_by.
	// Nil = system-initiated, no attribution.
	ctx = pipeline.WithTriggeredBy(ctx, p.TriggeredBy)

	taskCtx, cancelTask := context.WithCancel(ctx)
	defer cancelTask()
	onProgress := h.makeProgressFunc(p.JobID, cancelTask)
	// Stamp the JOB id for the debug feed: TranslateFile only receives the file
	// and the language, and the job id is what pairs an MT call with the APE
	// call for the same upload. WithMeta merges, so the
	// FileID that runMTPhase adds later survives.
	taskCtx = llmdebug.WithMeta(taskCtx, llmdebug.Meta{JobID: p.JobID})
	prior := h.loadRunSoFar(ctx, p)
	outcome, err := h.pipeline.TranslateFile(taskCtx, p.TenantID, p.FileID, p.SourceLanguage, p.TargetLanguage, onProgress, prior.phases(p.Phases))
	if err != nil {
		return h.translateFailed(ctx, p, prior, outcome, err)
	}
	outcome = prior.then(outcome)
	h.forgetRunSoFar(p)
	// Log the phase outcome for the same reason the AI step does: a skipped MT
	// phase makes no provider call, so the debug feed correctly shows no MT
	// entry — which is indistinguishable from a lost one. `no_segments` (the TM
	// phase already covered everything) and `no_assignment` are both normal and
	// both look like a bug without this line.
	logging.Info(ctx, "pipeline phases complete", "pipeline-phases-complete",
		logging.JobID(p.JobID),
		logging.Label("tm_skip_reason", string(outcome.TMSkipReason)),
		logging.Label("mt_skip_reason", string(outcome.MTSkipReason)))

	// Phases leave the job in "in_progress"; only job Complete
	// (HTTP /jobs/:id/complete) flips status to "completed" — its
	// precondition guard 409s on a job that is already completed.
	h.updateStep(ctx, p.JobID, "in_progress", "Translation complete")
	h.emitJobPipelineEvent(ctx, p.TenantID, p.JobID, projectevents.TypeJobPipelineCompleted, p.TriggeredBy,
		pipelineCompletedDetails(p.Phases, outcome))
	return nil
}

// HandleAIStep runs QE+APE for a job's active QE+APE workflow
// step. Enqueued by the workflow activation hook when such a step
// becomes active. Benign no-ops (no active AI step / extensions not wired /
// nothing pre-translated) return a skip reason with a nil error and don't fail
// the job; a provider request failure fails the job so the UI surfaces it.
func (h *Handler) HandleAIStep(ctx context.Context, t *asynq.Task) (err error) {
	var p PipelinePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	if p.TenantID == 0 {
		return fmt.Errorf("missing tenant_id in payload")
	}
	if p.JobID == 0 {
		return fmt.Errorf("missing job_id in payload")
	}
	if err := h.verifyPayloadJob(ctx, p); err != nil {
		return err
	}

	ctx, finish := h.trackTask(ctx, p, timing.StageTaskAIStep)
	defer func() { finish(err) }()

	if !h.startRun(ctx, p, "Running "+pipeline.AIStepMarker, pipeline.PhaseQEAPE) {
		return nil
	}

	ctx = pipeline.WithTriggeredBy(ctx, p.TriggeredBy)

	// Cooperative stop at batch boundaries, same as the pipeline handlers.
	taskCtx, cancelTask := context.WithCancel(ctx)
	defer cancelTask()
	onProgress := h.makeProgressFunc(p.JobID, cancelTask)

	// The result is logged, not discarded: RunAIStep returns a SkipReason with a
	// NIL error when the active step carries no QE+APE engine, or its backend
	// isn't wired. Without the log line, a skipped auto AI step looks
	// successful with no provider call and nothing saying why.
	res, err := h.pipeline.RunAIStep(taskCtx, p.TenantID, p.JobID, onProgress)
	if err == nil {
		if res.SkipReason != "" {
			logging.Info(ctx, "ai step skipped", "ai-step-skipped",
				logging.JobID(p.JobID),
				logging.Label("skip_reason", string(res.SkipReason)))
		} else {
			logging.Info(ctx, "ai step complete", "ai-step-complete",
				logging.JobID(p.JobID),
				logging.Debugf("auto_approved", res.AutoApproved),
				logging.Debugf("needs_review", res.NeedsReview))
		}
	}
	if err != nil {
		// The AI-step payload has no file_id; the auto-analysis promotion
		// needs it, so resolve it from the job.
		fileID := h.jobFileID(p.JobID)
		if st, ok := pipeline.StopRequested(h.queries, p.JobID, err); ok {
			h.finishStopped(ctx, p, fileID, pipeline.PhaseQEAPE, st)
			return nil
		}
		return h.failRun(ctx, p, fileID, pipeline.PhaseQEAPE, err)
	}

	// See HandleTranslate — completed is reserved for /complete.
	h.updateStep(ctx, p.JobID, "in_progress", pipeline.AIStepMarker+" complete")
	// Benign skips (no AI step / backend not wired / nothing to score)
	// ran nothing — "finished QE+APE" in the history would be a lie. Outcome
	// counts ride along so the row says what the automation actually decided.
	if res.SkipReason == "" {
		h.emitJobPipelineEvent(ctx, p.TenantID, p.JobID, projectevents.TypeJobPipelineCompleted, p.TriggeredBy,
			map[string]any{"phase": "QE+APE", "auto_approved": res.AutoApproved, "needs_review": res.NeedsReview})
	}

	// An `ai` step has nobody to hand the result to: it completes itself and
	// the workflow moves on (a skip still counts as done — there was nothing to
	// score). A hybrid step stays open for its human assignee.
	h.autoCompleteAIStep(ctx, p, res)

	// Auto-analyze-on-upload: the AI step is the job's terminal phase
	// when chained after a pipeline. Promote the project's pending auto analysis.
	// The payload carries no file_id (EnqueueAIStep only sets job_id), so resolve
	// it from the job.
	if h.analysisEnqueuer != nil {
		if job, err := h.queries.GetJob(ctx, p.JobID); err == nil {
			h.maybeAutoAnalyzeProject(ctx, job.FileID, p.TenantID)
		} else {
			logging.Warn(ctx, err, "auto-analysis: job lookup failed", "worker-auto-analysis-job-failed", logging.JobID(p.JobID))
		}
	}
	return nil
}

func (h *Handler) HandlePipeline(ctx context.Context, t *asynq.Task) (err error) {
	var p PipelinePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	if p.TenantID == 0 {
		return fmt.Errorf("missing tenant_id in payload")
	}
	if err := h.verifyPayloadJob(ctx, p); err != nil {
		return err
	}

	ctx, finish := h.trackTask(ctx, p, timing.StageTaskPipeline)
	defer func() { finish(err) }()

	if !h.startRun(ctx, p, pipeline.PhaseDescription(p.Phases), pipeline.PhaseNoun(p.Phases)) {
		return nil
	}

	ctx = pipeline.WithTriggeredBy(ctx, p.TriggeredBy)

	taskCtx, cancelTask := context.WithCancel(ctx)
	defer cancelTask()
	onProgress := h.makeProgressFunc(p.JobID, cancelTask)
	// Stamp the JOB id for the debug feed: TranslateFile only receives the file
	// and the language, and the job id is what pairs an MT call with the APE
	// call for the same upload. WithMeta merges, so the
	// FileID that runMTPhase adds later survives.
	taskCtx = llmdebug.WithMeta(taskCtx, llmdebug.Meta{JobID: p.JobID})
	prior := h.loadRunSoFar(ctx, p)
	outcome, err := h.pipeline.TranslateFile(taskCtx, p.TenantID, p.FileID, p.SourceLanguage, p.TargetLanguage, onProgress, prior.phases(p.Phases))
	if err != nil {
		return h.translateFailed(ctx, p, prior, outcome, err)
	}
	outcome = prior.then(outcome)
	h.forgetRunSoFar(p)

	// See HandleTranslate — completed is reserved for /complete.
	h.updateStep(ctx, p.JobID, "in_progress", "Pipeline complete")
	if p.Phases.UseMT {
		autostep.RecordMTMissing(ctx, h.queries, p.JobID, outcome.MTMissing())
	}
	h.emitJobPipelineEvent(ctx, p.TenantID, p.JobID, projectevents.TypeJobPipelineCompleted, p.TriggeredBy,
		pipelineCompletedDetails(p.Phases, outcome))

	// Predict-on-start: if the job's active step carries a QE+APE
	// engine, chain a QE+APE run now that pre-translation has
	// written the MT/TM output — predict must score written output, so it is
	// chained after the pipeline, never run in parallel with it. Best-effort — a failed enqueue is logged, not propagated (the
	// pipeline itself succeeded; the human can still review, and the manual
	// "Re-run AI step" action is available).
	if h.aiEnqueuer != nil && h.jobHasActiveAIStep(ctx, p.JobID) {
		if err := h.aiEnqueuer.EnqueueAIStep(p.JobID, p.TenantID, p.TriggeredBy); err != nil {
			logging.Error(ctx, err, "chaining the AI step after the pipeline failed", "worker-chain-ai-step-failed", logging.JobID(p.JobID))
		}
		// The chained AI step is now this job's terminal phase — the
		// auto-analysis trigger fires at the end of HandleAIStep instead, so
		// numbers include any APE auto-approvals. Don't fire here too.
		return nil
	}

	// A Pre-translation step's work is this pipeline, so the step completes
	// itself now — unless MT was asked for and had no engine for this
	// language: then the step stays open so the job doesn't read as done.
	if outcome.MTMissing() == "" {
		h.autoCompleteStep(ctx, p, stepdef.TypePretranslate, map[string]any{"phase": pipeline.PhaseNoun(p.Phases)})
	}

	// Auto-analyze-on-upload: this pipeline was the job's terminal phase
	// (no AI step chained). Promote the project's pending auto analysis — a
	// no-op unless the project has one pending and all its jobs are now ready.
	h.maybeAutoAnalyzeProject(ctx, p.FileID, p.TenantID)
	return nil
}

// makeProgressFunc returns a ProgressFunc that updates the job's translated/total
// counts. It is also the cooperative stop switch: at every chunk boundary it
// cancels the run when the job was deleted, or when a user requested a stop —
// in the latter case after writing this chunk's progress, so the kept work is
// reported in full.
func (h *Handler) makeProgressFunc(jobID int64, cancelTask context.CancelFunc) pipeline.ProgressFunc {
	if jobID == 0 || h.queries == nil {
		return nil
	}
	return func(phase string, done, total int) {
		dbCtx, cancel := freshDBContext()
		defer cancel()

		state, stateErr := pipeline.LoadRunState(h.queries, jobID)
		if stateErr == nil && state.Deleted {
			logging.Info(dbCtx, "job deleted mid-flight, aborting run", "job-run-deleted",
				logging.JobID(jobID), logging.Label("phase", phase))
			cancelTask()
			return
		}
		if stateErr == nil && state.StopRequested {
			logging.Info(dbCtx, "stop requested, aborting run after this chunk", "job-run-stop-requested",
				logging.JobID(jobID), logging.Label("phase", phase))
			defer cancelTask()
		}

		dc := int32(done)
		tot := int32(total)
		var step string
		switch phase {
		case "tm":
			step = fmt.Sprintf("TM lookup: %d matches found (%d segments)", done, total)
		case "mt":
			step = fmt.Sprintf("MT: %d / %d segments", done, total)
		default:
			step = fmt.Sprintf("%s: %d / %d", phase, done, total)
		}

		if err := h.queries.UpdateJobProgress(dbCtx, db.UpdateJobProgressParams{
			ID:              jobID,
			TranslatedCount: &dc,
			TotalCount:      &tot,
			CurrentStep:     step,
		}); err != nil {
			logging.Warn(dbCtx, err, "job progress update failed", "worker-job-progress-failed", logging.JobID(jobID))
		}
	}
}

// jobHasActiveAIStep reports whether the job's currently-active workflow step
// carries an AI router. Used to gate predict-on-start chaining so a
// plain-step pipeline doesn't enqueue a no-op TaskAIStep (and briefly flash a
// misleading "Running QE+APE" status). Any lookup miss → false (skip chaining).
func (h *Handler) jobHasActiveAIStep(ctx context.Context, jobID int64) bool {
	if h.queries == nil {
		return false
	}
	step, err := h.queries.GetActiveStepForJob(ctx, jobID)
	if err != nil {
		return false
	}
	ws, err := h.queries.GetWorkflowStep(ctx, step.WorkflowStepID)
	if err != nil {
		return false
	}
	return ws.AiRouterID != nil
}

// autoCompleteAIStep advances the job when its active step is `ai`.
// Best-effort: a failure here leaves the step in_progress
// — the QE+APE results are already written, so the task itself is not failed
// (a retry would re-run predict for nothing); a manager can advance the step
// by hand. Unapproved rows carry forward as needs_review into the next step.
func (h *Handler) autoCompleteAIStep(ctx context.Context, p PipelinePayload, res pipeline.AIStepResult) {
	details := map[string]any{"auto_approved": res.AutoApproved, "needs_review": res.NeedsReview}
	if res.SkipReason != "" {
		details["skip_reason"] = string(res.SkipReason)
	}
	h.autoCompleteStep(ctx, p, stepdef.TypeAI, details)
}

// autoCompleteStep advances the job when its active step is an automated step
// of stepType; see autostep.Complete.
func (h *Handler) autoCompleteStep(ctx context.Context, p PipelinePayload, stepType string, details map[string]any) {
	autostep.Complete(ctx, h.queries, h.workflow, p.TenantID, p.JobID, stepType, p.TriggeredBy, details)
}

func (h *Handler) updateStep(ctx context.Context, jobID int64, status, step string) {
	if jobID == 0 || h.queries == nil {
		return
	}
	// Use fresh context — the task ctx may already be expired or about to expire.
	dbCtx, cancel := freshDBContext()
	defer cancel()
	if err := h.queries.UpdateJobStatus(dbCtx, db.UpdateJobStatusParams{
		ID:          jobID,
		Status:      status,
		CurrentStep: step,
	}); err != nil {
		logging.Error(ctx, err, "job status update failed", "worker-job-status-failed", logging.JobID(jobID), logging.Status(status))
	}
}

func (h *Handler) failJob(ctx context.Context, jobID int64, jobErr error) {
	if jobID == 0 || h.queries == nil {
		return
	}
	// Use fresh context — the task ctx is typically expired by the time failJob runs.
	dbCtx, cancel := freshDBContext()
	defer cancel()
	msg := jobErr.Error()
	if err := h.queries.UpdateJobError(dbCtx, db.UpdateJobErrorParams{
		ID:           jobID,
		ErrorMessage: &msg,
	}); err != nil {
		logging.Error(ctx, err, "could not mark job failed", "worker-job-mark-failed-failed", logging.JobID(jobID))
	}
}

// HandleAnalysis runs one saved analysis identified by the payload. The
// analysis row already exists (POST handler created it with status=pending);
// the worker loads it tenant-scoped via analysis.Service.Run, computes
// breakdowns, and transitions the row to completed or failed. asynq retry
// is safe — Run no-ops on terminal-state rows.
func (h *Handler) HandleAnalysis(ctx context.Context, t *asynq.Task) error {
	var p AnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	if p.TenantID == 0 || p.AnalysisID == 0 {
		return fmt.Errorf("missing analysis_id or tenant_id in payload")
	}
	if h.analysis == nil {
		logging.Error(ctx, nil, "analysis service not wired; task dropped", "worker-analysis-not-wired", logging.AnalysisID(p.AnalysisID))
		return nil // not fatal — surface via logs, don't spin retries
	}
	return h.analysis.Run(ctx, p.AnalysisID, p.TenantID)
}

// RegisterHandlers registers all pipeline task handlers on the mux.
func (h *Handler) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskTranslate, h.HandleTranslate)
	mux.HandleFunc(TaskAIStep, h.HandleAIStep)
	mux.HandleFunc(TaskPipeline, h.HandlePipeline)
	mux.HandleFunc(TaskAnalysis, h.HandleAnalysis)
}
