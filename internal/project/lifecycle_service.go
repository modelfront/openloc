package project

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/settings"
	"openloc/internal/workflow"
)

// Lifecycle services behind POST /publish and POST /clear-translations,
// shared with the MCP project tools.

// ErrPublishFiles means the draft's uploads could not be turned into jobs; the
// project stays a draft and publishing again skips the files already done.
var ErrPublishFiles = errors.New("the project's files could not be prepared — try again, or remove the file and upload it after publishing")

// Publish turns a draft into an in_progress project: it checks the fields a
// live project must have, creates the jobs for files uploaded while drafting
// (analyze requests their automatic analysis), flips the status and completes
// the project's single project.created event (recordPublish).
func (h *Handler) Publish(ctx context.Context, tenantID, userID, id int64, analyze bool) (db.Project, error) {
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.Project{}, err
	}
	if project.Status != db.ProjectStatusDraft {
		return db.Project{}, httputil.NewClientError("only a draft project can be published")
	}
	// The same rule create enforces for a non-draft; the CHECK constraint
	// behind it is the backstop, not the error surface.
	if _, err := validateMetadata(ctx, h.queries, tenantID, CreateParams{
		Name: project.Name, SourceLanguage: project.SourceLanguage, TargetLanguages: project.TargetLanguages,
	}, false); err != nil {
		return db.Project{}, err
	}
	if err := settings.CheckProfilePretranslateRouter(ctx, h.queries, project.SettingsProfileID); err != nil {
		return db.Project{}, err
	}

	// Files first, status second: a failure here leaves a draft whose
	// already-materialized files are skipped on the next attempt.
	if h.files != nil {
		if err := h.files.MaterializeProjectFiles(ctx, tenantID, project.ID, analyze); err != nil {
			logging.Error(ctx, err, "publish could not create jobs for the draft's files", "project-publish-materialize-failed",
				logging.ProjectID(project.ID))
			return db.Project{}, ErrPublishFiles
		}
	}

	published, err := h.queries.PublishProjectDraft(ctx, db.PublishProjectDraftParams{ID: id, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		// Another publish of this draft (a second click, the assistant and the
		// page at once) flipped the status first and recorded it; this one had
		// nothing left to do.
		current, gerr := h.queries.GetProject(ctx, db.GetProjectParams{ID: id, TenantID: tenantID})
		if gerr != nil {
			return db.Project{}, fmt.Errorf("read the project another publish published: %w", gerr)
		}
		if current.Status != db.ProjectStatusDraft {
			return current, nil
		}
	}
	if err != nil {
		return db.Project{}, err
	}

	extra := map[string]any{}
	if published.TemplateID != nil {
		for k, v := range templateDetails(ctx, h.queries, tenantID, *published.TemplateID) {
			extra[k] = v
		}
	}
	if profile, err := h.queries.GetSettingsProfile(ctx, published.SettingsProfileID); err == nil && profile.WorkflowID != nil {
		extra["workflow_id"] = *profile.WorkflowID
		if wf, err := h.queries.GetWorkflowAny(ctx, db.GetWorkflowAnyParams{ID: *profile.WorkflowID, TenantID: tenantID}); err == nil {
			extra["workflow_name"] = wf.Name
		}
	}
	snapshotSetup(ctx, h.queries, tenantID, published.SettingsProfileID, extra)
	recordPublish(ctx, h.queries, published, tenantID, userID, extra)
	return published, nil
}

// ClearScope is which translations ClearTranslations blanks.
type ClearScope string

const (
	ClearNonConfirmed ClearScope = "non_confirmed"
	ClearAll          ClearScope = "all"
)

// PreviewClearTranslations makes the same refusals as ClearTranslations and
// counts the translations it would blank, without writing: the clearable
// counts mirror the ClearTranslationsByProject predicate.
func (h *Handler) PreviewClearTranslations(ctx context.Context, tenantID, id int64, scope ClearScope) (int64, error) {
	if err := h.preflightClear(ctx, tenantID, id, scope); err != nil {
		return 0, err
	}
	counts, err := h.queries.GetTranslationCountsByProject(ctx, db.GetTranslationCountsByProjectParams{ProjectID: id})
	if err != nil {
		return 0, fmt.Errorf("count translations: %w", err)
	}
	var n int64
	for _, c := range counts {
		if scope == ClearAll {
			n += int64(c.ClearableAll)
		} else {
			n += int64(c.ClearableNonConfirmed)
		}
	}
	return n, nil
}

func (h *Handler) preflightClear(ctx context.Context, tenantID, id int64, scope ClearScope) error {
	if _, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: id, TenantID: tenantID}); err != nil {
		return err
	}
	if scope != ClearNonConfirmed && scope != ClearAll {
		return httputil.NewClientError(`scope must be "non_confirmed" or "all"`)
	}
	return h.checkClearable(ctx, tenantID, id)
}

