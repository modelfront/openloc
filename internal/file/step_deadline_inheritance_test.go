package file_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// uploadFile posts a text file to a project. An upload carries no deadlines of
// its own: the assignments it creates resolve them from the project's workflow.
func uploadFile(t *testing.T, router http.Handler, path, filename, content string, userID, tenantID int64) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoMultipartUpload(t, router, path, filename, []byte(content), nil, userID, tenantID, "")
}

// deadlinesByStep returns, for every job in the project, a map of step name ->
// deadline (nil = NULL). Flattened across languages so a test can assert the
// same per-step value applied to every language job.
func deadlinesByStep(t *testing.T, q db.Querier, projectID int64) []map[string]*time.Time {
	t.Helper()
	ctx := context.Background()
	jobs, err := q.ListJobsByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListJobsByProject: %v", err)
	}
	out := make([]map[string]*time.Time, 0, len(jobs))
	for _, j := range jobs {
		assigns, err := q.ListJobStepAssignments(ctx, j.ID)
		if err != nil {
			t.Fatalf("ListJobStepAssignments: %v", err)
		}
		m := map[string]*time.Time{}
		for _, a := range assigns {
			if a.Deadline.Valid {
				tt := a.Deadline.Time
				m[a.StepName] = &tt
			} else {
				m[a.StepName] = nil
			}
		}
		out = append(out, m)
	}
	return out
}

// deadlinesByLangStep is deadlinesByStep keyed by the job's target language,
// for the per-language cases where which job got which date is the point.
func deadlinesByLangStep(t *testing.T, q db.Querier, projectID int64) map[string]map[string]*time.Time {
	t.Helper()
	ctx := context.Background()
	jobs, err := q.ListJobsByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListJobsByProject: %v", err)
	}
	out := map[string]map[string]*time.Time{}
	for _, j := range jobs {
		assigns, err := q.ListJobStepAssignments(ctx, j.ID)
		if err != nil {
			t.Fatalf("ListJobStepAssignments: %v", err)
		}
		m := map[string]*time.Time{}
		for _, a := range assigns {
			if a.Deadline.Valid {
				tt := a.Deadline.Time
				m[a.StepName] = &tt
			} else {
				m[a.StepName] = nil
			}
		}
		out[j.TargetLanguage] = m
	}
	return out
}

// wantDeadline fails unless the job for lang has exactly want on step.
func wantDeadline(t *testing.T, got map[string]map[string]*time.Time, lang, step string, want *time.Time) {
	t.Helper()
	have := got[lang][step]
	switch {
	case want == nil && have != nil:
		t.Errorf("%s/%s = %v, want no deadline", lang, step, have)
	case want != nil && have == nil:
		t.Errorf("%s/%s = no deadline, want %v", lang, step, want)
	case want != nil && !have.Equal(*want):
		t.Errorf("%s/%s = %v, want %v", lang, step, have, want)
	}
}

func ts(tm time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: tm, Valid: true} }

// A step's per-language deadline beats its own, and a language with no
// override falls back to the step's. The whole point of the feature: one step,
// different dates per target language.
//
// Anti-tautology: make StepDeadlines.For return step.Deadline unconditionally
// → the de assertion flips to the step-level date.
func TestIntegrationUpload_AssignmentsInheritPerLanguageDeadlines(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PerLangCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@perlang.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "PerLang", "en", []string{"fr", "de", "es"})

	stepDL := time.Date(2027, 2, 1, 17, 0, 0, 0, time.UTC)
	deDL := time.Date(2027, 1, 20, 9, 0, 0, 0, time.UTC)
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "translate", StepOrder: 0,
		RoleRequired: "member", Deadline: ts(stepDL),
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	if err := q.CreateWorkflowStepAssignment(ctx, db.CreateWorkflowStepAssignmentParams{
		WorkflowStepID: step.ID, TargetLanguage: "de", Deadline: ts(deDL),
	}); err != nil {
		t.Fatalf("create language deadline: %v", err)
	}

	rec := uploadFile(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"perlang.txt", "Hello world.", user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	got := deadlinesByLangStep(t, q, project.ID)
	wantDeadline(t, got, "de", "translate", &deDL)
	wantDeadline(t, got, "fr", "translate", &stepDL)
	wantDeadline(t, got, "es", "translate", &stepDL)
}

