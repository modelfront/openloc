package project_test

// A step a job reached stays reached after the job is sent back past it: a
// reject that resets the steps in between, or a clear that reopens a finished
// workflow, leaves those tasks pending but with rounds on record. A workflow
// edit must not swap or drop them.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/stepround"
	"openloc/internal/testutil"
)

type reachedFixture struct {
	t        *testing.T
	ctx      context.Context
	q        db.Querier
	router   http.Handler
	tenantID int64
	ownerID  int64
	pid      int64
	jobID    int64
	tasks    []int64 // job step assignment ids, by position
	steps    []db.WorkflowStep
	proof    int64 // a step definition to swap in
}

func setupReached(t *testing.T) reachedFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	ownerID, tenantID := testutil.RegisterUser(t, q, "reached")
	proj := testutil.SeedProject(t, q, tenantID, "Reached", "en", []string{"de"})
	file := testutil.SeedFile(t, q, proj.ID, "r.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	tasks := testutil.SeedWorkflow(t, q, proj.SettingsProfileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translation", StepType: "human", Status: "in_progress"},
		testutil.WorkflowStepSpec{Name: "Review", StepType: "human"},
		testutil.WorkflowStepSpec{Name: "Final", StepType: "human"},
	)
	if err := stepround.Open(ctx, q, job.ID, tasks[0], stepround.Keep); err != nil {
		t.Fatalf("open first round: %v", err)
	}
	steps, err := q.ListSettingsWorkflowSteps(ctx, proj.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	proof := testutil.SeedStepDefinition(t, q, tenantID, "Proofread", "human", "", nil)
	return reachedFixture{
		t: t, ctx: ctx, q: q, router: testutil.NewTestRouter(pool),
		tenantID: tenantID, ownerID: ownerID, pid: proj.ID, jobID: job.ID,
		tasks: tasks, steps: steps, proof: proof.ID,
	}
}

func (f reachedFixture) post(path string, body any) {
	f.t.Helper()
	rec := testutil.DoRequest(f.t, f.router, "POST", path, body, f.ownerID, f.tenantID)
	testutil.AssertStatus(f.t, rec, http.StatusOK)
}

func (f reachedFixture) advance() {
	f.t.Helper()
	f.post(fmt.Sprintf("/api/jobs/%d/workflow/advance", f.jobID), nil)
}

func (f reachedFixture) keep(i int) map[string]any {
	return map[string]any{"existing_step_id": f.steps[i].ID}
}

func (f reachedFixture) put(steps ...map[string]any) (int, string) {
	f.t.Helper()
	rec := testutil.DoRequest(f.t, f.router, "PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", f.pid), map[string]any{"steps": steps}, f.ownerID, f.tenantID)
	return rec.Code, rec.Body.String()
}

func (f reachedFixture) statuses() []string {
	f.t.Helper()
	rows, err := f.q.ListJobStepAssignments(f.ctx, f.jobID)
	if err != nil {
		f.t.Fatal(err)
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Status
	}
	return out
}

func TestIntegrationWorkflowEdit_StepResetByRejectStaysReached(t *testing.T) {
	f := setupReached(t)
	f.advance() // Translation → Review
	f.advance() // Review → Final
	f.post(fmt.Sprintf("/api/jobs/%d/workflow/reject", f.jobID), map[string]any{"reason": "again", "to_step_id": f.tasks[0]})
	if got := f.statuses(); got[1] != "pending" {
		t.Fatalf("precondition: Review is pending again after the reject, got %v", got)
	}

	if code, body := f.put(f.keep(0), map[string]any{"step_definition_id": f.proof}, f.keep(2)); code != http.StatusConflict || !contains(body, "Review") {
		t.Fatalf("swapping a step the job reached before the reject: want 409 naming Review, got %d %s", code, body)
	}
}

func TestIntegrationWorkflowEdit_StepResetByClearStaysReached(t *testing.T) {
	f := setupReached(t)
	f.advance()
	f.advance()
	f.advance() // workflow finished
	f.post(fmt.Sprintf("/api/jobs/%d/clear-translations", f.jobID), map[string]any{"scope": "all"})
	if got := f.statuses(); got[0] != "in_progress" || got[2] != "pending" {
		t.Fatalf("precondition: the clear reopens the first step and resets the rest, got %v", got)
	}

	if code, body := f.put(f.keep(0), f.keep(1)); code != http.StatusConflict || !contains(body, "Final") {
		t.Fatalf("removing a step the job finished before the clear: want 409 naming Final, got %d %s", code, body)
	}
	// The step still has its job task, and its round history.
	if got := f.statuses(); len(got) != 3 {
		t.Fatalf("the refused edit must leave all three tasks, got %v", got)
	}
}

