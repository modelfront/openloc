package scopecomplete_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/scopecomplete"
	"openloc/internal/testutil"
)

// fixture: a German project with files A, B and C, each one approved segment
// and a single step in progress. The linguist is assigned A and B; C is the
// owner's.
type fixture struct {
	pool     *pgxpool.Pool
	q        db.Querier
	router   http.Handler
	ownerID  int64
	tenant   int64
	linguist db.User
	project  db.Project
	jobs     map[string]db.Job
	tr       map[string]db.Translation
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	f := &fixture{pool: pool, q: q, router: testutil.NewTestRouter(pool), jobs: map[string]db.Job{}, tr: map[string]db.Translation{}}
	f.ownerID, f.tenant = testutil.RegisterUser(t, q, "scope-complete")
	f.linguist = testutil.SeedUserWithRole(t, q, f.tenant, "scope-complete-ling@test.com", "pw", "linguist")
	f.project = testutil.SeedProject(t, q, f.tenant, "Scope complete", "en", []string{"de"})
	for _, name := range []string{"A", "B", "C"} {
		file := testutil.SeedFile(t, q, f.project.ID, name, db.FileFormatJsonI18n)
		f.jobs[name] = testutil.SeedJob(t, q, file.ID, "de")
		s := testutil.SeedSegment(t, q, file.ID, "Hello "+name, 0)
		f.tr[name] = testutil.SeedTranslation(t, q, s.ID, "de", db.TranslationStatusNeedsReview)
		f.exec(t, `UPDATE translations SET active_text = 'Hallo', status = 'approved' WHERE id = $1`, f.tr[name].ID)
		assignee := &f.linguist.ID
		if name == "C" {
			assignee = &f.ownerID
		}
		testutil.SeedJobStep(t, q, f.project.SettingsProfileID, f.jobs[name].ID, assignee)
	}
	return f
}

