// Auto-complete rule: a project with the option on flips to
// 'completed' when every one of its jobs is completed, and back to
// 'in_progress' when that stops holding.
//
// The three triggers are exercised through their real HTTP routes:
//   - job completion → POST /api/jobs/{id}/complete (the sole completer)
//   - file deletion  → DELETE /api/projects/{id}/files/{id}
//   - job deletion   → DELETE /api/jobs/{id}
//
// Complete works with no approved segments (nothing to write to TM, the job
// still completes) and SeedJob leaves current_step_id NULL, so the final-step
// gate is skipped — same setup as job/complete_lifecycle_integration_test.go.
package project_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// completeJob drives the real Complete endpoint.
func completeJob(t *testing.T, router http.Handler, jobID, userID, tenantID int64) {
	t.Helper()
	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/complete", jobID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

func deleteFile(t *testing.T, router http.Handler, projectID, fileID, userID, tenantID int64) {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, router, "DELETE",
		fmt.Sprintf("/api/projects/%d/files/%d", projectID, fileID), nil, userID, tenantID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
}

func deleteJob(t *testing.T, router http.Handler, jobID, userID, tenantID int64) {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/jobs/%d", jobID), nil, userID, tenantID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
}

func setProjectField(t *testing.T, router http.Handler, projectID, userID, tenantID int64, body map[string]any) {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", projectID), body, userID, tenantID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// setProjectSettings drives the settings PUT — auto_complete lives on the
// project's settings_profile so templates carry it.
func setProjectSettings(t *testing.T, router http.Handler, projectID, userID, tenantID int64, body map[string]any) {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", projectID), body, userID, tenantID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
}

func assertProjectStatus(t *testing.T, q db.Querier, projectID, tenantID int64, want db.ProjectStatus, when string) {
	t.Helper()
	p, err := q.GetProject(context.Background(), db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("GetProject (%s): %v", when, err)
	}
	if p.Status != want {
		t.Fatalf("project status %s: want %q, got %q", when, want, p.Status)
	}
}

// autoCompleteFixture: one tenant, one owner, one project, and `files` files
// each carrying one job for the project's single target language.
type autoCompleteFixture struct {
	router  http.Handler
	q       db.Querier
	tenant  db.Tenant
	user    db.User
	project db.Project
	files   []db.File
	jobs    []db.Job
}

func newAutoCompleteFixture(t *testing.T, name string, fileCount int) (autoCompleteFixture, func()) {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)

	f := autoCompleteFixture{router: testutil.NewTestRouter(pool), q: q}
	f.tenant = testutil.SeedTenant(t, q, name+"Co")
	f.user = testutil.SeedUserWithRole(t, q, f.tenant.ID, "owner@"+name+".test", "pass", "owner")
	f.project = testutil.SeedProject(t, q, f.tenant.ID, name, "en", []string{"de"})

	for i := 0; i < fileCount; i++ {
		file := testutil.SeedFile(t, q, f.project.ID, fmt.Sprintf("f%d.txt", i), db.FileFormatTxt)
		f.files = append(f.files, file)
		f.jobs = append(f.jobs, testutil.SeedJob(t, q, file.ID, "de"))
	}

	// A freshly seeded project must not already be completed, or every
	// assertion below is vacuous.
	assertProjectStatus(t, q, f.project.ID, f.tenant.ID, db.ProjectStatusInProgress, "at fixture setup")
	return f, cleanup
}

// The regular case: the project completes on the LAST job, not the first.
// Asserting the intermediate state is what stops this passing for the wrong
// reason (e.g. a rule that ignores `done = total`).
func TestIntegrationAutoComplete_CompletesWhenAllJobsDone(t *testing.T) {
	f, cleanup := newAutoCompleteFixture(t, "Regular", 2)
	defer cleanup()

	completeJob(t, f.router, f.jobs[0].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusInProgress, "with one of two jobs done")

	completeJob(t, f.router, f.jobs[1].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusCompleted, "with both jobs done")
}

// Edge case 1: a project with no jobs must never satisfy "all jobs are
// completed" — the vacuous-truth trap. Tested against the query directly
// because an empty project has nothing to trigger a re-evaluation.
//
// RED without the `c.total > 0` guard in ReevaluateProjectAutoComplete.
func TestIntegrationAutoComplete_EmptyProjectNeverCompletes(t *testing.T) {
	f, cleanup := newAutoCompleteFixture(t, "Empty", 0)
	defer cleanup()

	updated, err := f.q.ReevaluateProjectAutoComplete(context.Background(), db.ReevaluateProjectAutoCompleteParams{
		ProjectID: f.project.ID,
		TenantID:  f.tenant.ID,
	})
	if err != nil {
		t.Fatalf("re-evaluating an empty project: %v", err)
	}
	if len(updated) != 0 {
		t.Fatalf("re-evaluating an empty project: want no status change, got %v", updated)
	}
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusInProgress, "for an empty project")
}