// workflow_step_assignments is the blueprint twin of job_step_assignments and
// will grow a per-language assignee beside `deadline`, so a row can exist
// while carrying no deadline at all. Such a row must leave the step's own
// deadline in place rather than blanking it.
//
// Anti-tautology: drop `AND wsa.deadline IS NOT NULL` from
// ListWorkflowStepAssignmentsForProfile and the de assertion flips to NULL.
func TestIntegrationUpload_LanguageRowWithoutDeadlineFallsBackToStep(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "NullLangCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@nulllang.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "NullLang", "en", []string{"de", "fr"})

	stepDL := time.Date(2027, 2, 1, 17, 0, 0, 0, time.UTC)
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "translate", StepOrder: 0,
		RoleRequired: "member", Deadline: ts(stepDL),
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	// A row for de that sets nothing — what an assignee-only row will look like.
	if err := q.CreateWorkflowStepAssignment(ctx, db.CreateWorkflowStepAssignmentParams{
		WorkflowStepID: step.ID, TargetLanguage: "de", Deadline: pgtype.Timestamptz{},
	}); err != nil {
		t.Fatalf("create language row: %v", err)
	}

	rec := uploadFile(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"nulllang.txt", "Hello world.", user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	got := deadlinesByLangStep(t, q, project.ID)
	wantDeadline(t, got, "de", "translate", &stepDL)
	wantDeadline(t, got, "fr", "translate", &stepDL)
}

func TestIntegrationWorkflowStep_AIStepDeadlineRejectedByDB(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_ = pool

	tenant := testutil.SeedTenant(t, q, "AICheckCo")
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "APE", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	aiRouterID := testutil.SeedRouterForEngine(t, q, tenant.ID, engine.ID)
	project := testutil.SeedProject(t, q, tenant.ID, "AI check", "en", []string{"fr"})

	_, err = q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "Auto APE", StepOrder: 0,
		RoleRequired: "linguist", AiRouterID: &aiRouterID, StepType: "ai",
		Deadline: ts(time.Date(2027, 5, 1, 10, 0, 0, 0, time.UTC)),
	})
	if err == nil {
		t.Fatal("expected the workflow_steps_ai_no_deadline constraint to reject an ai step with a deadline")
	}
}

// On upload, each job_step_assignment inherits its workflow step's
// blueprint deadline. Applied to every target-language job.
//
// Anti-tautology: drop `Deadline: deadline` from CreateJobStepAssignment in the
// upload handler → every assertion below flips to NULL.
func TestIntegrationUpload_AssignmentsInheritStepDeadlines(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "InheritCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@inherit.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "Inherit", "en", []string{"fr", "de"})

	translateDL := time.Date(2027, 2, 1, 17, 0, 0, 0, time.UTC)
	if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "translate", StepOrder: 0,
		RoleRequired: "member", Deadline: ts(translateDL),
	}); err != nil {
		t.Fatalf("create step 0: %v", err)
	}
	if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "review", StepOrder: 1,
		RoleRequired: "member", // no deadline
	}); err != nil {
		t.Fatalf("create step 1: %v", err)
	}

	rec := uploadFile(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"inherit.txt", "Hello world.\nSecond line.", user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	perJob := deadlinesByStep(t, q, project.ID)
	if len(perJob) != 2 {
		t.Fatalf("expected 2 jobs (fr, de), got %d", len(perJob))
	}
	for _, m := range perJob {
		if m["translate"] == nil || !m["translate"].Equal(translateDL) {
			t.Errorf("translate step deadline = %v, want %v", m["translate"], translateDL)
		}
		if m["review"] != nil {
			t.Errorf("review step deadline = %v, want NULL (no blueprint deadline)", m["review"])
		}
	}
}

func TestIntegrationUpload_AIFirstStepUnassigned(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AIFirst")
	user := testutil.SeedUser(t, q, tenant.ID, "u@aifirst.test", "pw")
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "APE", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	aiRouterID := testutil.SeedRouterForEngine(t, q, tenant.ID, engine.ID)
	project := testutil.SeedProject(t, q, tenant.ID, "AI first", "en", []string{"fr"})
	if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "Auto APE", StepOrder: 0,
		RoleRequired: "linguist", AiRouterID: &aiRouterID, StepType: "ai",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "Review", StepOrder: 1, RoleRequired: "linguist",
	}); err != nil {
		t.Fatal(err)
	}

	rec := uploadFile(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"ai.txt", "Hello world.", user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	jobs, err := q.ListJobsByProject(ctx, project.ID)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d (err=%v)", len(jobs), err)
	}
	rows, err := q.ListJobStepAssignments(ctx, jobs[0].ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("expected 2 assignments, got %d (err=%v)", len(rows), err)
	}
	if rows[0].Status != "in_progress" || rows[0].AssignedTo != nil {
		t.Errorf("ai first step should be in_progress and unassigned, got status=%q assigned_to=%v", rows[0].Status, rows[0].AssignedTo)
	}
	if rows[1].Status != "pending" {
		t.Errorf("review should be pending, got %q", rows[1].Status)
	}
}

