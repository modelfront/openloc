package project_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// deadlineOf returns the assignment's deadline for one job's only step.
func deadlineOf(t *testing.T, q db.Querier, jobID int64) *time.Time {
	t.Helper()
	rows, err := q.ListJobStepAssignments(context.Background(), jobID)
	if err != nil {
		t.Fatalf("ListJobStepAssignments: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 assignment on job %d, got %d", jobID, len(rows))
	}
	if !rows[0].Deadline.Valid {
		return nil
	}
	d := rows[0].Deadline.Time
	return &d
}

// stepsBody builds the workflow-steps PUT body for one step carrying a
// step-level deadline plus per-language overrides.
func stepsBody(t *testing.T, q db.Querier, tenantID int64, deadline string, langs map[string]string) map[string]any {
	t.Helper()
	lds := make([]map[string]any, 0, len(langs))
	for lang, d := range langs {
		lds = append(lds, map[string]any{"target_language": lang, "deadline": d})
	}
	return stepBodyWith(t, q, tenantID, map[string]any{"deadline": deadline, "language_deadlines": lds})
}

// stepBodyWith builds the PUT body for the one "translate" step with any
// fields set verbatim — a raw language_deadlines slice can repeat a language,
// which the map stepsBody takes cannot.
func stepBodyWith(t *testing.T, q db.Querier, tenantID int64, fields map[string]any) map[string]any {
	t.Helper()
	return map[string]any{"steps": []map[string]any{
		testutil.LibraryStep(t, q, tenantID, "translate", map[string]any{"role_required": "linguist"}, fields),
	}}
}

// Setting deadlines on a project that already has jobs must reach those jobs —
// per target language — instead of only applying to files uploaded later. The
// twin of the default_assignee propagation, and the same rule about what it
// may touch: a step nobody has finished yet.
//
// Anti-tautology: drop the RefreshJobStepAssignmentDeadlines call from
// writeLanguageDeadlines and both de/fr assertions flip to "no deadline".
func TestIntegrationWorkflowSteps_DeadlinesPropagateToInFlightJobs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DeadlinePropCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@dlprop.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "DLProp", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	deJob := testutil.SeedJob(t, q, file.ID, "de")
	frJob := testutil.SeedJob(t, q, file.ID, "fr")

	// One shared step, both jobs in flight on it and carrying no deadline yet.
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "translate", StepOrder: 0, RoleRequired: "linguist",
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	for _, jobID := range []int64{deJob.ID, frJob.ID} {
		a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
			JobID: jobID, WorkflowStepID: step.ID, Status: "in_progress",
		})
		if err != nil {
			t.Fatalf("create assignment: %v", err)
		}
		if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &a.ID}); err != nil {
			t.Fatalf("set current step: %v", err)
		}
	}
	if deadlineOf(t, q, deJob.ID) != nil {
		t.Fatal("precondition: jobs should start with no deadline")
	}

	stepDL := time.Date(2027, 6, 1, 17, 0, 0, 0, time.UTC)
	deDL := time.Date(2027, 5, 20, 9, 0, 0, 0, time.UTC)
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		stepsBody(t, q, tenant.ID, stepDL.Format(time.RFC3339), map[string]string{"de": deDL.Format(time.RFC3339)}),
		admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	if got := deadlineOf(t, q, deJob.ID); got == nil || !got.Equal(deDL) {
		t.Errorf("de job deadline = %v, want its language override %v", got, deDL)
	}
	if got := deadlineOf(t, q, frJob.ID); got == nil || !got.Equal(stepDL) {
		t.Errorf("fr job deadline = %v, want the step deadline %v", got, stepDL)
	}
}

// A completed step is history: re-dating the blueprint must not rewrite when
// work that is already done was due.
//
// Anti-tautology: widen the status filter in RefreshJobStepAssignmentDeadlines to
// every status and this flips RED.
func TestIntegrationWorkflowSteps_DeadlinesSkipCompletedAssignments(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DeadlineDoneCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@dldone.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "DLDone", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	originalDL := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "translate", StepOrder: 0,
		RoleRequired: "linguist", Deadline: pgtype.Timestamptz{Time: originalDL, Valid: true},
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: step.ID, Status: "completed",
		Deadline: pgtype.Timestamptz{Time: originalDL, Valid: true},
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	movedDL := time.Date(2027, 9, 9, 9, 0, 0, 0, time.UTC)
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		stepsBody(t, q, tenant.ID, movedDL.Format(time.RFC3339), nil),
		admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	if got := deadlineOf(t, q, job.ID); got == nil || !got.Equal(originalDL) {
		t.Errorf("completed assignment deadline = %v, want it untouched at %v", got, originalDL)
	}
}

// A per-language deadline may only name a language the project translates
// into — the UI offers exactly those, so the server enforces exactly those.
func TestIntegrationWorkflowSteps_RejectsUnknownTargetLanguage(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DeadlineLangCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@dllang.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "DLLang", "en", []string{"de"})

	deadline := time.Date(2027, 6, 1, 17, 0, 0, 0, time.UTC).Format(time.RFC3339)
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		stepsBody(t, q, tenant.ID, deadline, map[string]string{"ja": deadline}),
		admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// The same language, once the project actually translates into it, is fine.
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		stepsBody(t, q, tenant.ID, deadline, map[string]string{"de": deadline}),
		admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// A template holds no schedule. "Save as template" forwards the wizard's step
