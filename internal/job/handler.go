package job

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"openloc/internal/auth"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/notification"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/suggestion"
	"openloc/internal/taskedit"
	"openloc/internal/workflow"
)

// PipelineService defines the pipeline operations the job handler delegates to.
//
// Every method takes tenantID up-front: the
// pipeline's internal `GetFileTenant` lookup uses it as the authoritative
// scope, and a stale fileID from a deleted/cross-tenant payload is rejected
// at the DB layer rather than silently operating on someone else's row.
type PipelineService interface {
	TranslateFile(ctx context.Context, tenantID, fileID int64, sourceLang, targetLang string, onProgress pipeline.ProgressFunc, phases pipeline.TranslatePhases) (pipeline.TranslateOutcome, error)
	RunAIStep(ctx context.Context, tenantID, jobID int64, onProgress pipeline.ProgressFunc) (pipeline.AIStepResult, error)
	ExportFile(ctx context.Context, tenantID, fileID int64, targetLang string) ([]byte, string, error)
	ExportBilingualXLIFF(ctx context.Context, tenantID, fileID int64, sourceLang, targetLang string) ([]byte, string, error)
	SaveApprovedToTM(ctx context.Context, tenantID, fileID int64, sourceLang, targetLang string) (pipeline.TMWriteResult, error)
	MTPreview(ctx context.Context, tenantID, fileID int64, sourceLang, targetLang, sourceText, sourceRichText string, translationID int64) (pipeline.MTPreviewOutput, error)
}

// TaskEnqueuer enqueues pipeline tasks for async background processing.
type TaskEnqueuer interface {
	EnqueuePipeline(fileID int64, sourceLang, targetLang string, jobID, tenantID int64, phases pipeline.TranslatePhases, triggeredBy *int64) error
}

// TaskCanceller cancels running or queued asynq tasks.
type TaskCanceller interface {
	CancelProcessing(id string) error
}

// Service holds the job actions shared by the REST handlers and the MCP
// tools. Every method takes a job already resolved tenant-scoped (ResolveJob)
// or resolves it itself; caller mistakes come back as httputil
// Client/Conflict/Forbidden errors, a missing job as pgx.ErrNoRows.
type Service struct {
	queries   db.Querier
	pool      *pgxpool.Pool // wraps multi-step writes (job delete) in a transaction
	pipeline  PipelineService
	enqueuer  TaskEnqueuer                    // nil falls back to synchronous execution
	canceller TaskCanceller                   // nil skips asynq-level cancellation on delete
	draining  <-chan struct{}                 // closed at shutdown start; nil never drains (tests)
	guard     *taskedit.Guard                 // checks text writes against the job's current task
	failures  notification.JobFailureNotifier // nil sends no bell notification for a failed run
	workflow  *workflow.Service               // nil leaves a Pre-translation step open after Apply MT
}

// Handler serves the job routes over the shared Service.
type Handler struct {
	*Service
	// suggestions signs the insert token on an MT preview; nil issues none.
	suggestions *suggestion.Signer
}

func NewHandler(q db.Querier, pool *pgxpool.Pool, p PipelineService, enqueuer TaskEnqueuer) *Handler {
	return &Handler{Service: &Service{queries: q, pool: pool, pipeline: p, enqueuer: enqueuer, guard: taskedit.New(q, pool)}}
}

// SetSuggestionSigner signs MT previews, so inserting one records an MT event.
func (h *Handler) SetSuggestionSigner(s *suggestion.Signer) { h.suggestions = s }

// SetFailureNotifier sends a bell notification when a run the user waits on fails.
func (h *Handler) SetFailureNotifier(n notification.JobFailureNotifier) { h.failures = n }

// SetWorkflow lets Apply MT complete the job's Pre-translation step, as the
// upload worker does.
func (h *Handler) SetWorkflow(w *workflow.Service) { h.workflow = w }

// SetCanceller sets the optional task canceller for job deletion cleanup.
func (h *Handler) SetCanceller(c TaskCanceller) { h.canceller = c }

// SetDraining wires the server's shutdown signal into the synchronous pipeline
// runs: they are detached from the request context (a browser refresh must
// not abort paid MT work) and the draining channel is the one thing that
// still cancels them.
func (h *Handler) SetDraining(ch <-chan struct{}) { h.draining = ch }

