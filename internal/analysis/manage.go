package analysis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
)

// Saved-analysis create / read / delete, shared by the REST handlers and the
// MCP tools. Project and analysis lookups are tenant-scoped; a foreign or
// missing id comes back as pgx.ErrNoRows.

// maxJobsPerAnalysis is a sanity ceiling well past any realistic project; it
// catches accidental "send everything" client bugs.
const maxJobsPerAnalysis = 10_000

// InputError is a caller mistake. Its message is written for the caller and is
// safe to show.
type InputError struct{ Msg string }

func (e InputError) Error() string { return e.Msg }

// ErrEnqueue means the analysis row exists but its worker task could not be
// dispatched; the row has been marked failed.
var ErrEnqueue = errors.New("analysis could not be queued")

// CreateInput is what a new saved analysis needs.
type CreateInput struct {
	ProjectID int64
	Name      string // optional; defaults to "Analysis <timestamp>"
	JobIDs    []int64
}

// Create validates the request, inserts a pending analysis and enqueues its
// worker. enq may be nil: the row then stays pending with no worker.
//
// Gate order: tenant-scoped project, non-empty and bounded job_ids, every job
// in this project, pretranslate finished for every job (only when the
// project's settings run TM or MT on upload).
func Create(ctx context.Context, q db.Querier, enq Enqueuer, tenantID, userID int64, in CreateInput) (db.Analysis, error) {
	project, err := q.GetProject(ctx, db.GetProjectParams{ID: in.ProjectID, TenantID: tenantID})
	if err != nil {
		return db.Analysis{}, err
	}
	if len(in.JobIDs) == 0 {
		return db.Analysis{}, InputError{"job_ids must be non-empty"}
	}
	if len(in.JobIDs) > maxJobsPerAnalysis {
		return db.Analysis{}, InputError{fmt.Sprintf("too many jobs in one analysis (max %d)", maxJobsPerAnalysis)}
	}

	// The query filters by project_id, so cross-project ids fall out and show
	// up as a length mismatch.
	rows, err := q.ListJobsByIDsForProject(ctx, db.ListJobsByIDsForProjectParams{
		JobIds:    in.JobIDs,
		ProjectID: in.ProjectID,
	})
	if err != nil {
		return db.Analysis{}, fmt.Errorf("validate jobs: %w", err)
	}
	if len(rows) != uniqueIntCount(in.JobIDs) {
		return db.Analysis{}, InputError{fmt.Sprintf("job_ids do not belong to this project: %v", findMissing(in.JobIDs, rows))}
	}

	settings, err := q.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		return db.Analysis{}, fmt.Errorf("load project settings: %w", err)
	}
	if settings.TmOnUpload || settings.MtOnUpload {
		var blocked []string
		for _, j := range rows {
			if j.Status == "created" || pipeline.IsPipelineRunning(j.Status, j.CurrentStep) {
				blocked = append(blocked, fmt.Sprintf("%s (status=%s)", j.FileName, j.Status))
			}
		}
		if len(blocked) > 0 {
			return db.Analysis{}, InputError{"pretranslate has not completed for: " + strings.Join(blocked, ", ") + ". Run Apply TM/MT first."}
		}
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Analysis " + time.Now().UTC().Format("2006-01-02 15:04")
	}
	var createdBy *int64
	if userID > 0 {
		createdBy = &userID
	}
	a, err := q.InsertAnalysis(ctx, db.InsertAnalysisParams{
		TenantID:         tenantID,
		ProjectID:        in.ProjectID,
		Name:             name,
		SourceLanguage:   project.SourceLanguage,
		JobIds:           in.JobIDs,
		CreatedBy:        createdBy,
		SettingsSnapshot: []byte(`{}`),
	})
	if err != nil {
		return db.Analysis{}, fmt.Errorf("insert analysis: %w", err)
	}

	actorID, actorName := projectevents.ActorFromContext(ctx)
	projectevents.Emit(ctx, q, projectevents.Event{
		ProjectID: in.ProjectID,
		TenantID:  tenantID,
		Type:      projectevents.TypeAnalysisCreated,
		ActorID:   actorID,
		ActorName: actorName,
		Details:   map[string]any{"analysis_id": a.ID, "name": a.Name, "job_count": len(in.JobIDs)},
	})

	if enq != nil {
		if err := enq.EnqueueAnalysis(a.ID, tenantID); err != nil {
			logging.Error(ctx, err, "analysis dispatch failed", "analysis.enqueue_failed", logging.AnalysisID(a.ID))
			// If recording the failure also fails, the analysis shows as
			// running forever with no other path left to surface it.
			if err := markFailed(ctx, q, a.ID); err != nil {
				errtrack.Capture(ctx, err, errtrack.CaptureOptions{
					Class:     errtrack.ClassStateWriteFailed,
					Message:   "could not mark analysis failed — it will display as running forever",
					Subsystem: "analysis",
					Event:     "mark-analysis-failed",
					Entity:    errtrack.EntityID(a.ID),
				})
			}
			return db.Analysis{}, ErrEnqueue
		}
	}
	return a, nil
}

// List returns the project's live saved analyses, newest first.
func List(ctx context.Context, q db.Querier, tenantID, projectID int64) ([]db.Analysis, error) {
	if _, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID}); err != nil {
		return nil, err
	}
	rows, err := q.ListAnalysesByProject(ctx, db.ListAnalysesByProjectParams{ProjectID: projectID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("list analyses: %w", err)
	}
	return rows, nil
}

// Get returns one live analysis with its per-job breakdown rows.
func Get(ctx context.Context, q db.Querier, tenantID, id int64) (db.Analysis, []db.JobAnalysis, error) {
	a, err := q.GetAnalysis(ctx, db.GetAnalysisParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.Analysis{}, nil, err
	}
	breakdowns, err := q.ListJobAnalysisByAnalysis(ctx, id)
	if err != nil {
		return db.Analysis{}, nil, fmt.Errorf("load analysis breakdowns: %w", err)
	}
	return a, breakdowns, nil
}

// Delete soft-deletes a live analysis and records the project event. Returns
// pgx.ErrNoRows when there is no live analysis with that id in the tenant.
func Delete(ctx context.Context, q db.Querier, tenantID, id int64) error {
	old, err := q.GetAnalysis(ctx, db.GetAnalysisParams{ID: id, TenantID: tenantID})
	if err != nil {
		return err
	}
	n, err := q.DeleteAnalysis(ctx, db.DeleteAnalysisParams{ID: id, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("delete analysis: %w", err)
	}
	if n == 0 {
		return pgx.ErrNoRows
	}
	actorID, actorName := projectevents.ActorFromContext(ctx)
	projectevents.Emit(ctx, q, projectevents.Event{
		ProjectID: old.ProjectID,
		TenantID:  tenantID,
		Type:      projectevents.TypeAnalysisDeleted,
		ActorID:   actorID,
		ActorName: actorName,
		Details:   map[string]any{"analysis_id": old.ID, "name": old.Name},
	})
	return nil
}