// draft verbatim, dates included, but a template has no screen to show them on
// and hands none to the projects it creates — so they are dropped on write
// rather than sitting in the table as data nothing can reach.
//
// Anti-tautology: remove the !isProject branch from ReplaceWorkflowSteps and
// both assertions flip.
func TestIntegrationTemplateWorkflowSteps_StoreNoDeadlines(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TemplateDeadlineCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@tpldl.test", "pass")
	template := testutil.SeedTemplate(t, q, tenant.ID, "Scheduled template")

	deadline := time.Date(2027, 6, 1, 17, 0, 0, 0, time.UTC).Format(time.RFC3339)
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/templates/%d/workflow-steps", template.ID),
		stepsBody(t, q, tenant.ID, deadline, map[string]string{"de": deadline}),
		admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	steps, err := q.ListSettingsWorkflowSteps(ctx, template.SettingsProfileID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("ListSettingsWorkflowSteps: %+v (err=%v)", steps, err)
	}
	if steps[0].Deadline.Valid {
		t.Errorf("template step deadline = %v, want none", steps[0].Deadline.Time)
	}
	rows, err := q.ListWorkflowStepAssignmentsForProfile(ctx, template.SettingsProfileID)
	if err != nil {
		t.Fatalf("ListWorkflowStepAssignmentsForProfile: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("template per-language deadlines = %+v, want none", rows)
	}
}

// Language codes are normalized on the way in, so "DE" and "de" are the same
// override rather than two rows racing to win the resolve.
func TestIntegrationWorkflowSteps_NormalizesTargetLanguage(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DeadlineNormCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@dlnorm.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "DLNorm", "en", []string{"de"})

	stepDL := time.Date(2027, 6, 1, 17, 0, 0, 0, time.UTC).Format(time.RFC3339)
	deDL := time.Date(2027, 5, 20, 9, 0, 0, 0, time.UTC)
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		stepsBody(t, q, tenant.ID, stepDL, map[string]string{"DE": deDL.Format(time.RFC3339)}),
		admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rows, err := q.ListWorkflowStepAssignmentsForProfile(ctx, project.SettingsProfileID)
	if err != nil {
		t.Fatalf("ListWorkflowStepAssignmentsForProfile: %v", err)
	}
	if len(rows) != 1 || rows[0].TargetLanguage != "de" {
		t.Fatalf("stored language codes = %+v, want one row for \"de\"", rows)
	}
}

// Clearing a step's date does not leave its in-flight jobs undated: the
// refresh falls back to the project's own deadline, the same last resort
// materialization uses.
//
// Anti-tautology: drop the `p.deadline` term from the COALESCE in
// RefreshJobStepAssignmentDeadlines and this flips to "no deadline".
func TestIntegrationWorkflowSteps_PropagationFallsBackToProjectDeadline(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PropProjDLCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@propprojdl.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "PropProjDL", "en", []string{"de"})
	projectDL := time.Date(2027, 8, 12, 15, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `UPDATE projects SET deadline = $1 WHERE id = $2`, projectDL, project.ID); err != nil {
		t.Fatalf("set project deadline: %v", err)
	}

	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	stepDL := pgtype.Timestamptz{Time: time.Date(2027, 1, 1, 9, 0, 0, 0, time.UTC), Valid: true}
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "translate", StepOrder: 0, RoleRequired: "linguist",
		Deadline: stepDL,
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: step.ID, Status: "in_progress", Deadline: stepDL,
	})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &a.ID}); err != nil {
		t.Fatalf("set current step: %v", err)
	}

	// The step's date is cleared, and no language override takes its place.
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		stepsBody(t, q, tenant.ID, "", nil), admin.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	if got := deadlineOf(t, q, job.ID); got == nil || !got.Equal(projectDL) {
		t.Errorf("job deadline = %v, want the project's own %v", got, projectDL)
	}
}