func (h *Handler) HandleListByProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	views, err := h.ListByProject(r.Context(), auth.TenantIDFromContext(r.Context()), projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		httputil.NotFound(w, "project")
		return
	}
	if err != nil {
		logging.Error(r.Context(), err, "job list failed", "job-list-failed", logging.ProjectID(projectID))
		httputil.ServerError(w, "list", "jobs")
		return
	}
	httputil.Data(w, views)
}

func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	view, err := h.Detail(r.Context(), auth.TenantIDFromContext(r.Context()), jobID)
	if httputil.DBError(w, err, "job") {
		return
	}
	httputil.Data(w, view)
}

// HandleTranslations returns translations for a job (delegates to existing query).
func (h *Handler) HandleTranslations(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	job, err := h.queries.GetJobTenant(r.Context(), db.GetJobTenantParams{
		ID: jobID, TenantID: tenantID,
	})
	if httputil.DBError(w, err, "job") {
		return
	}

	translations, err := h.queries.ListTranslationsByFile(r.Context(), db.ListTranslationsByFileParams{
		FileID:         job.FileID,
		TargetLanguage: job.TargetLanguage,
	})
	if err != nil {
		httputil.ServerError(w, "list", "translations")
		return
	}

	httputil.Data(w, translations)
}

// resolveJob gets the job and its project's source language with tenant isolation.
func (h *Handler) resolveJob(r *http.Request) (db.Job, string, error) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		return db.Job{}, "", err
	}
	return h.ResolveJob(r.Context(), auth.TenantIDFromContext(r.Context()), jobID)
}

func (h *Handler) HandleDeleteJob(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	job, err := h.queries.GetJobTenant(r.Context(), db.GetJobTenantParams{
		ID:       jobID,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "job") {
		return
	}

	// Cancel any in-flight asynq task before deleting (best-effort)
	if h.canceller != nil && job.AsynqTaskID != nil && *job.AsynqTaskID != "" {
		if err := h.canceller.CancelProcessing(*job.AsynqTaskID); err != nil {
			logging.Warn(r.Context(), err, "asynq task cancel failed (may have already completed)", "job-asynq-cancel-failed", logging.JobID(jobID))
		}
	}

	// Both deletes run inside a single transaction so a crash or DB failure
	// between them can't leave an orphaned job row pointing at deleted
	// translations. Translations FK to segments, not jobs — there's
	// no DB-level CASCADE to lean on, so the atomicity has to come from the
	// application tx. Pinned by TestIntegrationJobDelete_RollsBackOnDeleteFailure.
	// The job is locked first, so the delete queues behind a step change.
	if err := database.InJobsTx(r.Context(), h.pool, auth.TenantIDFromContext(r.Context()), []int64{jobID}, func(_ pgx.Tx, q db.Querier) error {
		if err := q.DeleteJobTranslations(r.Context(), db.DeleteJobTranslationsParams{
			FileID:         job.FileID,
			TargetLanguage: job.TargetLanguage,
		}); err != nil {
			return fmt.Errorf("delete translations: %w", err)
		}
		return q.DeleteJob(r.Context(), jobID)
	}); err != nil {
		logging.Error(r.Context(), err, "job delete failed", "job-delete-failed", logging.JobID(jobID))
		httputil.ServerError(w, "delete", "job")
		return
	}

	// Audit. The job row is gone; the event's job_id is a
	// plain correlation value, and file_name/target_language in details keep
	// it renderable.
	h.emitJobEvent(r.Context(), projectevents.TypeJobDeleted, job, nil)

	// Removing a task can leave every remaining one completed — or leave none
	// at all, which reopens the project.
	h.reevaluateAutoComplete(r.Context(), job.FileID)

	httputil.NoContent(w)
}

const isoFormat = "2006-01-02T15:04:05Z07:00"

func formatTS(t pgtype.Timestamptz) string { return t.Time.Format(isoFormat) }

func formatOptionalTS(d pgtype.Timestamptz) *string {
	if !d.Valid {
		return nil
	}
	s := formatTS(d)
	return &s
}
