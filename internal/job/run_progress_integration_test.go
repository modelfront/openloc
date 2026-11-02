package job_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The "Translating N / M" counter reads translated_count / total_count. A new
// run starts without them, so the counter never shows the last run's numbers
// while the new run has not reported its first chunk yet.

// seedFinishedRun leaves the fixture's job as a finished run that reported
// 404 / 404, the state a second Pre-translate starts from.
func seedFinishedRun(t *testing.T, fx stopFixture) {
	t.Helper()
	if _, err := fx.pool.Exec(context.Background(),
		`UPDATE jobs SET status = 'in_progress', current_step = 'Translation complete',
		        translated_count = 404, total_count = 404 WHERE id = $1`, fx.job.ID); err != nil {
		t.Fatalf("seed finished run: %v", err)
	}
}

// progressJSON is the slice of GET /api/jobs/{id} the counter renders.
type progressJSON struct {
	TranslatedCount *int32 `json:"translated_count"`
	TotalCount      *int32 `json:"total_count"`
}

func getProgressJSON(t *testing.T, fx stopFixture) progressJSON {
	t.Helper()
	rec := testutil.DoRequest(t, fx.router, "GET", fmt.Sprintf("/api/jobs/%d", fx.job.ID), nil, fx.user.ID, fx.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var body struct {
		Data progressJSON `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &body)
	return body.Data
}

func assertNoProgress(t *testing.T, fx stopFixture, after string) {
	t.Helper()
	got := getProgressJSON(t, fx)
	if got.TranslatedCount != nil || got.TotalCount != nil {
		t.Errorf("after %s the job reports %v / %v — the last run's counter, shown as the new run's progress",
			after, deref(got.TranslatedCount), deref(got.TotalCount))
	}
}

func deref(p *int32) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestIntegrationStartJobRun_ClearsLastRunProgress(t *testing.T) {
	fx := newStopFixture(t)
	seedFinishedRun(t, fx)
	if _, err := fx.pool.Exec(context.Background(),
		`UPDATE jobs SET status = 'queued', current_step = 'Queued for pre-translation' WHERE id = $1`, fx.job.ID); err != nil {
		t.Fatalf("queue job: %v", err)
	}

	n, err := fx.q.StartJobRun(context.Background(), db.StartJobRunParams{ID: fx.job.ID, CurrentStep: "Running MT"})
	if err != nil || n != 1 {
		t.Fatalf("StartJobRun: rows=%d err=%v", n, err)
	}
	assertNoProgress(t, fx, "StartJobRun")
}

func TestIntegrationClaimJobPipeline_ClearsProgressAndRestorePutsItBack(t *testing.T) {
	fx := newStopFixture(t)
	seedFinishedRun(t, fx)
	ctx := context.Background()

	const claimStep = "Running MT"
	prev, err := fx.q.ClaimJobPipeline(ctx, db.ClaimJobPipelineParams{ID: fx.job.ID, TenantID: fx.tenant.ID, CurrentStep: claimStep})
	if err != nil {
		t.Fatalf("ClaimJobPipeline: %v", err)
	}
	assertNoProgress(t, fx, "ClaimJobPipeline")

	// A pre-flight rejection runs nothing, so the job gets its counter back.
	if err := fx.q.RestoreJobPipelineState(ctx, db.RestoreJobPipelineStateParams{
		ID: fx.job.ID, Status: prev.Status, CurrentStep: prev.CurrentStep, ErrorMessage: prev.ErrorMessage,
		TranslatedCount: prev.TranslatedCount, TotalCount: prev.TotalCount,
		UpdatedAt: prev.UpdatedAt, ClaimStep: claimStep,
	}); err != nil {
		t.Fatalf("RestoreJobPipelineState: %v", err)
	}
	got := getProgressJSON(t, fx)
	if deref(got.TranslatedCount) != int32(404) || deref(got.TotalCount) != int32(404) {
		t.Errorf("after the restore the job reports %v / %v, want 404 / 404", deref(got.TranslatedCount), deref(got.TotalCount))
	}
}

// A queued job shows "Queued for pre-translation", not the last run's counter,
// for as long as it waits for a worker: queueing clears the counts too.
func TestIntegrationQueueingClearsLastRunProgress(t *testing.T) {
	ctx := context.Background()

	t.Run("UpdateJobStatus to queued", func(t *testing.T) {
		fx := newStopFixture(t)
		seedFinishedRun(t, fx)
		if err := fx.q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{
			ID: fx.job.ID, Status: "queued", CurrentStep: "Queued for pre-translation",
		}); err != nil {
			t.Fatalf("UpdateJobStatus: %v", err)
		}
		assertNoProgress(t, fx, "queueing the job")
	})

	t.Run("UpdateJobStatus to another state keeps them", func(t *testing.T) {
		fx := newStopFixture(t)
		seedFinishedRun(t, fx)
		if err := fx.q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{
			ID: fx.job.ID, Status: "in_progress", CurrentStep: "Pipeline complete",
		}); err != nil {
			t.Fatalf("UpdateJobStatus: %v", err)
		}
		got := getProgressJSON(t, fx)
		if deref(got.TranslatedCount) != int32(404) || deref(got.TotalCount) != int32(404) {
			t.Errorf("finishing a phase changed the counts to %v / %v, want 404 / 404", deref(got.TranslatedCount), deref(got.TotalCount))
		}
	})

	t.Run("ClaimFailedJobForRerun", func(t *testing.T) {
		fx := newStopFixture(t)
		if _, err := fx.pool.Exec(ctx,
			`UPDATE jobs SET status = 'failed', current_step = 'MT failed', translated_count = 120, total_count = 404 WHERE id = $1`, fx.job.ID); err != nil {
			t.Fatalf("seed failed run: %v", err)
		}
		n, err := fx.q.ClaimFailedJobForRerun(ctx, db.ClaimFailedJobForRerunParams{ID: fx.job.ID, CurrentStep: "Queued for pre-translation"})
		if err != nil || n != 1 {
			t.Fatalf("ClaimFailedJobForRerun: rows=%d err=%v", n, err)
		}
		assertNoProgress(t, fx, "ClaimFailedJobForRerun")
	})
}