// ClearResult is what a project-wide clear did.
type ClearResult struct {
	Cleared int64 `json:"cleared"`
	// Held: rows the clear would have cleared that another user has open.
	Held int64 `json:"held"`
}

// ClearTranslations blanks the translations of every file × language of the
// project and drops their QA findings and AI comments in the same
// transaction. Rows another user has open are kept and counted. The pre-flight is atomic: a running job refuses the whole call
// with httputil.ConflictError, a job past the first workflow step with
// httputil.ForbiddenError. The window between the check and the clear is
// accepted (the pretranslate enqueue has the symmetric race).
func (h *Handler) ClearTranslations(ctx context.Context, tenantID, id int64, scope ClearScope) (ClearResult, error) {
	if err := h.preflightClear(ctx, tenantID, id, scope); err != nil {
		return ClearResult{}, err
	}

	// A commit that blanked the targets but kept their findings would show
	// errors against empty segments: qa_results cascades on row DELETE, and a
	// clear is an UPDATE. The project's jobs are locked first, so the clear
	// queues behind a step change instead of deadlocking with its recount.
	var cleared, held int64
	err := database.InTx(ctx, h.pool, func(_ pgx.Tx, q db.Querier) error {
		if _, err := q.LockProjectJobsForWrite(ctx, db.LockProjectJobsForWriteParams{ProjectID: id, TenantID: tenantID}); err != nil {
			return fmt.Errorf("lock jobs: %w", err)
		}
		actor := auth.UserIDPtr(auth.UserIDFromContext(ctx))
		clearedIDs, err := q.ClearTranslationsByProject(ctx, db.ClearTranslationsByProjectParams{
			ProjectID: id,
			ScopeAll:  scope == ClearAll,
			ActorID:   actor,
		})
		if err != nil {
			return fmt.Errorf("clear translations: %w", err)
		}
		if held, err = q.CountHeldClearableByProject(ctx, db.CountHeldClearableByProjectParams{
			ProjectID: id, TenantID: tenantID, ActorID: actor, ScopeAll: scope == ClearAll,
		}); err != nil {
			return fmt.Errorf("count held rows: %w", err)
		}
		if err := q.DeleteQAResultsForTranslations(ctx, clearedIDs); err != nil {
			return fmt.Errorf("clear QA findings: %w", err)
		}
		// Only the rows the clear touched lose their AI remarks; a human comment
		// is about the segment and stays.
		if err := q.DeleteAICommentsForTranslations(ctx, clearedIDs); err != nil {
			return fmt.Errorf("clear AI comments: %w", err)
		}
		cleared = int64(len(clearedIDs))
		return nil
	})
	if err != nil {
		return ClearResult{}, err
	}

	actorID, actorName := projectevents.ActorFromContext(ctx)
	projectevents.Emit(ctx, h.queries, projectevents.Event{
		ProjectID: id,
		TenantID:  tenantID,
		Type:      projectevents.TypeTranslationsCleared,
		ActorID:   actorID,
		ActorName: actorName,
		Details:   map[string]any{"scope": string(scope), "cleared": cleared},
	})
	return ClearResult{Cleared: cleared, Held: held}, nil
}

// checkClearable refuses a clear while any job of the project is running or
// past its first workflow step. The project must already be verified in the
// tenant: ListJobsByProject is scoped by project only.
func (h *Handler) checkClearable(ctx context.Context, tenantID, projectID int64) error {
	jobs, err := h.queries.ListJobsByProject(ctx, projectID)
	if err != nil {
		return fmt.Errorf("list jobs: %w", err)
	}
	for _, j := range jobs {
		// IsPipelineRunning, not a raw status compare: a job whose pipeline
		// finished but awaits a human Complete is not running.
		if pipeline.IsPipelineRunning(string(j.Status), j.CurrentStep) {
			return httputil.NewConflictError("one or more jobs are running — wait for them to finish before clearing")
		}
		// An errored task takes no edits until its automation is re-run or the
		// task is completed — the same rule as the task edit guard.
		if j.Status == "failed" {
			return httputil.NewConflictError("one or more tasks have an error — re-run their automation or complete them before clearing")
		}
		// ListJobsByProject omits current_step_id.
		full, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: j.ID, TenantID: tenantID})
		if err != nil {
			return fmt.Errorf("load job: %w", err)
		}
		if !workflow.JobInFirstStep(ctx, h.queries, full) {
			return httputil.NewForbiddenError("one or more jobs are past the first workflow step")
		}
	}
	return nil
}
