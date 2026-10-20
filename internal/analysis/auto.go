package analysis

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
)

// EnsurePendingAutoAnalysis records that an upload asked for auto-analysis.
// It coalesces: the partial unique index uq_pending_auto_analysis
// means at most one pending auto analysis exists per project, so a multi-file
// upload (or a burst of uploads) collapses into a single row. Called from the
// file upload handler when the upload carries analyze=true. Best-effort — a
// failure is logged, not propagated (the upload itself already succeeded and a
// manual analysis is always available).
func EnsurePendingAutoAnalysis(ctx context.Context, q db.Querier, tenantID, projectID int64, sourceLanguage string) {
	if q == nil || tenantID == 0 || projectID == 0 {
		return
	}
	name := "Auto analysis " + time.Now().UTC().Format("2006-01-02 15:04")
	created, err := q.EnsurePendingAutoAnalysis(ctx, db.EnsurePendingAutoAnalysisParams{
		TenantID:       tenantID,
		ProjectID:      projectID,
		Name:           name,
		SourceLanguage: sourceLanguage,
	})
	// ON CONFLICT DO NOTHING → no row → ErrNoRows means a pending auto analysis
	// already exists for the project (coalesced). Expected, not an error.
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			logging.Warn(ctx, err, "auto-analysis: ensuring the pending analysis failed", "auto-analysis-ensure-failed", logging.ProjectID(projectID))
		}
		return
	}

	// Audit: a genuinely new pending row was created.
	// ActorFromContext resolves the uploader on the HTTP path and nil
	// (= System) on the worker path — it never panics without claims.
	actorID, actorName := projectevents.ActorFromContext(ctx)
	projectevents.Emit(ctx, q, projectevents.Event{
		ProjectID: projectID,
		TenantID:  tenantID,
		Type:      projectevents.TypeAnalysisCreated,
		ActorID:   actorID,
		ActorName: actorName,
		Details:   map[string]any{"analysis_id": created.ID, "name": created.Name, "trigger": "auto"},
	})
}

// PromoteProjectAutoAnalysisIfReady enqueues the project's pending auto analysis
// once the project's pretranslate has settled. Called both from the
// upload handler (covers projects with no pretranslate, ready immediately) and
// from the pipeline worker's terminal completion hooks (covers TM/MT-on-upload
// projects, ready once the last job finishes). Self-gating and idempotent:
//
//   - no pending auto row → nothing to do.
//   - any of the project's jobs still 'created' or pipeline-running → wait; a
//     later completion re-checks. Readiness mirrors the manual analysis gate.
//   - otherwise enqueue. Concurrent promotions are expected: the enqueue's
//     task ID drops a second task while the first is queued, and the worker's
//     transactional claim (Service.Run) makes any task that still gets through
//     a no-op.
//
// Best-effort: failures are logged, not propagated.
func PromoteProjectAutoAnalysisIfReady(ctx context.Context, q db.Querier, enq Enqueuer, tenantID, projectID int64) {
	if q == nil || enq == nil || tenantID == 0 || projectID == 0 {
		return
	}

	a, err := q.GetPendingAutoAnalysis(ctx, projectID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			logging.Warn(ctx, err, "auto-analysis: pending analysis load failed", "auto-analysis-load-pending-failed", logging.ProjectID(projectID))
		}
		return // no pending auto analysis — nothing to promote
	}

	// Load the project's pretranslate flags so readiness matches the manual
	// analysis gate exactly. GetPendingAutoAnalysis gave us the tenant + project.
	project, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			logging.Warn(ctx, err, "auto-analysis: project load failed", "auto-analysis-load-project-failed", logging.ProjectID(projectID))
		}
		return
	}
	settings, err := q.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		logging.Warn(ctx, err, "auto-analysis: settings profile load failed", "auto-analysis-load-settings-failed", logging.ProjectID(projectID))
		return
	}
	pretranslateEnabled := settings.TmOnUpload || settings.MtOnUpload

	jobs, err := q.ListProjectJobsForAutoAnalysis(ctx, projectID)
	if err != nil {
		logging.Warn(ctx, err, "auto-analysis: job list failed", "auto-analysis-list-jobs-failed", logging.ProjectID(projectID))
		return
	}
	if len(jobs) == 0 {
		return
	}

	// Readiness — mirrors internal/analysis/handler.go HandleCreate. When the
	// project runs pretranslate on upload, hold off until every job is past it
	// (not still 'created' waiting for the pipeline, not actively running). On a
	// project with no pretranslate, 'created' jobs are analysis-ready
	// immediately. The last completion to clear this enqueues; earlier ones
	// return here.
	for _, j := range jobs {
		if pretranslateEnabled && (j.Status == "created" || pipeline.IsPipelineRunning(j.Status, j.CurrentStep)) {
			return // still pretranslating — a later completion will re-check
		}
	}

	if err := enq.EnqueueAnalysis(a.ID, tenantID); err != nil {
		// Row stays pending; a later completion (or a manual run) can retry.
		// Don't fail the row — unlike a POST, there's no user waiting on a
		// response, and the pending row is still valid to enqueue later.
		logging.Warn(ctx, err, "auto-analysis: enqueue failed", "auto-analysis-enqueue-failed", logging.ProjectID(projectID), logging.AnalysisID(a.ID))
	}
}