// Inserting a step after the locked prefix shifts the steps after it: each
// shifted position's waiting tasks take the assignee of the step now there.
func TestIntegrationWorkflowEdit_InsertReseedsWaitingTasks(t *testing.T) {
	f := setupReached(t)
	reviewer := testutil.SeedUserWithRole(t, f.q, f.tenantID, "rev@reached.test", "pass", "linguist")
	withAssignee := func(m map[string]any, id int64) map[string]any {
		m["default_assignee"] = id
		return m
	}
	if code, body := f.put(f.keep(0), withAssignee(f.keep(1), reviewer.ID), f.keep(2)); code != http.StatusOK {
		t.Fatalf("assign Review: want 200, got %d %s", code, body)
	}

	// Proofread (nobody by default) goes in at position 2, before Review.
	if code, body := f.put(f.keep(0), map[string]any{"step_definition_id": f.proof}, withAssignee(f.keep(1), reviewer.ID), f.keep(2)); code != http.StatusOK {
		t.Fatalf("insert Proofread: want 200, got %d %s", code, body)
	}
	rows, err := f.q.ListJobStepAssignments(f.ctx, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("want 4 tasks after the insert, got %d", len(rows))
	}
	if rows[1].StepName != "Proofread" || rows[1].AssignedTo != nil {
		t.Errorf("the Proofread task must be unassigned like its step, got %q assigned to %v", rows[1].StepName, rows[1].AssignedTo)
	}
	if rows[2].StepName != "Review" || rows[2].AssignedTo == nil || *rows[2].AssignedTo != reviewer.ID {
		t.Errorf("the Review task must stay with the reviewer, got %q assigned to %v", rows[2].StepName, rows[2].AssignedTo)
	}
}

