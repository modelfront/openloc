package job_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type stopFixture struct {
	pool    *pgxpool.Pool
	q       db.Querier
	router  http.Handler
	tenant  db.Tenant
	user    db.User
	project db.Project
	file    db.File
	job     db.Job
}

// newStopFixture seeds one job whose pipeline is mid-run (in_progress with a
// live progress label — the predicate RequestJobStop and the FE agree on).
func newStopFixture(t *testing.T) stopFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "StopCo-"+t.Name())
	user := testutil.SeedUser(t, q, tenant.ID, "u@stop.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Stop Project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	if err := q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: job.ID, Status: "in_progress", CurrentStep: "MT: 30 / 100 segments"}); err != nil {
		t.Fatalf("seed running job: %v", err)
	}
	return stopFixture{pool: pool, q: q, router: testutil.NewTestRouter(pool), tenant: tenant, user: user, project: project, file: file, job: job}
}

func (fx stopFixture) stop(t *testing.T, jobID int64) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequest(t, fx.router, "POST", fmt.Sprintf("/api/jobs/%d/stop", jobID), nil, fx.user.ID, fx.tenant.ID)
}

// jobJSON is the slice of the job payload the Stop UI reads. Asserting it on
// the wire (not just in the DB) is what catches a JobView that stops
// serializing stop_requested_at: every Stop affordance — the "Stopping…" badge,
// canStopJob, whether the bulk Stop button renders — keys on that field.
type jobJSON struct {
	ID              int64   `json:"id"`
	PipelineStatus  string  `json:"pipeline_status"`
	CurrentStep     string  `json:"current_step"`
	StopRequestedAt *string `json:"stop_requested_at"`
}