// setProjectDeadline puts a due date on the project itself. SeedProject makes
// projects without one, and the fallback under test only exists when there is.
func setProjectDeadline(t *testing.T, pool *pgxpool.Pool, projectID int64, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE projects SET deadline = $1 WHERE id = $2`, at, projectID); err != nil {
		t.Fatalf("set project deadline: %v", err)
	}
}

// A step nobody scheduled still gives its tasks a date: the project's own.
// Without it a PM who set one deadline on the project would get tasks with
// none, and the deadline cron only ever reads jsa.deadline.
//
// Anti-tautology: drop the `d.project` fallback from StepDeadlines.For and
// both assertions flip to "no deadline".
func TestIntegrationUpload_StepWithoutDeadlineFallsBackToProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ProjFallbackCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@projfb.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "ProjFallback", "en", []string{"de", "fr"})

	projectDL := time.Date(2027, 3, 10, 12, 0, 0, 0, time.UTC)
	setProjectDeadline(t, pool, project.ID, projectDL)

	// No deadline on the step, and no per-language row either.
	if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "translate", StepOrder: 0,
		RoleRequired: "member",
	}); err != nil {
		t.Fatalf("create step: %v", err)
	}

	rec := uploadFile(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"projfb.txt", "Hello world.", user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	got := deadlinesByLangStep(t, q, project.ID)
	wantDeadline(t, got, "de", "translate", &projectDL)
	wantDeadline(t, got, "fr", "translate", &projectDL)
}

// The fallback is the LAST resort — anything more specific still wins, so
// adding it cannot quietly rewrite a schedule somebody set.
func TestIntegrationUpload_ProjectDeadlineIsTheLastResort(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PrecedenceCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@prec.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "Precedence", "en", []string{"de", "fr"})

	projectDL := time.Date(2027, 3, 10, 12, 0, 0, 0, time.UTC)
	stepDL := time.Date(2027, 2, 1, 17, 0, 0, 0, time.UTC)
	deDL := time.Date(2027, 1, 20, 9, 0, 0, 0, time.UTC)
	setProjectDeadline(t, pool, project.ID, projectDL)

	scheduled, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "translate", StepOrder: 0,
		RoleRequired: "member", Deadline: ts(stepDL),
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	if err := q.CreateWorkflowStepAssignment(ctx, db.CreateWorkflowStepAssignmentParams{
		WorkflowStepID: scheduled.ID, TargetLanguage: "de", Deadline: ts(deDL),
	}); err != nil {
		t.Fatalf("create language deadline: %v", err)
	}
	// A second step nobody scheduled, to show both rules at once.
	if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "review", StepOrder: 1,
		RoleRequired: "member",
	}); err != nil {
		t.Fatalf("create step: %v", err)
	}

	rec := uploadFile(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"prec.txt", "Hello world.", user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	got := deadlinesByLangStep(t, q, project.ID)
	wantDeadline(t, got, "de", "translate", &deDL)   // language beats step
	wantDeadline(t, got, "fr", "translate", &stepDL) // step beats project
	wantDeadline(t, got, "de", "review", &projectDL) // nothing set → project
	wantDeadline(t, got, "fr", "review", &projectDL)
}

// The fallback must not reach an ai step: it completes itself, and a DB
// constraint keeps its blueprint row deadline-free.
func TestIntegrationUpload_AIStepIgnoresProjectDeadline(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AIProjDLCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@aiprojdl.test", "pw")
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "APE", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	aiRouterID := testutil.SeedRouterForEngine(t, q, tenant.ID, engine.ID)
	project := testutil.SeedProject(t, q, tenant.ID, "AI proj deadline", "en", []string{"fr"})
	setProjectDeadline(t, pool, project.ID, time.Date(2027, 3, 10, 12, 0, 0, 0, time.UTC))

	if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "Auto APE", StepOrder: 0,
		RoleRequired: "linguist", AiRouterID: &aiRouterID, StepType: "ai",
	}); err != nil {
		t.Fatal(err)
	}

	rec := uploadFile(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"aiprojdl.txt", "Hello world.", user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	wantDeadline(t, deadlinesByLangStep(t, q, project.ID), "fr", "Auto APE", nil)
}