// GET /settings tells the client which steps are locked, and marks the
// library workflow "edited" once the steps themselves change.
func TestIntegrationWorkflowEdit_SettingsShowLocksAndEditedMark(t *testing.T) {
	f := setupReached(t)
	f.advance() // Translation completed, Review in progress
	var defs []int64
	for _, name := range []string{"Translation", "Review", "Final"} {
		defs = append(defs, testutil.SeedStepDefinition(t, f.q, f.tenantID, name, "human", "", nil).ID)
	}
	wf := createLibraryWorkflow(t, f.router, "Three steps", defs, f.ownerID, f.tenantID)
	f.post(fmt.Sprintf("/api/projects/%d/workflow/apply", f.pid), map[string]any{"workflow_id": wf})

	type settingsBody struct {
		Project struct {
			WorkflowID     *int64 `json:"workflow_id"`
			WorkflowEdited bool   `json:"workflow_edited"`
		} `json:"project"`
		WorkflowSteps []struct {
			Name        string `json:"name"`
			Locked      bool   `json:"locked"`
			ReachedJobs int    `json:"reached_jobs"`
		} `json:"workflow_steps"`
	}
	get := func() settingsBody {
		t.Helper()
		rec := testutil.DoRequest(t, f.router, "GET", fmt.Sprintf("/api/projects/%d/settings", f.pid), nil, f.ownerID, f.tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		return testutil.DecodeData[settingsBody](t, rec)
	}

	got := get()
	if got.Project.WorkflowID == nil || *got.Project.WorkflowID != wf || got.Project.WorkflowEdited {
		t.Fatalf("after apply: want workflow %d, not edited; got %v edited=%v", wf, got.Project.WorkflowID, got.Project.WorkflowEdited)
	}
	if len(got.WorkflowSteps) != 3 {
		t.Fatalf("want 3 steps, got %+v", got.WorkflowSteps)
	}
	for i, want := range []struct {
		locked bool
		jobs   int
	}{{true, 1}, {true, 1}, {false, 0}} {
		if s := got.WorkflowSteps[i]; s.Locked != want.locked || s.ReachedJobs != want.jobs {
			t.Errorf("step %d (%s): want locked=%v reached_jobs=%d, got %+v", i+1, s.Name, want.locked, want.jobs, s)
		}
	}

	byDef := func(id int64, extra ...map[string]any) map[string]any {
		m := map[string]any{"step_definition_id": id}
		for _, e := range extra {
			for k, v := range e {
				m[k] = v
			}
		}
		return m
	}
	// A deadline is not a change of steps.
	if code, body := f.put(byDef(defs[0]), byDef(defs[1]), byDef(defs[2], map[string]any{"deadline": "2030-01-01T00:00:00Z"})); code != http.StatusOK {
		t.Fatalf("set a deadline: want 200, got %d %s", code, body)
	}
	if get().Project.WorkflowEdited {
		t.Error("a deadline-only save must not mark the workflow edited")
	}
	// Swapping the unlocked last step is.
	if code, body := f.put(byDef(defs[0]), byDef(defs[1]), byDef(f.proof)); code != http.StatusOK {
		t.Fatalf("swap the last step: want 200, got %d %s", code, body)
	}
	if !get().Project.WorkflowEdited {
		t.Error("swapping a step must mark the workflow edited")
	}
	// Applying the library workflow again clears the mark.
	f.post(fmt.Sprintf("/api/projects/%d/workflow/apply", f.pid), map[string]any{"workflow_id": wf})
	if get().Project.WorkflowEdited {
		t.Error("applying a workflow must clear the edited mark")
	}
}

// The preview reports what a save would do and writes nothing.
func TestIntegrationWorkflowEdit_PreviewWritesNothing(t *testing.T) {
	f := setupReached(t)
	// A second job that has finished every step but is not completed.
	doneFile := testutil.SeedFile(t, f.q, f.pid, "done.txt", db.FileFormatTxt)
	doneJob := testutil.SeedJob(t, f.q, doneFile.ID, "de")
	for _, s := range f.steps {
		testutil.AssignStep(t, f.q, doneJob.ID, s.ID, testutil.WorkflowStepSpec{Status: "completed"})
	}
	preview := func(steps ...map[string]any) (int, string) {
		rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/workflow-steps/preview", f.pid), map[string]any{"steps": steps}, f.ownerID, f.tenantID)
		return rec.Code, rec.Body.String()
	}

	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/workflow-steps/preview", f.pid),
		map[string]any{"steps": []map[string]any{f.keep(0), f.keep(1), f.keep(2), {"step_definition_id": f.proof}}}, f.ownerID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	type previewBody struct {
		Added, Removed, Changed int
		OpenJobs                int `json:"open_jobs"`
		StartNow                []struct {
			StepName string `json:"step_name"`
			RunsAI   bool   `json:"runs_ai"`
			Jobs     int
		} `json:"start_now"`
		Effects []string
	}
	got := testutil.DecodeData[previewBody](t, rec)
	if got.Added != 1 || got.Removed != 0 || got.Changed != 0 || got.OpenJobs != 2 {
		t.Errorf("want 1 added, 2 open jobs; got %+v", got)
	}
	if len(got.StartNow) != 1 || got.StartNow[0].StepName != "Proofread" || got.StartNow[0].Jobs != 1 || got.StartNow[0].RunsAI {
		t.Errorf("the finished job starts Proofread at once; got %+v", got.StartNow)
	}
	if len(got.Effects) != 2 || !contains(got.Effects[1], "Proofread") {
		t.Errorf("effects should name the step that starts now; got %q", got.Effects)
	}

	// Nothing was written.
	steps, err := f.q.ListSettingsWorkflowSteps(f.ctx, f.steps[0].SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Errorf("preview must not add the step, profile has %d", len(steps))
	}
	done, err := f.q.ListJobStepAssignments(f.ctx, doneJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 3 {
		t.Errorf("preview must not start a step on the finished job, it has %d tasks", len(done))
	}

	// The same refusals as the save.
	if code, body := preview(map[string]any{"step_definition_id": f.proof}, f.keep(1), f.keep(2)); code != http.StatusConflict || !contains(body, "Translation") {
		t.Errorf("preview of a locked swap: want 409 naming Translation, got %d %s", code, body)
	}
	// Another tenant's project is not found.
	otherID, otherTenant := testutil.RegisterUser(t, f.q, "other")
	rec = testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/workflow-steps/preview", f.pid),
		map[string]any{"steps": []map[string]any{f.keep(0)}}, otherID, otherTenant)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}