// getJobJSON reads the job through GET /api/jobs/{id} (the CAT editor's source).
func (fx stopFixture) getJobJSON(t *testing.T, jobID int64) jobJSON {
	t.Helper()
	rec := testutil.DoRequest(t, fx.router, "GET", fmt.Sprintf("/api/jobs/%d", jobID), nil, fx.user.ID, fx.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var body struct {
		Data jobJSON `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &body)
	return body.Data
}

// listJobJSON reads the same job through GET /api/projects/{id}/jobs (the
// project page's source) — a separate JobView construction site.
func (fx stopFixture) listJobJSON(t *testing.T, jobID int64) jobJSON {
	t.Helper()
	rec := testutil.DoRequest(t, fx.router, "GET", fmt.Sprintf("/api/projects/%d/jobs", fx.project.ID), nil, fx.user.ID, fx.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var body struct {
		Data []jobJSON `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &body)
	for _, j := range body.Data {
		if j.ID == jobID {
			return j
		}
	}
	t.Fatalf("job %d missing from the project jobs list", jobID)
	return jobJSON{}
}

func TestIntegrationStopJob_RunningReturns202AndSetsFlag(t *testing.T) {
	fx := newStopFixture(t)

	rec := fx.stop(t, fx.job.ID)
	testutil.AssertStatus(t, rec, http.StatusAccepted)
	var body struct {
		Status string `json:"status"`
		JobID  int64  `json:"job_id"`
	}
	testutil.DecodeJSON(t, rec, &body)
	if body.Status != "stopping" || body.JobID != fx.job.ID {
		t.Errorf("body = %+v, want status=stopping job_id=%d", body, fx.job.ID)
	}

	post, err := fx.q.GetJob(context.Background(), fx.job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if !post.StopRequestedAt.Valid {
		t.Error("stop_requested_at must be set while the run is still finishing its chunk")
	}
	if post.Status != "in_progress" {
		t.Errorf("status = %q; the request only flags the run, the worker ends it", post.Status)
	}

	// Both JobView construction sites must carry the flag, or the UI shows
	// a Stop button that never turns into "Stopping…".
	for name, got := range map[string]jobJSON{"GET /jobs/{id}": fx.getJobJSON(t, fx.job.ID), "GET /projects/{id}/jobs": fx.listJobJSON(t, fx.job.ID)} {
		if got.StopRequestedAt == nil || *got.StopRequestedAt == "" {
			t.Errorf("%s: stop_requested_at = %v, want a timestamp while the stop is pending", name, got.StopRequestedAt)
		}
		if got.PipelineStatus != "in_progress" {
			t.Errorf("%s: pipeline_status = %q, want in_progress", name, got.PipelineStatus)
		}
	}
}

func TestIntegrationStopJob_IdleReturns409(t *testing.T) {
	fx := newStopFixture(t)
	idle := testutil.SeedJob(t, fx.q, fx.file.ID, "fr")

	rec := fx.stop(t, idle.ID)
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "no pipeline run") {
		t.Errorf("409 body should say nothing is running, got %s", rec.Body.String())
	}
}

// A finished phase ("… complete") is not running either.
func TestIntegrationStopJob_FinishedPhaseReturns409(t *testing.T) {
	fx := newStopFixture(t)
	if err := fx.q.UpdateJobStatus(context.Background(), db.UpdateJobStatusParams{ID: fx.job.ID, Status: "in_progress", CurrentStep: "Pipeline complete"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	testutil.AssertStatus(t, fx.stop(t, fx.job.ID), http.StatusConflict)
}

func TestIntegrationStopJob_AlreadyStoppingReturns409(t *testing.T) {
	fx := newStopFixture(t)
	testutil.AssertStatus(t, fx.stop(t, fx.job.ID), http.StatusAccepted)
	testutil.AssertStatus(t, fx.stop(t, fx.job.ID), http.StatusConflict)
}

func TestIntegrationStopJob_CrossTenant404(t *testing.T) {
	fx := newStopFixture(t)
	other := testutil.SeedTenant(t, fx.q, "OtherStopCo")
	intruder := testutil.SeedUser(t, fx.q, other.ID, "x@other.test", "pass")

	rec := testutil.DoRequest(t, fx.router, "POST", fmt.Sprintf("/api/jobs/%d/stop", fx.job.ID), nil, intruder.ID, other.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	post, err := fx.q.GetJob(context.Background(), fx.job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if post.StopRequestedAt.Valid {
		t.Error("a cross-tenant request must not flag the job")
	}
}

// Stop shares Apply TM/MT's gate: a linguist assigned to the job may stop it;
// an unassigned linguist may not.
func TestIntegrationStopJob_LinguistAccess(t *testing.T) {
	fx := newStopFixture(t)
	assigned := testutil.SeedUserWithRole(t, fx.q, fx.tenant.ID, "ling@stop.test", "pass", "linguist")
	testutil.SeedJobStep(t, fx.q, fx.project.SettingsProfileID, fx.job.ID, &assigned.ID)
	stranger := testutil.SeedUserWithRole(t, fx.q, fx.tenant.ID, "other@stop.test", "pass", "linguist")

	rec := testutil.DoRequestWithRole(t, fx.router, "POST", fmt.Sprintf("/api/jobs/%d/stop", fx.job.ID), nil, stranger.ID, fx.tenant.ID, "linguist")
	// The exact status matters: RequireLinguistJobAccess answers 403 here (the
	// tenant-opaque 404 belongs to the earlier ownership gate). A bare
	// "not 202" would also pass for a 500 from a broken lookup.
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	blocked, err := fx.q.GetJob(context.Background(), fx.job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if blocked.StopRequestedAt.Valid {
		t.Error("a rejected request must not flag the job")
	}

	rec = testutil.DoRequestWithRole(t, fx.router, "POST", fmt.Sprintf("/api/jobs/%d/stop", fx.job.ID), nil, assigned.ID, fx.tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusAccepted)
}

// A run that stopped touching its row (hard-killed pod) has nobody left to
// honour the flag, so the request closes the job out immediately.
func TestIntegrationStopJob_StaleRunMarkedStoppedImmediately(t *testing.T) {
	fx := newStopFixture(t)
	ctx := context.Background()
	if _, err := fx.pool.Exec(ctx, "UPDATE jobs SET updated_at = now() - interval '31 minutes' WHERE id = $1", fx.job.ID); err != nil {
		t.Fatalf("age the run: %v", err)
	}

	testutil.AssertStatus(t, fx.stop(t, fx.job.ID), http.StatusAccepted)

	post, err := fx.q.GetJob(ctx, fx.job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if post.Status != "stopped" {
		t.Errorf("status = %q, want stopped (no live run will consume the flag)", post.Status)
	}
	if post.StopRequestedAt.Valid {
		t.Error("stop_requested_at must be cleared once the job is stopped")
	}
	if !strings.HasPrefix(post.CurrentStep, "Stopped") {
		t.Errorf("current_step = %q, want a Stopped label", post.CurrentStep)
	}
	if got := fx.getJobJSON(t, fx.job.ID); got.StopRequestedAt != nil {
		t.Errorf("stop_requested_at = %v on the wire, want absent once the job is stopped", *got.StopRequestedAt)
	}
}

func TestIntegrationStopJob_EmitsStopRequestedEvent(t *testing.T) {
	fx := newStopFixture(t)
	testutil.AssertStatus(t, fx.stop(t, fx.job.ID), http.StatusAccepted)

	rows, err := fx.q.ListProjectEvents(context.Background(), db.ListProjectEventsParams{ProjectID: fx.project.ID, TenantID: fx.tenant.ID, Limit: 50})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.EventType != "job.stop_requested" {
			continue
		}
		found = true
		if r.ActorID == nil || *r.ActorID != fx.user.ID {
			t.Errorf("stop request must be attributed to the requester, got actor %v", r.ActorID)
		}
	}
	if !found {
		t.Error("expected a job.stop_requested event")
	}
}

// The staleness verdict and the flag are decided by one statement, so a run
// that is genuinely translating can never be closed out by a stop request: a
// concurrent Apply TM/MT claim either wins the row first (and the request then
// sees a fresh row) or is locked out afterwards. This pins the second half —
// once a stop is flagged, ClaimJobPipeline's own 30-minute staleness escape can
// no longer reclaim the job, so nothing can start a live run behind the
// handler's close-out.
func TestIntegrationStopJob_FlaggedStaleRunIsClaimProof(t *testing.T) {
	fx := newStopFixture(t)
	ctx := context.Background()
	if _, err := fx.pool.Exec(ctx, "UPDATE jobs SET updated_at = now() - interval '31 minutes' WHERE id = $1", fx.job.ID); err != nil {
		t.Fatalf("age the run: %v", err)
	}

	// Before the request the escape is open — that is what makes the race real.
	stale, err := fx.q.RequestJobStop(ctx, fx.job.ID)
	if err != nil {
		t.Fatalf("RequestJobStop: %v", err)
	}
	if !stale {
		t.Fatal("a run untouched for 31 minutes must be reported stale")
	}

	_, err = fx.q.ClaimJobPipeline(ctx, db.ClaimJobPipelineParams{ID: fx.job.ID, TenantID: fx.tenant.ID, CurrentStep: "Running MT"})
	if err == nil {
		t.Error("a flagged job must be out of reach of the claim's staleness escape — otherwise a live run could start behind the close-out")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("ClaimJobPipeline: %v", err)
	}
}

// A live run (its progress writer keeps updated_at fresh) is never reported
// stale, so the handler leaves it to end itself at the next chunk boundary.
func TestIntegrationStopJob_LiveRunIsNotReportedStale(t *testing.T) {
	fx := newStopFixture(t)

	stale, err := fx.q.RequestJobStop(context.Background(), fx.job.ID)
	if err != nil {
		t.Fatalf("RequestJobStop: %v", err)
	}
	if stale {
		t.Error("a run that touched its row moments ago must not be reported stale")
	}
}

// Every start-or-end transition clears a pending stop. Without that, a request
// landing as a run finishes leaves the job showing "Stopping…" forever and the
// next worker run refuses at pickup, because StartJobRun deliberately does NOT
// clear the flag (that omission is what honours a stop requested while queued).
// The flag is set with raw SQL rather than through RequestJobStop: what is
// pinned here is that these three statements clear it, not how it was set.
func TestIntegrationStopFlagClearedByJobTransitions(t *testing.T) {
	fx := newStopFixture(t)
	ctx := context.Background()

	setFlag := func(t *testing.T) {
		t.Helper()
		if _, err := fx.pool.Exec(ctx, "UPDATE jobs SET stop_requested_at = now() WHERE id = $1", fx.job.ID); err != nil {
			t.Fatalf("set stop flag: %v", err)
		}
	}
	assertCleared := func(t *testing.T, statement string) {
		t.Helper()
		post, err := fx.q.GetJob(ctx, fx.job.ID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if post.StopRequestedAt.Valid {
			t.Errorf("%s left stop_requested_at set — the job would stay stuck on \"Stopping…\" and the next run would refuse at pickup", statement)
		}
	}

	t.Run("UpdateJobStatus", func(t *testing.T) {
		setFlag(t)
		if err := fx.q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{
			ID: fx.job.ID, Status: "in_progress", CurrentStep: "Pipeline complete",
		}); err != nil {
			t.Fatalf("UpdateJobStatus: %v", err)
		}
		assertCleared(t, "UpdateJobStatus")
	})

	t.Run("UpdateJobError", func(t *testing.T) {
		setFlag(t)
		msg := "engine unreachable"
		if err := fx.q.UpdateJobError(ctx, db.UpdateJobErrorParams{ID: fx.job.ID, ErrorMessage: &msg}); err != nil {
			t.Fatalf("UpdateJobError: %v", err)
		}
		assertCleared(t, "UpdateJobError")
	})

	t.Run("ClaimJobPipeline", func(t *testing.T) {
		// A claim only fires on a job that is not already running, so the
		// previous subtest's terminal state is the precondition it needs.
		setFlag(t)
		if _, err := fx.q.ClaimJobPipeline(ctx, db.ClaimJobPipelineParams{ID: fx.job.ID, TenantID: fx.tenant.ID, CurrentStep: "Running MT"}); err != nil {
			t.Fatalf("ClaimJobPipeline: %v — the subtest needs a claimable job to exercise the clear", err)
		}
		assertCleared(t, "ClaimJobPipeline")
	})
}