// Edge case 1 reached via edge case 2: deleting the last file of a completed
// project empties it, so it no longer qualifies and reopens. Guards against
// both failure modes — staying stuck at 'completed', and staying 'completed'
// vacuously because zero jobs read as "all done".
func TestIntegrationAutoComplete_EmptiedProjectReopens(t *testing.T) {
	f, cleanup := newAutoCompleteFixture(t, "Emptied", 1)
	defer cleanup()

	completeJob(t, f.router, f.jobs[0].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusCompleted, "with its only job done")

	deleteFile(t, f.router, f.project.ID, f.files[0].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusInProgress, "after deleting the only file")
}

// Edge case 2: deleting a file removes its jobs via ON DELETE CASCADE, so the
// project can complete without anything being completed.
//
// RED without the project.Reevaluate call in file.HandleDelete.
func TestIntegrationAutoComplete_FileDeleteCompletesProject(t *testing.T) {
	f, cleanup := newAutoCompleteFixture(t, "FileDelete", 2)
	defer cleanup()

	completeJob(t, f.router, f.jobs[0].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusInProgress, "with file B still pending")

	deleteFile(t, f.router, f.project.ID, f.files[1].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusCompleted, "after deleting the pending file")
}

// Edge case 2, job granularity: one file, two target languages = two tasks.
// Deleting the unfinished one completes the project.
//
// RED without the reevaluateAutoComplete call in job.HandleDeleteJob.
func TestIntegrationAutoComplete_JobDeleteCompletesProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "JobDeleteCo")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@jobdelete.test", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, "JobDelete", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	jobDE := testutil.SeedJob(t, q, file.ID, "de")
	jobFR := testutil.SeedJob(t, q, file.ID, "fr")

	completeJob(t, router, jobDE.ID, user.ID, tenant.ID)
	assertProjectStatus(t, q, project.ID, tenant.ID, db.ProjectStatusInProgress, "with the fr job still pending")

	deleteJob(t, router, jobFR.ID, user.ID, tenant.ID)
	assertProjectStatus(t, q, project.ID, tenant.ID, db.ProjectStatusCompleted, "after deleting the pending job")
}

// Option off → the feature never touches the status, however many jobs finish.
func TestIntegrationAutoComplete_OptionOffLeavesStatusAlone(t *testing.T) {
	f, cleanup := newAutoCompleteFixture(t, "OptionOff", 2)
	defer cleanup()

	setProjectSettings(t, f.router, f.project.ID, f.user.ID, f.tenant.ID, map[string]any{"auto_complete": false})

	completeJob(t, f.router, f.jobs[0].ID, f.user.ID, f.tenant.ID)
	completeJob(t, f.router, f.jobs[1].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusInProgress, "with the option off and every job done")
}

