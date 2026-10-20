package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"openloc/internal/aiengine"
	"openloc/internal/airouter"
	"openloc/internal/aiusage"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/enginehealth"
	"openloc/internal/extension"
	"openloc/internal/llmdebug"
	"openloc/internal/logging"
	"openloc/internal/timing"
)

// AIStepResult reports the outcome of a QE+APE workflow-step predict run.
type AIStepResult struct {
	Total        int             `json:"total"`
	AutoApproved int             `json:"auto_approved"`
	NeedsReview  int             `json:"needs_review"`
	SkipReason   PhaseSkipReason `json:"skip_reason,omitempty"`
}

// PhaseSkipNoAIStep — the job's active workflow step carries no AI router, so
// there's nothing for RunAIStep to do. A benign no-op for the activation hook;
// the manual re-run endpoint surfaces it as a 400.
const PhaseSkipNoAIStep PhaseSkipReason = "no_ai_step"

// PhaseSkipNoEngineForLanguage — the step's AI router has no rule for the
// job's target language and no default engine, so nothing reviews this job.
// Benign like PhaseSkipNoAIStep; the manual re-run endpoint names the fix.
const PhaseSkipNoEngineForLanguage PhaseSkipReason = "no_engine_for_language"

// predictVerdict is a backend's verdict on one row, keyed by translation id.
// When Postedit is set it replaces the translation, and PosteditApproved
// (not Approved) decides the approval.
type predictVerdict struct {
	ID               string
	Approved         bool
	Postedit         *string
	PosteditApproved *bool
}

// aiPredictResult is one segment's predict verdict, normalized so outcome
// routing (routePredictOutcome / routeQEOutcome) is shared across backends.
// Note carries a 0-100 score (→ translations.qe_note) and Comment a
// reviewer-facing remark (→ an AI-authored segment comment).
type aiPredictResult struct {
	predictVerdict
	Note    *string
	Comment *string
	// PosteditRichText is the postedit with its inline tags decoded back to
	// markup. Nil when the segment has no tags or there is no postedit.
	PosteditRichText *string
	// UnknownTags are chip labels the model returned that decode could not
	// resolve. Nothing downstream can spot these — a leftover `{4}` is
	// indistinguishable from a real placeholder — so they become a comment.
	UnknownTags []string
}

// aiComment is the reviewer-facing comment for one result: the model's own
// remark, plus a note about any inline tag chip it returned that could not be
// resolved back to markup.
//
// The tag note is the only signal those leftovers produce. A stray `{4}` in the
// target renders exactly like a real placeholder, so the tag QA rule compares it
// as a match and reports nothing — it would reach the delivered file unnoticed.
func aiComment(r aiPredictResult) *string {
	parts := make([]string, 0, 2)
	if r.Comment != nil {
		parts = append(parts, *r.Comment)
	}
	if len(r.UnknownTags) > 0 {
		parts = append(parts, fmt.Sprintf(
			"AI returned inline tags that do not match the source: %s. Check the tags in this segment.",
			strings.Join(r.UnknownTags, ", ")))
	}
	if len(parts) == 0 {
		return nil
	}
	joined := strings.Join(parts, "\n\n")
	return &joined
}

// aiPredictRunner scores one batch of pending rows. Implementations: the
// extension closure (aistep_ext.go) and the LLM closure (aistep_llm.go). A
// result may be missing for a row (e.g. bad-output action "ignore") — the
// routing loop skips rows without a result.
type aiPredictRunner func(ctx context.Context, batch []db.ListPreTranslatedByFileRow, batchIdx, totalBatches int) ([]aiPredictResult, error)

