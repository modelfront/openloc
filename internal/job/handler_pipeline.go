package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/segmentbulk"
	"openloc/internal/suggestion"
	"openloc/internal/taskedit"
)

// Pipeline operations: per-phase apply (TM, MT, Predict), project-level
// pre-translate fan-out, file export, completion. Manual triggers use the
// phase-split endpoints (tm-apply, mt-apply, predict); automatic runs start
// from the upload hook.

// HandleTMApply runs ONLY the TM lookup phase (no MT fallback). Segments with
// no TM match stay untranslated. Surfaced as the "Apply TM" button on the job
// page.
func (h *Handler) HandleTMApply(w http.ResponseWriter, r *http.Request) {
	h.runPhases(w, r, pipeline.TranslatePhases{UseTM: true}, "tm-apply")
}

// HandleMTApply runs ONLY the MT phase, skipping TM lookup entirely. Surfaced
// as the "Apply MT" button on the job page; lets the user re-MT segments even
// when TM has a match (e.g., to compare or override TM-filled translations).
func (h *Handler) HandleMTApply(w http.ResponseWriter, r *http.Request) {
	h.runPhases(w, r, pipeline.TranslatePhases{UseMT: true}, "mt-apply")
}

// runPhases runs the phases on the job, or only on the editor's selection
// when the body carries `translation_ids` (no body = the whole job). The
// phases read the job's own file and language, so an id from elsewhere is
// never touched.
func (h *Handler) runPhases(w http.ResponseWriter, r *http.Request, phases pipeline.TranslatePhases, label string) {
	job, sourceLang, err := h.resolveJob(r)
	if err != nil {
		httputil.NotFound(w, "job")
		return
	}
	var req segmentbulk.Request
	if err := httputil.Decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.TranslationIDs != nil {
		if phases.TranslationIDs, err = segmentbulk.NormalizeIDs(req.TranslationIDs); err != nil {
			httputil.ServiceError(w, err, "run", label)
			return
		}
	}
	res, err := h.ApplyPhases(r.Context(), auth.TenantIDFromContext(r.Context()), job, sourceLang, phases, label)
	if err != nil {
		writeRunError(w, err, label)
		return
	}
	if res.Stopped {
		httputil.JSON(w, http.StatusOK, map[string]any{"message": label + " stopped", "job_id": job.ID, "stopped": true})
		return
	}
	resp := taskedit.WithReopened(map[string]any{"message": label + " complete", "job_id": job.ID, "held": res.Held}, res.TaskReopened)
	if res.SkipReason != "" {
		resp["skip_reason"] = res.SkipReason
	}
	httputil.JSON(w, http.StatusOK, resp)
}

// writeRunError maps a failed synchronous run onto the response: 503 for a
// drain (resumable), 400 for a provider or configuration message the user
// can act on, 409 when another run holds the job, 500 otherwise.
func writeRunError(w http.ResponseWriter, err error, label string) {
	if errors.Is(err, ErrRunInterrupted) {
		httputil.Error(w, http.StatusServiceUnavailable, ErrRunInterrupted.Error())
		return
	}
	httputil.ServiceError(w, err, "run", label)
}

// phaseSkipReason returns the skip-reason for the response body when
// the requested phase no-op'd on empty input. Single-phase callers
// only: a combined TM+MT caller needs per-phase reasons in the
// response shape, not first-match precedence.
func phaseSkipReason(phases pipeline.TranslatePhases, outcome pipeline.TranslateOutcome) string {
	if phases.UseTM && outcome.TMSkipReason == pipeline.PhaseSkipNoSegments {
		return string(pipeline.PhaseSkipNoSegments)
	}
	if phases.UseMT && outcome.MTSkipReason == pipeline.PhaseSkipNoSegments {
		return string(pipeline.PhaseSkipNoSegments)
	}
	return ""
}