// 'on_hold' is a deliberate park — finishing the work must not silently
// re-label it as delivered.
//
// RED without the `p.status <> 'on_hold'` clause.
func TestIntegrationAutoComplete_OnHoldNotOverwritten(t *testing.T) {
	f, cleanup := newAutoCompleteFixture(t, "OnHold", 1)
	defer cleanup()

	setProjectField(t, f.router, f.project.ID, f.user.ID, f.tenant.ID, map[string]any{"status": "on_hold"})

	completeJob(t, f.router, f.jobs[0].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusOnHold, "on hold with every job done")
}

// The reopen branch only ever overwrites 'completed', so a manually chosen
// status survives a re-evaluation that finds unfinished work.
//
// RED if the reopen branch drops its `p.status = 'completed'` condition.
func TestIntegrationAutoComplete_ManualStatusSurvivesDowngrade(t *testing.T) {
	f, cleanup := newAutoCompleteFixture(t, "ManualStatus", 2)
	defer cleanup()

	setProjectField(t, f.router, f.project.ID, f.user.ID, f.tenant.ID, map[string]any{"status": "on_hold"})

	// Deleting one file leaves the other's job unfinished, so the project does
	// not qualify — and must be left exactly as the user set it.
	deleteFile(t, f.router, f.project.ID, f.files[0].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusOnHold, "after a non-qualifying re-evaluation")
}

// Un-completion is noticed late, not never (known gap).
//
// ClaimJobPipeline is the production mechanism: it does not exclude
// status='completed', so re-running MT/TM/AI on a finished job flips it back to
// in_progress. It's called directly here because driving mt-apply over HTTP
// would need a full MT engine assignment — the query under test is the real
// one, not a stand-in.
//
// Part 1 pins the accepted gap: no trigger fires, so the project keeps reading
// 'completed'. Part 2 covers the `done < total` reopen branch, which nothing
// else reaches.
func TestIntegrationAutoComplete_UnCompletedJobReopensOnNextTrigger(t *testing.T) {
	f, cleanup := newAutoCompleteFixture(t, "SelfHeal", 2)
	defer cleanup()
	ctx := context.Background()

	completeJob(t, f.router, f.jobs[0].ID, f.user.ID, f.tenant.ID)
	completeJob(t, f.router, f.jobs[1].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusCompleted, "with every job done")

	_, err := f.q.ClaimJobPipeline(ctx, db.ClaimJobPipelineParams{
		ID: f.jobs[0].ID, TenantID: f.tenant.ID, CurrentStep: "MT apply",
	})
	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("pre-condition: ClaimJobPipeline refused a completed job — the gap this test pins no longer exists, so both halves need rewriting")
	}
	if err != nil {
		t.Fatalf("ClaimJobPipeline: %v", err)
	}
	job, err := f.q.GetJob(ctx, f.jobs[0].ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != "in_progress" {
		t.Fatalf("pre-condition: job status after claim: want %q, got %q", "in_progress", job.Status)
	}

	// Part 1: the claim itself doesn't re-evaluate.
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusCompleted, "immediately after a job un-completed")

	// Part 2: the next trigger observes the drift and reopens the project.
	deleteJob(t, f.router, f.jobs[1].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusInProgress, "after the next trigger re-evaluated")
}

// The rule is tenant-scoped: another tenant's id must not drive this project's
// status, even with the correct project id.
//
// RED without `p.tenant_id = @tenant_id` in the query.
func TestIntegrationAutoComplete_TenantScoped(t *testing.T) {
	f, cleanup := newAutoCompleteFixture(t, "TenantScope", 1)
	defer cleanup()

	other := testutil.SeedTenant(t, f.q, "OtherCo")

	// Finish the work so tenant A's project genuinely qualifies — the only
	// thing standing between it and 'completed' is the tenant mismatch.
	if err := f.q.UpdateJobStatus(context.Background(), db.UpdateJobStatusParams{
		ID: f.jobs[0].ID, Status: "completed", CurrentStep: "done",
	}); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	updated, err := f.q.ReevaluateProjectAutoComplete(context.Background(), db.ReevaluateProjectAutoCompleteParams{
		ProjectID: f.project.ID,
		TenantID:  other.ID,
	})
	if err != nil {
		t.Fatalf("re-evaluating with the wrong tenant: %v", err)
	}
	if len(updated) != 0 {
		t.Fatalf("re-evaluating with the wrong tenant: want no status change, got %v", updated)
	}
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusInProgress, "after a cross-tenant re-evaluation attempt")

	// Sanity: the correct tenant DOES complete it, proving the ErrNoRows above
	// came from the tenant filter and not from an unqualified project.
	if _, err := f.q.ReevaluateProjectAutoComplete(context.Background(), db.ReevaluateProjectAutoCompleteParams{
		ProjectID: f.project.ID,
		TenantID:  f.tenant.ID,
	}); err != nil {
		t.Fatalf("re-evaluating with the owning tenant: %v", err)
	}
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusCompleted, "after the owning tenant re-evaluated")
}

