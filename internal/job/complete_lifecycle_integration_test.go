package job_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// HandleComplete's lifecycle contract: a successful call must
// transition the job to "completed". This is the *only* place that flips
// status to completed — every other handler (TM/MT/predict apply,
// pre-translate worker) leaves the job in "in_progress" until the user
// hits Complete.
//
// Flow pinned: phases → in_progress → user confirms segments → user
// clicks Complete → THIS handler → completed. If any phase marked the job
// completed, CompleteJob's precondition check would 409 "job is already
// completed" on the Complete click.
//
// Anti-tautology RED proof: delete the `s.setJobStatus(job.ID,
// "completed", "completed")` line in CompleteJob. The job
// stays at whatever status it was before (created/in_progress), and the
// "completed" assertion below flips to fail.
func TestIntegrationComplete_MarksJobCompleted(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "CompleteLifecycleCo")
	// With 0 approved segments completion writes nothing to TM but still
	// makes the status transition, which is what we're pinning here.
	user := testutil.SeedUser(t, q, tenant.ID, "u@complete.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Complete Lifecycle", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	pre, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("pre-condition GetJob: %v", err)
	}
	if string(pre.Status) == "completed" {
		t.Fatalf("pre-condition: SeedJob already returned a completed job — test is tautological")
	}

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/complete", job.ID),
		nil, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	post, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("post-condition GetJob: %v", err)
	}
	if string(post.Status) != "completed" {
		t.Fatalf("job status after Complete: want %q (Complete is the sole completer), got %q",
			"completed", post.Status)
	}

	// Audit: completing the job must land in the project
	// history, attributed to the completing user. RED without the
	// emitJobEvent call in CompleteJob.
	events, err := q.ListProjectEvents(ctx, db.ListProjectEventsParams{
		ProjectID: project.ID, TenantID: tenant.ID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("list project events: %v", err)
	}
	var completed int
	for _, e := range events {
		if e.EventType == "job.completed" {
			completed++
			if e.ActorID == nil || *e.ActorID != user.ID {
				t.Errorf("job.completed must be attributed to the completing user %d, got %v", user.ID, e.ActorID)
			}
		}
	}
	if completed != 1 {
		t.Fatalf("expected exactly 1 job.completed event, got %d", completed)
	}
}

// Idempotency guard: clicking Complete twice in a row must return 409 on
// the second click. CompleteJob's precondition check 409s only when the
// job is actually completed already.
func TestIntegrationComplete_AlreadyCompleted_Returns409(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "CompleteAlreadyDoneCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@complete-done.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Complete Already Done", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// First click — should succeed and flip status to completed.
	rec1 := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/complete", job.ID),
		nil, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec1, http.StatusOK)

	// Second click — must 409 with the "already completed" message.
	rec2 := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/complete", job.ID),
		nil, user.ID, tenant.ID,
	)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409 on double-Complete; got %d (body: %s)", rec2.Code, rec2.Body.String())
	}
	body := strings.ToLower(rec2.Body.String())
	if !strings.Contains(body, "already completed") {
		t.Errorf("expected 'already completed' in error; got: %s", rec2.Body.String())
	}
}

// Sister contract test for the TM/MT apply paths: a successful
// TM-apply must leave the job in "in_progress", NOT "completed". Sets up
// a TM with a read assignment so the phase actually runs (avoids the
// no-assignment 400) but matches no entries (zero-segment file is fine —
// the runPhases handler still reaches the final setJobStatus call).
//
// Anti-tautology RED proof: revert the `setJobStatus(..., "in_progress",
// label+" complete")` line in ApplyPhases (service_runs.go) to "completed".
// The "in_progress" assertion below flips to fail.
func TestIntegrationTMApply_LeavesJobInProgress(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TMApplyLifecycleCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@tm-life.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "TM Apply Lifecycle", "en", []string{"de"})
	tm := testutil.SeedTM(t, q, tenant.ID, "Lifecycle TM")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tm.ID,
		Mode:              "read_write",
		Priority:          1,
	}); err != nil {
		t.Fatalf("seed TM assignment: %v", err)
	}
	file := testutil.SeedFile(t, q, project.ID, "f.json", db.FileFormatJsonI18n)
	job := testutil.SeedJob(t, q, file.ID, "de")

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/tm-apply", job.ID),
		nil, user.ID, tenant.ID,
	)
	// Skip-no-segments is fine — the handler still hit setJobStatus on the
	// way out, which is what we're observing.
	if rec.Code != http.StatusOK {
		t.Fatalf("tm-apply: want 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	post, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("post-condition GetJob: %v", err)
	}
	// Only /complete completes a job.
	if string(post.Status) != "in_progress" || post.CurrentStep != "tm-apply complete" {
		t.Fatalf("job after tm-apply: status=%q current_step=%q, want in_progress / %q",
			post.Status, post.CurrentStep, "tm-apply complete")
	}
}