// phaseUnactionableMessage returns a user-facing error message when a
// requested phase was a no-op due to missing project configuration. Empty
// string when every requested phase either ran or skipped for a non-config
// reason (e.g., nothing to translate).
func phaseUnactionableMessage(phases pipeline.TranslatePhases, outcome pipeline.TranslateOutcome, sourceLang, targetLang string) string {
	if phases.UseTM && outcome.TMSkipReason == pipeline.PhaseSkipNoAssignment {
		return fmt.Sprintf("No TM configured for %s → %s — add one in project settings", sourceLang, targetLang)
	}
	if phases.UseMT && outcome.MTSkipReason == pipeline.PhaseSkipNoAssignment {
		return fmt.Sprintf("No MT engine configured for %s → %s — add one in project settings", sourceLang, targetLang)
	}
	if phases.UseMT && outcome.MTSkipReason == pipeline.PhaseSkipNoRoute {
		return fmt.Sprintf("The project's MT router has no engine for %s — add a route for it, or a default engine, in MT → Routers", targetLang)
	}
	if phases.UseMT && outcome.MTSkipReason == pipeline.PhaseSkipNoProvider {
		return fmt.Sprintf("The AI engine assigned for %s → %s has no connected provider — connect it in AI settings → Providers", sourceLang, targetLang)
	}
	return ""
}

// pipelineProgress mirrors the worker's makeProgressFunc for the synchronous
// path: persist per-chunk progress (the FE's 2s poll renders it), bump
// updated_at so the claim's staleness escape can never steal a live run, and
// cooperatively cancel the detached run when the job was deleted mid-flight or
// a user requested a stop (after this chunk's progress is written, so the kept
// work is reported in full). Background ctx like setJobStatus: the run is not
// tied to the request.
func (s *Service) pipelineProgress(jobID int64, cancelRun context.CancelFunc) pipeline.ProgressFunc {
	return func(phase string, done, total int) {
		dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		state, stateErr := pipeline.LoadRunState(s.queries, jobID)
		if stateErr == nil && state.Deleted {
			cancelRun()
			return
		}
		if stateErr == nil && state.StopRequested {
			defer cancelRun()
		}
		dc, tot := int32(done), int32(total)
		step := fmt.Sprintf("%s: %d / %d", strings.ToUpper(phase), done, total)
		if err := s.queries.UpdateJobProgress(dbCtx, db.UpdateJobProgressParams{
			ID: jobID, TranslatedCount: &dc, TotalCount: &tot, CurrentStep: step,
		}); err != nil {
			logging.Warn(dbCtx, err, "failed to persist pipeline progress", "job-pipeline-progress-failed", logging.JobID(jobID))
		}
	}
}

// setJobStatus / markJobError mirror the worker's pattern: fresh context with
// a short deadline so status writes don't ride on a potentially-cancelled
// request context, and errors are logged (not silently dropped) so a failed
// status write surfaces in prod logs.
func (s *Service) setJobStatus(jobID int64, status, step string) {
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.queries.UpdateJobStatus(dbCtx, db.UpdateJobStatusParams{ID: jobID, Status: status, CurrentStep: step}); err != nil {
		logging.Error(dbCtx, err, "job status update failed", "job-status-update-failed", logging.JobID(jobID), logging.Status(status))
	}
}

func (s *Service) markJobError(jobID int64, jobErr error) {
	dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg := jobErr.Error()
	if err := s.queries.UpdateJobError(dbCtx, db.UpdateJobErrorParams{ID: jobID, ErrorMessage: &msg}); err != nil {
		logging.Error(dbCtx, err, "could not record job error", "job-error-record-failed", logging.JobID(jobID))
	}
}