// The project fallback must not reach an ai step: it completes itself, and a
// CHECK keeps its blueprint row deadline-free, so a date on its assignment
// would wake the deadline cron for work nobody is waiting on. Driven through
// the query rather than the PUT, because rebuilding an ai step over one a job
// has reached is refused for identity reasons before the refresh ever runs.
//
// Anti-tautology: drop the `CASE WHEN ws.step_type = 'ai'` arm from
// RefreshJobStepAssignmentDeadlines and this flips to the project's date.
func TestIntegrationWorkflowSteps_PropagationSkipsAISteps(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "PropAICo")
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "APE", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	aiRouterID := testutil.SeedRouterForEngine(t, q, tenant.ID, engine.ID)
	project := testutil.SeedProject(t, q, tenant.ID, "PropAI", "en", []string{"de"})
	if _, err := pool.Exec(ctx, `UPDATE projects SET deadline = $1 WHERE id = $2`,
		time.Date(2027, 8, 12, 15, 0, 0, 0, time.UTC), project.ID); err != nil {
		t.Fatalf("set project deadline: %v", err)
	}

	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	aiJob := testutil.SeedJob(t, q, file.ID, "de")
	aiStep, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "Auto APE", StepOrder: 0,
		RoleRequired: "linguist", AiRouterID: &aiRouterID, StepType: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: aiJob.ID, WorkflowStepID: aiStep.ID, Status: "in_progress",
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	// A human step beside it, so the same refresh is shown to DO date the step
	// that should be dated — otherwise a no-op query would pass this test.
	humanJob := testutil.SeedJob(t, q, file.ID, "fr")
	humanStep, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "translate", StepOrder: 1, RoleRequired: "linguist",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: humanJob.ID, WorkflowStepID: humanStep.ID, Status: "in_progress",
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	for _, id := range []int64{aiStep.ID, humanStep.ID} {
		if err := q.RefreshJobStepAssignmentDeadlines(ctx, db.RefreshJobStepAssignmentDeadlinesParams{
			WorkflowStepID: id, TenantID: tenant.ID,
		}); err != nil {
			t.Fatalf("refresh: %v", err)
		}
	}

	if got := deadlineOf(t, q, aiJob.ID); got != nil {
		t.Errorf("ai step assignment = %v, want no deadline", got)
	}
	if got := deadlineOf(t, q, humanJob.ID); got == nil {
		t.Error("human step assignment has no deadline — the refresh did nothing, so the ai assertion proves nothing")
	}
}

// A date a PM set on one job by hand survives a workflow save that changes
// nothing the step's deadlines resolve from — an assignee edit here, or an MCP
// patch of another step, which resubmits every step. A save that does change
// the step's date, or its per-language set, overwrites it.
//
// Anti-tautology: make writeLanguageDeadlines unconditional again in
// ReplaceWorkflowSteps and the first assertion flips to the step's date.
func TestIntegrationWorkflowSteps_HandSetJobDeadlineSurvivesUnrelatedEdit(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "HandDeadlineCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@handdl.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "HandDL", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	stepDL := time.Date(2027, 6, 1, 17, 0, 0, 0, time.UTC)
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "translate", StepOrder: 0,
		RoleRequired: "linguist", Deadline: pgtype.Timestamptz{Time: stepDL, Valid: true},
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: step.ID, Status: "in_progress",
		Deadline: pgtype.Timestamptz{Time: stepDL, Valid: true},
	})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &a.ID}); err != nil {
		t.Fatalf("set current step: %v", err)
	}
	setByHand := func(d time.Time) {
		t.Helper()
		if err := q.UpdateStepAssignmentDeadline(ctx, db.UpdateStepAssignmentDeadlineParams{
			ID: a.ID, Deadline: pgtype.Timestamptz{Time: d, Valid: true},
		}); err != nil {
			t.Fatalf("set deadline by hand: %v", err)
		}
	}
	put := func(fields map[string]any) {
		t.Helper()
		rec := testutil.DoRequest(t, router, "PUT",
			fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
			stepBodyWith(t, q, tenant.ID, fields), admin.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
	}

	handDL := time.Date(2027, 3, 3, 10, 0, 0, 0, time.UTC)
	setByHand(handDL)

	// Same date, new assignee.
	put(map[string]any{"deadline": stepDL.Format(time.RFC3339), "default_assignee": admin.ID})
	if got := deadlineOf(t, q, job.ID); got == nil || !got.Equal(handDL) {
		t.Errorf("after an assignee-only edit, job deadline = %v, want the hand-set %v", got, handDL)
	}
	rows, err := q.ListJobStepAssignments(ctx, job.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListJobStepAssignments: %+v (err=%v)", rows, err)
	}
	if rows[0].AssignedTo == nil || *rows[0].AssignedTo != admin.ID {
		t.Errorf("assignee = %v, want %d — the save did not reach the job at all", rows[0].AssignedTo, admin.ID)
	}

	// A new step date overwrites the hand-set one.
	movedDL := time.Date(2027, 7, 7, 17, 0, 0, 0, time.UTC)
	put(map[string]any{"deadline": movedDL.Format(time.RFC3339), "default_assignee": admin.ID})
	if got := deadlineOf(t, q, job.ID); got == nil || !got.Equal(movedDL) {
		t.Errorf("after a step date change, job deadline = %v, want %v", got, movedDL)
	}

	// So does a new per-language date, with the step's own date unchanged.
	setByHand(handDL)
	deDL := time.Date(2027, 5, 5, 9, 0, 0, 0, time.UTC)
	put(map[string]any{
		"deadline": movedDL.Format(time.RFC3339), "default_assignee": admin.ID,
		"language_deadlines": []map[string]any{{"target_language": "de", "deadline": deDL.Format(time.RFC3339)}},
	})
	if got := deadlineOf(t, q, job.ID); got == nil || !got.Equal(deDL) {
		t.Errorf("after a language date change, job deadline = %v, want %v", got, deDL)
	}
}