// RunAIStep runs the QE+APE predict for the job's currently-active workflow
// step. The step's engine row matching the job's source→target pair
// decides the backend:
//   - a row without a system prompt runs the extension its key belongs to;
//     it is routed as APE when the extension has the ape capability, else QE;
//   - an LLM row (system prompt + model preset) runs Claude/OpenAI with the
//     {status, postedit, score} output contract; QE-vs-APE derives from the
//     prompt's output mode (out_text ⇒ APE).
//
// Either way the file's pre_translated rows (MT and TM/fuzzy alike) for the
// job's target language are scored and routed:
//   - QE  auto-approves where approved; everything else drops to the human.
//   - APE additionally applies the postedit and auto-approves where the
//     postedit is approved (routePredictOutcome).
//
// It returns counts for the manual re-run endpoint. A benign no-op sets
// SkipReason with a nil error (no active AI step / provider not connected /
// nothing pre-translated). A provider request failure is surfaced as an
// httputil.ClientError carrying an actionable message.
//
// Tenant scoping: the file is loaded via GetFileTenant, so a cross-tenant jobID
// is rejected at the DB layer before any provider call.
//
// onProgress (optional) is reported once per provider batch with phase
// PhaseQEAPE; the run stops between batches when ctx is cancelled.
func (s *Service) RunAIStep(ctx context.Context, tenantID, jobID int64, onProgress ProgressFunc) (AIStepResult, error) {
	var res AIStepResult

	job, err := s.queries.GetJob(ctx, jobID)
	if err != nil {
		return res, fmt.Errorf("get job %d: %w", jobID, err)
	}
	file, err := s.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: job.FileID, TenantID: tenantID})
	if err != nil {
		return res, fmt.Errorf("get file %d: %w", job.FileID, err)
	}
	project, err := s.queries.GetProjectByID(ctx, file.ProjectID)
	if err != nil {
		return res, fmt.Errorf("get project %d: %w", file.ProjectID, err)
	}

	// Resolve the active step, then its AI router → the engine for this job's
	// target language.
	step, err := s.queries.GetActiveStepForJob(ctx, jobID)
	if err != nil {
		return res, fmt.Errorf("no active workflow step for job %d: %w", jobID, err)
	}
	ws, err := s.queries.GetWorkflowStep(ctx, step.WorkflowStepID)
	if err != nil {
		return res, fmt.Errorf("get workflow step %d: %w", step.WorkflowStepID, err)
	}
	if ws.AiRouterID == nil {
		res.SkipReason = PhaseSkipNoAIStep
		return res, nil
	}
	engineID, err := airouter.ResolveEngineID(ctx, s.queries, tenantID, *ws.AiRouterID, job.TargetLanguage)
	if err != nil {
		return res, fmt.Errorf("resolve AI router %d: %w", *ws.AiRouterID, err)
	}
	if engineID == nil {
		res.SkipReason = PhaseSkipNoEngineForLanguage
		return res, nil
	}
	engine, err := s.queries.GetAIEngine(ctx, db.GetAIEngineParams{ID: *engineID, TenantID: tenantID})
	if err != nil {
		return res, fmt.Errorf("get AI engine %d: %w", *engineID, err)
	}
	// Switched off, same as an MT engine: the router points at something that
	// cannot run, which fails the job rather than quietly skipping
	if !engine.Enabled {
		return res, aiStepUnusable(&EngineUnusableError{EngineName: engine.Name, Reason: enginehealth.Disabled})
	}
	// Only qe_ape-purpose engines review. The router save path
	// validates this too (airouter.Validate); the guard catches older rows
	// saved before that check.
	if engine.Purpose != aiengine.PurposeQEAPE {
		logging.Warn(ctx, nil, "AI engine is an MT engine; QE+APE skipped", "ai-step-engine-is-mt", logging.JobID(jobID), logging.AIEngineID(engine.ID))
		res.SkipReason = PhaseSkipNoAIStep
		return res, nil
	}

	sourceLang := project.SourceLanguage
	targetLang := job.TargetLanguage
	stepName := ws.Name

	// Debug-recorder context for every provider call this step makes (both
	// the extension and LLM runners receive this ctx via the batching loop).
	ctx = llmdebug.WithMeta(ctx, llmdebug.Meta{
		JobID: jobID, FileID: file.ID, Project: project.Name, Step: stepName,
	})
	ctx = aiusage.WithMeta(ctx, aiusage.Meta{
		TenantID: tenantID, AIEngineID: engine.ID, AIEngineName: engine.Name, Purpose: engine.Purpose,
		ProjectID: project.ID, JobID: jobID,
	})

	// Resolve which of the engine's language-routing rows applies to this
	// job's pair (wildcards supported — same matcher as the AI-as-MT path).
	engineRows, err := s.queries.ListAIEngineRows(ctx, engine.ID)
	if err != nil {
		return res, fmt.Errorf("list rows for AI engine %d: %w", engine.ID, err)
	}
	matchedRow := matchAIEngineRow(engineRows, sourceLang, targetLang)
	if matchedRow == nil {
		// The engine has no row covering this job's language pair — nothing to
		// run. Benign no-op (mirrors "no AI step").
		res.SkipReason = PhaseSkipNoAIStep
		return res, nil
	}
	// The key this row ran with was deleted from AI settings. A row's token is
	// required at save, so a null one can only mean that.
	if matchedRow.TokenID == nil {
		return res, aiStepUnusable(&EngineUnusableError{EngineName: engine.Name, Reason: enginehealth.NoKey})
	}

	// Resolve the backend for the matched row: a row without a system prompt
	// runs an extension, a row with one runs an LLM.
	var (
		runPredict    aiPredictRunner
		mode          string
		detailsSource string
		detailsModel  string
		batchSize     = llmPredictBatchSize
		predictStage  = timing.StageAIPredictLLM
	)
	if matchedRow.SystemPromptID == nil {
		var skip PhaseSkipReason
		var pred extension.BatchPredictor
		pred, skip, err = s.resolveExtensionPredictor(ctx, tenantID, engine.Name, *matchedRow.TokenID)
		if err != nil {
			return res, err
		}
		if skip != "" {
			logging.Info(ctx, "extensions not configured, predict skipped", "ai-step-extensions-unconfigured", logging.JobID(jobID))
			res.SkipReason = skip
			return res, nil
		}
		mode = "qe"
		if pred.APE() {
			mode = "qe_ape"
		}
		detailsSource = pred.Provider()
		batchSize = pred.BatchSize()
		predictStage = timing.StageAIPredictExtension
		runPredict = s.extensionPredictRunner(pred, file, project, stepName, jobID, sourceLang, targetLang)
	} else {
		var skip PhaseSkipReason
		runPredict, mode, detailsSource, detailsModel, skip, err = s.resolveLLMPredictRunner(ctx, tenantID, matchedRow, stepName, jobID, project, sourceLang, targetLang)
		if err != nil {
			return res, err
		}
		if skip != "" {
			res.SkipReason = skip
			return res, nil
		}
	}

	// A round that re-runs its AI after a rollback reviews every unconfirmed,
	// unlocked segment with text; any other run only the pre-translated ones.
	pending, err := s.queries.ListPreTranslatedByFile(ctx, db.ListPreTranslatedByFileParams{
		FileID: file.ID, TargetLanguage: targetLang, ReviewAll: step.AiReviewAll,
	})
	if err != nil {
		return res, fmt.Errorf("list pre-translated: %w", err)
	}
	res.Total = len(pending)
	if len(pending) == 0 {
		res.SkipReason = PhaseSkipNoSegments
		return res, nil
	}

	// The predict backend decides which slug the batches land under, so a
	// dashboard can compare an extension against an LLM reviewer.
	predictMeta := []timing.Option{timing.Meta("source", detailsSource)}
	if detailsModel != "" {
		predictMeta = append(predictMeta, timing.Meta("model", detailsModel))
	}

	// Provider calls run up to aiPredictMaxInFlight batches at once; their
	// results are routed and written here, one batch at a time in file order.
	// After a failed batch no more are sent, but the answers of the batches
	// already in flight are still written, so a retry does not pay for them
	// again; then the step fails.
	totalBatches := (len(pending) + batchSize - 1) / batchSize
	predictions := predictConcurrently(ctx, runPredict, pending, batchSize, aiPredictMaxInFlight, predictStage, predictMeta)
	defer predictions.stop()
	var failed error
	for batchIdx := 0; batchIdx < totalBatches; batchIdx++ {
		start := batchIdx * batchSize
		end := min(start+batchSize, len(pending))
		batch := pending[start:end]
		var results []aiPredictResult
		if failed == nil {
			var err error
			results, err = predictions.wait(batchIdx)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return res, fmt.Errorf("qe/ape aborted (reviewed %d/%d): %w", start, len(pending), ctxErr)
			}
			if err != nil {
				failed = err
				predictions.halt()
				continue
			}
		} else {
			var ok bool
			if results, ok = predictions.received(batchIdx); !ok {
				continue
			}
		}

		// Everything from here to the end of the batch is local routing and
		// DB writes — separated from the provider call so a slow QE+APE step
		// can be blamed on the right side of the wire.
		// Sibling of the predict span above, not a child — distinct ctx name so
		// the loop's next iteration still branches off the batch loop's ctx.
		routeCtx, doneRoute := timing.Start(ctx, timing.StageAIRouteApply)

		resultMap := make(map[string]aiPredictResult, len(results))
		for _, r := range results {
			resultMap[r.ID] = r
		}

		// Rows this batch's APE rewrote, for the stale-QA drop below. Scoped to
		// the batch so the delete stays a small array.
		var rewritten []int64

		for _, row := range batch {
			result, ok := resultMap[strconv.FormatInt(row.ID, 10)]
			if !ok {
				logging.Warn(ctx, nil, "predict returned no result for translation", "ai-step-no-result", logging.JobID(jobID), logging.TranslationID(row.ID))
				continue
			}
			var params db.UpdateTranslationPredictParams
			if mode != "qe" {
				params = routePredictOutcome(row, result)
			} else {
				params = routeQEOutcome(row, result)
			}
			params.QeNote = result.Note
			params.ReviewAll = step.AiReviewAll
			if _, err := s.queries.UpdateTranslationPredict(routeCtx, params); err != nil {
				// ErrNoRows = the row was human-confirmed between the
				// fetch and this write. Skip it (and its history emission).
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				logging.Error(ctx, err, "failed to save predict result", "ai-step-save-failed", logging.JobID(jobID), logging.TranslationID(row.ID))
				continue
			}
			if params.Status == db.TranslationStatusAutoApproved {
				res.AutoApproved++
			} else {
				res.NeedsReview++
			}
			// The LLM's `comment` output lands as an AI-authored segment
			// comment (NULL user_id). Best-effort, like
			// history emission: a comment failure must not fail the step.
			if comment := aiComment(result); comment != nil {
				if _, err := s.queries.CreateAIComment(routeCtx, db.CreateAICommentParams{
					TranslationID: row.ID,
					Content:       *comment,
				}); err != nil {
					logging.Error(ctx, err, "failed to create AI comment", "ai-step-comment-failed", logging.JobID(jobID), logging.TranslationID(row.ID))
				}
			}
			// Two mutually exclusive outcomes get a history row, and which one
			// depends on whether the text moved:
			//
			//   ape_approved / ape_needs_review — APE rewrote the target, so an
			//       `ape` row carries the new text and flips origin. Its
			//       details.predict_outcome also records whether that rewrite was
			//       approved, which is why an approving APE needs no second row.
			//   approved — the target is accepted untouched (QE's only approval,
			//       and APE outcome 1). Nothing to show as text, so an
			//       `auto_approved` row records the approval on its own.
			//
			// `rejected` changes neither text nor approval and still writes
			// nothing; it lives on as predict_outcome state.
			details := map[string]any{
				"predict_outcome": string(params.PredictOutcome.PredictOutcome),
				"source":          detailsSource,
			}
			if detailsModel != "" {
				details["model"] = detailsModel
			}
			if result.Note != nil {
				details["note"] = *result.Note
			}
			switch {
			case params.PredictOutcome.Valid &&
				(params.PredictOutcome.PredictOutcome == db.PredictOutcomeApeApproved ||
					params.PredictOutcome.PredictOutcome == db.PredictOutcomeApeNeedsReview):
				s.EmitAutomationHistory(routeCtx, AutomationEmitOpts{
					TranslationID: row.ID,
					Kind:          db.TranslationHistoryKindApe,
					Origin:        db.TranslationOriginApe,
					Text:          params.ActiveText,
					RichText:      params.ActiveRichText,
					CreatedBy:     TriggeredByFromContext(routeCtx),
					// Which AI system post-edited this, so APE volume charts
					// separately from AI-MT and from vendor MT.
					AIEngineID: &engine.ID,
					// The step that ran it, which the History panel names.
					JobStepAssignmentID: &step.ID,
					Details:             details,
				})
				rewritten = append(rewritten, row.ID)

			case params.Status == db.TranslationStatusAutoApproved:
				// Status-only: no Text, so this row never becomes the next
				// confirm's edit-distance baseline.
				EmitHumanHistory(routeCtx, s.queries, HumanEmitOpts{
					TranslationID:       row.ID,
					Kind:                db.TranslationHistoryKindAutoApproved,
					CreatedBy:           TriggeredByFromContext(routeCtx),
					JobStepAssignmentID: &step.ID,
					AIEngineID:          &engine.ID,
					Details:             details,
				})
			}
		}
		// The postedit replaced the target, so findings computed on the old
		// text no longer describe the row — ignores included, since the ignore
		// is fingerprinted on a message derived from that text.
		s.dropStaleQAResults(routeCtx, rewritten)
		doneRoute(nil)
		if onProgress != nil {
			onProgress(AIStepLabel(mode), end, len(pending))
		}
	}

	return res, failed
}