// HandleClearTranslations reverts every (non-confirmed | all)
// translation for this job. Locked rows, and rows another user holds, are
// skipped in SQL.
func (h *Handler) HandleClearTranslations(w http.ResponseWriter, r *http.Request) {
	job, _, err := h.resolveJob(r)
	if err != nil {
		httputil.NotFound(w, "job")
		return
	}
	var req struct {
		Scope string `json:"scope"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	res, err := h.ClearTranslations(r.Context(), job, req.Scope)
	if httputil.ServiceError(w, err, "clear", "translations") {
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"cleared": res.Cleared, "held": res.Held, "workflow_reopened": res.WorkflowReopened})
}

// parseClearScope: named scopes (not a raw bool) so an empty body
// can't accidentally clear-all.
func parseClearScope(s string) (scopeAll, ok bool) {
	switch s {
	case "all":
		return true, true
	case "non_confirmed":
		return false, true
	default:
		return false, false
	}
}

// HandleMTPreview returns an MT translation for one source segment for the
// CAT editor sidebar. When the pipeline already translated the segment
// (translation_history kind='mt'), that stored output is returned — with an
// AI-as-MT engine a live preview is a paid LLM round trip per segment click,
// re-deriving text already on record (and without the segment prompt, so
// possibly a different text than the pipeline produced). Only segments with
// no MT on record live-fetch, and a live preview is never persisted. The CAT editor's React Query 5-min staleTime
// caches repeat segment visits.
func (h *Handler) HandleMTPreview(w http.ResponseWriter, r *http.Request) {
	sourceText := strings.TrimSpace(r.URL.Query().Get("source_text"))
	if sourceText == "" {
		httputil.BadRequest(w, "source_text required")
		return
	}
	job, sourceLang, err := h.resolveJob(r)
	if err != nil {
		httputil.NotFound(w, "job")
		return
	}
	var translationID int64
	if tid, err := strconv.ParseInt(r.URL.Query().Get("translation_id"), 10, 64); err == nil {
		translationID = tid
	}
	res, err := h.MTPreview(r.Context(), auth.TenantIDFromContext(r.Context()), job, sourceLang, sourceText, translationID)
	if err != nil {
		logging.Error(r.Context(), err, "MT preview failed", "mt-preview-failed", logging.JobID(job.ID))
		// A pair the engine's catalog refuses is the user's to fix, not a bug.
		if msg, ok := httputil.AsClientError(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		httputil.ServerError(w, "fetch", "mt preview")
		return
	}
	body := map[string]any{"text": res.Text, "rich_text": res.RichText, "engine": res.Engine}
	if res.Stored {
		body["stored"] = true
	}
	if res.Text != "" && h.suggestions != nil {
		token, err := h.suggestions.Sign(auth.TenantIDFromContext(r.Context()), suggestion.Claims{
			// The segment's exact source, as the editor sent it; the engine
			// got it trimmed.
			Kind: suggestion.KindMT, JobID: job.ID, Source: suggestion.SourceHash(r.URL.Query().Get("source_text")), TranslationID: translationID,
			MTEngineID: res.MTEngineID, AIEngineID: res.AIEngineID, EngineName: res.Engine,
		})
		if err != nil {
			httputil.ServerError(w, "sign", "mt preview")
			return
		}
		// Inserting the suggestion with this token records an MT event.
		body["insert_token"] = token
	}
	httputil.JSON(w, http.StatusOK, body)
}

// engineNameFromDetails extracts engine_name from a translation_history
// details payload (EmitAutomationHistory stamps it on every kind='mt' row).
// Empty when absent or unparseable — the sidebar shows the text either way.
func engineNameFromDetails(details []byte) string {
	var d struct {
		EngineName string `json:"engine_name"`
	}
	if err := json.Unmarshal(details, &d); err != nil {
		return ""
	}
	return d.EngineName
}

// HandleAIStep re-runs the QE+APE predict (extension or LLM, per the engine
// row) for the job's active workflow step — the manager-gated
// "Re-run AI step" action. Predict normally runs automatically when the step
// becomes active (workflow activation hook); this endpoint lets a manager
// re-trigger it. Mirrors runPhases' status lifecycle so the job-status poll
// sees the same transitions, and surfaces a provider failure as a 400 with an
// actionable message (ClientError).
func (h *Handler) HandleAIStep(w http.ResponseWriter, r *http.Request) {
	job, _, err := h.resolveJob(r)
	if err != nil {
		httputil.NotFound(w, "job")
		return
	}
	res, err := h.RunAIStep(r.Context(), auth.TenantIDFromContext(r.Context()), job)
	if err != nil {
		writeRunError(w, err, "ai-step")
		return
	}
	if res.Stopped {
		httputil.JSON(w, http.StatusOK, map[string]any{"message": "ai-step stopped", "job_id": job.ID, "stopped": true})
		return
	}
	writeAIStepResponse(w, job.ID, res.AIStepResult)
}

// aiStepSkipMessage maps a config-missing skip to its user-facing 400 message.
func aiStepSkipMessage(reason pipeline.PhaseSkipReason) string {
	switch reason {
	case pipeline.PhaseSkipNoAIStep:
		return "the current workflow step has no AI router to run"
	case pipeline.PhaseSkipNoEngineForLanguage:
		return "this step's AI router has no engine for the job's target language — add a rule for it or set a default engine"
	case pipeline.PhaseSkipNoExtensions:
		return "custom providers are not available on this server — ask your administrator to set AI_SECRETS_KEY"
	case pipeline.PhaseSkipNoProvider:
		return "this step's AI provider is not connected (or the engine row has no model) — check AI settings → Providers"
	}
	return ""
}

func writeAIStepResponse(w http.ResponseWriter, jobID int64, res pipeline.AIStepResult) {
	resp := map[string]any{
		"message":       "ai-step complete",
		"job_id":        jobID,
		"total":         res.Total,
		"auto_approved": res.AutoApproved,
		"needs_review":  res.NeedsReview,
	}
	if res.SkipReason == pipeline.PhaseSkipNoSegments {
		resp["skip_reason"] = string(pipeline.PhaseSkipNoSegments)
	}
	httputil.JSON(w, http.StatusOK, resp)
}

// HandleExport serves the job-level export endpoint. Default behavior:
// source-format mirror (DOCX→DOCX, etc.). `?format=xliff` emits a
// bilingual XLIFF 1.2 document for hand-off to external translators in
// other CAT tools. The bilingual format is
// the same one served by GET /api/files/{fileID}/export — kept
// symmetric so users get consistent behavior whether they navigate from
// the file detail page or from the CAT editor.
func (h *Handler) HandleExport(w http.ResponseWriter, r *http.Request) {
	job, sourceLang, err := h.resolveJob(r)
	if err != nil {
		httputil.NotFound(w, "job")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	format := r.URL.Query().Get("format")
	switch format {
	case "", "source":
		data, filename, err := h.pipeline.ExportFile(r.Context(), tenantID, job.FileID, job.TargetLanguage)
		if err != nil {
			if msg, ok := pipeline.UserExportMessage(err); ok {
				httputil.BadRequest(w, msg)
				return
			}
			logging.Error(r.Context(), err, "job export failed", "job-export-failed", logging.JobID(job.ID))
			httputil.ServerError(w, "export", "file")
			return
		}
		// Audit: a deliverable download is a user-visible
		// project action; emit only after the export actually succeeded.
		h.emitJobEvent(r.Context(), projectevents.TypeJobExported, job, map[string]any{"format": "source"})
		w.Header().Set("Content-Type", "application/octet-stream")
		httputil.SetAttachmentFilename(w, pipeline.SourceExportFilename(filename, job.TargetLanguage))
		w.Write(data)
	case "xliff":
		data, filename, err := h.pipeline.ExportBilingualXLIFF(r.Context(), tenantID, job.FileID, sourceLang, job.TargetLanguage)
		if err != nil {
			logging.Error(r.Context(), err, "bilingual XLIFF export failed", "job-xliff-export-failed", logging.JobID(job.ID))
			httputil.ServerError(w, "export", "bilingual XLIFF")
			return
		}
		h.emitJobEvent(r.Context(), projectevents.TypeJobExported, job, map[string]any{"format": "xliff"})
		w.Header().Set("Content-Type", "application/x-xliff+xml")
		httputil.SetAttachmentFilename(w, filename)
		w.Write(data)
	default:
		httputil.BadRequest(w, fmt.Sprintf("unsupported format %q (expected: source, xliff)", format))
	}
}

// HandleComplete marks a job completed and writes its approved segments to TM.
func (h *Handler) HandleComplete(w http.ResponseWriter, r *http.Request) {
	job, sourceLang, err := h.resolveJob(r)
	if err != nil {
		httputil.NotFound(w, "job")
		return
	}
	res, err := h.CompleteJob(r.Context(), auth.TenantIDFromContext(r.Context()), auth.UserIDFromContext(r.Context()), job, sourceLang)
	if httputil.ServiceError(w, err, "complete", "job") {
		return
	}
	resp := map[string]any{
		"message":        "job completed",
		"job_id":         job.ID,
		"approved_count": res.ApprovedCount,
		"tm_saved_count": res.TMSavedCount,
	}
	if res.NoWriteTMs {
		resp["warning"] = "No matching write TMs found for this language pair. Segments were not saved to Translation Memory. Check your template's TM assignments."
	}
	httputil.JSON(w, http.StatusOK, resp)
}