// The option lives on settings_profiles so templates carry it: a template
// saved from the form with auto-complete OFF, then a project created from that
// template, must arrive OFF. Both directions in one test —
// a setup that drops the value and an apply that ignores it are the same class
// of bug.
//
// RED without the explicit AutoComplete in ApplySetup's UpdateSettingsProfile
// (the template silently takes the column default) or in ApplyTemplate's copy
// (apply leaves the new project's default).
func TestIntegrationAutoComplete_RoundTripsThroughTemplate(t *testing.T) {
	f, cleanup := newAutoCompleteFixture(t, "TemplateRoundTrip", 1)
	defer cleanup()

	rec := testutil.DoRequestWithRole(t, f.router, "POST", "/api/templates",
		map[string]any{"name": "TemplateRoundTrip tmpl", "settings": map[string]any{"auto_complete": false}},
		f.user.ID, f.tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tmplResp struct {
		Data struct {
			ID           int64 `json:"id"`
			AutoComplete bool  `json:"auto_complete"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	if tmplResp.Data.AutoComplete {
		t.Fatalf("template snapshot: want auto_complete=false (the project's value), got true")
	}

	// A project created from that template inherits the OFF choice.
	rec = testutil.DoRequestWithRole(t, f.router, "POST", "/api/projects", map[string]any{
		"name": "From TemplateRoundTrip", "source_language": "en", "target_languages": []string{"de"},
		"template_id": tmplResp.Data.ID,
	}, f.user.ID, f.tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var projResp struct {
		Data struct{ ID int64 } `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)

	newProject, err := f.q.GetProject(context.Background(), db.GetProjectParams{
		ID: projResp.Data.ID, TenantID: f.tenant.ID,
	})
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	profile, err := f.q.GetSettingsProfile(context.Background(), newProject.SettingsProfileID)
	if err != nil {
		t.Fatalf("GetSettingsProfile: %v", err)
	}
	if profile.AutoComplete {
		t.Fatalf("project created from template: want auto_complete=false, got true")
	}
}

// Auto-completion lands in project history, attributed to whoever finished the
// last task, so a PM can see why the status moved.
func TestIntegrationAutoComplete_EmitsHistoryEvent(t *testing.T) {
	f, cleanup := newAutoCompleteFixture(t, "History", 1)
	defer cleanup()

	completeJob(t, f.router, f.jobs[0].ID, f.user.ID, f.tenant.ID)
	assertProjectStatus(t, f.q, f.project.ID, f.tenant.ID, db.ProjectStatusCompleted, "with its only job done")

	events, err := f.q.ListProjectEvents(context.Background(), db.ListProjectEventsParams{
		ProjectID: f.project.ID, TenantID: f.tenant.ID, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListProjectEvents: %v", err)
	}
	var found int
	for _, ev := range events {
		if ev.EventType == "project.auto_completed" {
			found++
			if ev.ActorID == nil || *ev.ActorID != f.user.ID {
				t.Errorf("project.auto_completed actor: want %d, got %v", f.user.ID, ev.ActorID)
			}
		}
	}
	if found != 1 {
		t.Fatalf("project.auto_completed events: want 1, got %d", found)
	}
}