func (f *fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(testutil.Ctx(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *fixture) complete(t *testing.T, userID int64, role string, body map[string]any, names ...string) []scopecomplete.JobResult {
	t.Helper()
	ids := make([]string, len(names))
	for i, n := range names {
		ids[i] = fmt.Sprint(f.jobs[n].ID)
	}
	path := fmt.Sprintf("/api/projects/%d/editor/complete?jobs=%s", f.project.ID, strings.Join(ids, ","))
	rec := testutil.DoRequestWithRole(t, f.router, http.MethodPost, path, body, userID, f.tenant, role)
	testutil.AssertStatus(t, rec, http.StatusOK)
	return testutil.DecodeData[[]scopecomplete.JobResult](t, rec)
}

func (f *fixture) jobStatus(t *testing.T, name string) (job, step string) {
	t.Helper()
	if err := f.pool.QueryRow(testutil.Ctx(),
		`SELECT j.status, jsa.status FROM jobs j JOIN job_step_assignments jsa ON jsa.job_id = j.id WHERE j.id = $1`,
		f.jobs[name].ID).Scan(&job, &step); err != nil {
		t.Fatal(err)
	}
	return job, step
}

func TestIntegrationScopeComplete_CompletesEachFile(t *testing.T) {
	f := newFixture(t)
	res := f.complete(t, f.linguist.ID, "linguist", map[string]any{"comment": "All done"}, "A", "B")
	if len(res) != 2 || res[0].JobID != f.jobs["A"].ID || res[1].JobID != f.jobs["B"].ID {
		t.Fatalf("results = %+v, want A then B", res)
	}
	for i, name := range []string{"A", "B"} {
		if res[i].Outcome != scopecomplete.OutcomeCompleted {
			t.Errorf("%s: %+v, want completed", name, res[i])
		}
		if job, step := f.jobStatus(t, name); job != "completed" || step != "completed" {
			t.Errorf("%s: job %s, step %s; want both completed", name, job, step)
		}
	}
}

// One file the caller may not complete fails alone; the others complete.
func TestIntegrationScopeComplete_OneRefusalLeavesTheOthersDone(t *testing.T) {
	f := newFixture(t)
	// B's step in progress is the owner's; the linguist has its next step, so
	// may open B but not complete the current one.
	f.exec(t, `UPDATE job_step_assignments SET assigned_to = $1 WHERE job_id = $2`, f.ownerID, f.jobs["B"].ID)
	review, err := f.q.CreateSettingsWorkflowStep(testutil.Ctx(), db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: f.project.SettingsProfileID, Name: "review", StepOrder: 1, RoleRequired: "linguist",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.CreateJobStepAssignment(testutil.Ctx(), db.CreateJobStepAssignmentParams{
		JobID: f.jobs["B"].ID, WorkflowStepID: review.ID, AssignedTo: &f.linguist.ID, Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	res := f.complete(t, f.linguist.ID, "linguist", nil, "A", "B")
	if res[0].Outcome != scopecomplete.OutcomeCompleted {
		t.Errorf("A: %+v, want completed", res[0])
	}
	if res[1].Outcome != scopecomplete.OutcomeFailed || res[1].Error == nil {
		t.Errorf("B: %+v, want failed with a reason", res[1])
	}
	if job, _ := f.jobStatus(t, "A"); job != "completed" {
		t.Errorf("A's job = %s, want completed despite B", job)
	}
	var open string
	if err := f.pool.QueryRow(testutil.Ctx(),
		`SELECT assigned_to::text FROM job_step_assignments WHERE job_id = $1 AND status = 'in_progress'`, f.jobs["B"].ID).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != fmt.Sprint(f.ownerID) {
		t.Errorf("B's step in progress belongs to %s, want the owner's step still in progress", open)
	}
}

// Approve all: a row another user holds keeps that file's step open and is
// reported; the other file completes.
func TestIntegrationScopeComplete_ApproveAllStopsOnAHeldRow(t *testing.T) {
	f := newFixture(t)
	f.exec(t, `UPDATE translations SET status = 'needs_review' WHERE id IN ($1, $2)`, f.tr["A"].ID, f.tr["B"].ID)
	testutil.SeedHold(t, f.q, f.tenant, f.jobs["A"].ID, f.tr["A"].ID, f.ownerID)

	res := f.complete(t, f.linguist.ID, "linguist", map[string]any{"approve_all": true}, "A", "B")
	if res[0].Outcome != scopecomplete.OutcomeBlocked || len(res[0].Blocked) != 1 || res[0].Blocked[0].ID != f.tr["A"].ID {
		t.Errorf("A: %+v, want blocked on its held row", res[0])
	}
	if _, step := f.jobStatus(t, "A"); step != "in_progress" {
		t.Errorf("A's step = %s, want still in progress", step)
	}
	if res[1].Outcome != scopecomplete.OutcomeCompleted {
		t.Errorf("B: %+v, want approved and completed", res[1])
	}
	var status string
	if err := f.pool.QueryRow(testutil.Ctx(), `SELECT status FROM translations WHERE id = $1`, f.tr["B"].ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "approved" {
		t.Errorf("B's row = %s, want approved", status)
	}
}

func TestIntegrationScopeComplete_OnlyJobsOfTheScope(t *testing.T) {
	f := newFixture(t)
	path := fmt.Sprintf("/api/projects/%d/editor/complete?jobs=%d", f.project.ID, f.jobs["C"].ID)
	rec := testutil.DoRequest(t, f.router, http.MethodPost, path, map[string]any{"job_ids": []int64{f.jobs["A"].ID}}, f.ownerID, f.tenant)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if job, _ := f.jobStatus(t, "A"); job == "completed" {
		t.Errorf("A completed through a scope it is not in")
	}

	otherUser, otherTenant := testutil.RegisterUser(t, f.q, "scope-complete-other")
	rec = testutil.DoRequest(t, f.router, http.MethodPost, path, nil, otherUser, otherTenant)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	if job, _ := f.jobStatus(t, "C"); job == "completed" {
		t.Errorf("another tenant completed C")
	}
}

// The same request twice never advances a job twice: the second names the
// step the job has already left, so it is refused for that job.
func TestIntegrationScopeComplete_RepeatAdvancesOnce(t *testing.T) {
	f := newFixture(t)
	review, err := f.q.CreateSettingsWorkflowStep(testutil.Ctx(), db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: f.project.SettingsProfileID, Name: "review", StepOrder: 1, RoleRequired: "linguist",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.CreateJobStepAssignment(testutil.Ctx(), db.CreateJobStepAssignmentParams{
		JobID: f.jobs["A"].ID, WorkflowStepID: review.ID, AssignedTo: &f.linguist.ID, Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	var first int64
	if err := f.pool.QueryRow(testutil.Ctx(), `SELECT current_step_id FROM jobs WHERE id = $1`, f.jobs["A"].ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"steps": map[string]int64{fmt.Sprint(f.jobs["A"].ID): first}}

	res := f.complete(t, f.linguist.ID, "linguist", body, "A")
	if res[0].Outcome != scopecomplete.OutcomeAdvanced {
		t.Fatalf("first: %+v, want advanced", res[0])
	}
	res = f.complete(t, f.linguist.ID, "linguist", body, "A")
	if res[0].Outcome != scopecomplete.OutcomeFailed || res[0].Error == nil {
		t.Errorf("repeat: %+v, want failed: the step changed", res[0])
	}
	var reviewStatus string
	if err := f.pool.QueryRow(testutil.Ctx(),
		`SELECT status FROM job_step_assignments WHERE job_id = $1 AND workflow_step_id = $2`, f.jobs["A"].ID, review.ID).Scan(&reviewStatus); err != nil {
		t.Fatal(err)
	}
	if reviewStatus != "in_progress" {
		t.Errorf("review step = %s, want still in progress after the repeat", reviewStatus)
	}
}

// A job whose last step closed but whose completion did not (a try cut off
// between the two) is completed by the next try.
func TestIntegrationScopeComplete_FinishesAJobLeftHalfDone(t *testing.T) {
	f := newFixture(t)
	f.exec(t, `UPDATE job_step_assignments SET status = 'completed' WHERE job_id = $1`, f.jobs["A"].ID)
	f.exec(t, `UPDATE jobs SET current_step_id = NULL WHERE id = $1`, f.jobs["A"].ID)

	res := f.complete(t, f.linguist.ID, "linguist", nil, "A")
	if res[0].Outcome != scopecomplete.OutcomeCompleted {
		t.Fatalf("result: %+v, want completed", res[0])
	}
	if job, _ := f.jobStatus(t, "A"); job != "completed" {
		t.Errorf("A's job = %s, want completed", job)
	}
}

// Sending the last step's batch again, with the step id the editor took when
// the dialog opened, reports the file completed, as the first answer did, and
// completes nothing a second time.
func TestIntegrationScopeComplete_ResentCompletionStaysCompleted(t *testing.T) {
	f := newFixture(t)
	var step int64
	if err := f.pool.QueryRow(testutil.Ctx(), `SELECT current_step_id FROM jobs WHERE id = $1`, f.jobs["A"].ID).Scan(&step); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"steps": map[string]int64{fmt.Sprint(f.jobs["A"].ID): step}}
	if res := f.complete(t, f.linguist.ID, "linguist", body, "A"); res[0].Outcome != scopecomplete.OutcomeCompleted {
		t.Fatalf("first: %+v, want completed", res[0])
	}
	state := func() string {
		t.Helper()
		var s string
		if err := f.pool.QueryRow(testutil.Ctx(),
			`SELECT j.status || '|' || j.updated_at::text || '|' || coalesce(j.current_step_id::text, '') || '|' ||
			        string_agg(jsa.id::text || ':' || jsa.status || ':' || coalesce(jsa.completed_at::text, ''), ',' ORDER BY jsa.id)
			 FROM jobs j JOIN job_step_assignments jsa ON jsa.job_id = j.id WHERE j.id = $1
			 GROUP BY j.id`, f.jobs["A"].ID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := state()

	res := f.complete(t, f.linguist.ID, "linguist", body, "A")
	if res[0].Outcome != scopecomplete.OutcomeCompleted || res[0].Error != nil {
		t.Errorf("resent: %+v, want completed with no error", res[0])
	}
	if after := state(); after != before {
		t.Errorf("job state changed on the resend:\n before %s\n after  %s", before, after)
	}
}
