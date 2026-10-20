package project_test

// Workflow edits on a project with running jobs: a step a
// job has reached keeps its identity — swapping or removing it is 409 — while
// untouched positions may change and appended steps land on every job.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationWorkflowEditsWithRunningJobs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "InFlight")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@inflight.test", "pass", "project_manager")
	reviewer := testutil.SeedUserWithRole(t, q, tenant.ID, "rev@inflight.test", "pass", "linguist")
	translation := testutil.SeedStepDefinition(t, q, tenant.ID, "Translation", "human", "", nil)
	review := testutil.SeedStepDefinition(t, q, tenant.ID, "Review", "human", "", nil)
	proofread := testutil.SeedStepDefinition(t, q, tenant.ID, "Proofread", "human", "", nil)
	lqa := testutil.SeedStepDefinition(t, q, tenant.ID, "LQA", "human", "", nil)

	// Project with Translation → Review (the defaults link to these two).
	pid := createProjectWithBody(t, router, map[string]any{
		"name": "Running", "source_language": "en", "target_languages": []string{"de"},
	}, pm.ID, tenant.ID)
	project, err := q.GetProject(ctx, db.GetProjectParams{ID: pid, TenantID: tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	steps, _ := q.ListSettingsWorkflowSteps(ctx, project.SettingsProfileID)
	if len(steps) != 2 {
		t.Fatalf("precondition: 2 default steps, got %d", len(steps))
	}

	// One job sitting on step 1 (Translation, in_progress); step 2 pending.
	file := testutil.SeedFile(t, q, pid, "a.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	a1, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: job.ID, WorkflowStepID: steps[0].ID, AssignedTo: &pm.ID, Status: "in_progress"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: job.ID, WorkflowStepID: steps[1].ID, Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &a1.ID}); err != nil {
		t.Fatal(err)
	}

	put := func(steps []map[string]any) (int, string) {
		rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", pid), map[string]any{"steps": steps}, pm.ID, tenant.ID)
		return rec.Code, rec.Body.String()
	}
	ref := func(id int64, extra ...map[string]any) map[string]any {
		m := map[string]any{"step_definition_id": id}
		for _, e := range extra {
			for k, v := range e {
				m[k] = v
			}
		}
		return m
	}
	assignments := func() []db.ListJobStepAssignmentsRow {
		rows, err := q.ListJobStepAssignments(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}

	// Swapping the reached first step → 409 naming it.
	if code, body := put([]map[string]any{ref(proofread.ID), ref(review.ID)}); code != http.StatusConflict || !contains(body, "Translation") {
		t.Fatalf("changing a reached step: want 409 naming it, got %d %s", code, body)
	}
	// Removing the reached first step (only Review left) → 409.
	if code, _ := put([]map[string]any{ref(review.ID)}); code != http.StatusConflict {
		t.Fatalf("removing a reached step: want 409, got %d", code)
	}
	// The pending second step may be swapped, and its assignee set.
	if code, body := put([]map[string]any{ref(translation.ID), ref(proofread.ID, map[string]any{"default_assignee": reviewer.ID})}); code != http.StatusOK {
		t.Fatalf("swapping a pending step: want 200, got %d %s", code, body)
	}
	if rows := assignments(); len(rows) != 2 || rows[1].StepName != "Proofread" || rows[1].Status != "pending" || rows[1].AssignedTo == nil || *rows[1].AssignedTo != reviewer.ID {
		t.Fatalf("pending assignment should follow the swapped step + new assignee, got %+v", rows)
	}
	// Appending a step lands a pending assignment on the running job.
	if code, body := put([]map[string]any{ref(translation.ID), ref(proofread.ID), ref(lqa.ID)}); code != http.StatusOK {
		t.Fatalf("append: want 200, got %d %s", code, body)
	}
	if rows := assignments(); len(rows) != 3 || rows[2].StepName != "LQA" || rows[2].Status != "pending" {
		t.Fatalf("appended step should be pending on the job, got %+v", rows)
	}
	// Removing the never-reached trailing steps is fine.
	if code, body := put([]map[string]any{ref(translation.ID)}); code != http.StatusOK {
		t.Fatalf("removing pending steps: want 200, got %d %s", code, body)
	}
	if rows := assignments(); len(rows) != 1 || rows[0].Status != "in_progress" {
		t.Fatalf("only the reached step should remain, got %+v", rows)
	}

	// Applying a library workflow follows the same rule: a workflow starting
	// with a different step is refused; one that keeps Translation first and
	// adds Review is applied.
	wfBad := createLibraryWorkflow(t, router, "Review first", []int64{review.ID, translation.ID}, pm.ID, tenant.ID)
	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/apply", pid), map[string]any{"workflow_id": wfBad}, pm.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusConflict)
	wfGood := createLibraryWorkflow(t, router, "Standard", []int64{translation.ID, review.ID}, pm.ID, tenant.ID)
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/apply", pid), map[string]any{"workflow_id": wfGood}, pm.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if rows := assignments(); len(rows) != 2 || rows[1].StepName != "Review" || rows[1].Status != "pending" {
		t.Fatalf("apply should append Review as pending, got %+v", rows)
	}
	// Detaching is always allowed.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/workflow/detach", pid), nil, pm.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// A job whose workflow already finished (all steps completed, job not yet
	// completed) starts the appended step at once; a completed job is left alone.
	fileDone := testutil.SeedFile(t, q, pid, "done.txt", db.FileFormatTxt)
	doneJob := testutil.SeedJob(t, q, fileDone.ID, "de")
	cur, _ := q.ListSettingsWorkflowSteps(ctx, project.SettingsProfileID)
	for _, st := range cur {
		if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: doneJob.ID, WorkflowStepID: st.ID, Status: "completed"}); err != nil {
			t.Fatal(err)
		}
	}
	fileClosed := testutil.SeedFile(t, q, pid, "closed.txt", db.FileFormatTxt)
	closedJob := testutil.SeedJob(t, q, fileClosed.ID, "de")
	for _, st := range cur {
		if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: closedJob.ID, WorkflowStepID: st.ID, Status: "completed"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: closedJob.ID, Status: "completed", CurrentStep: "Done"}); err != nil {
		t.Fatal(err)
	}
	if code, body := put([]map[string]any{ref(translation.ID), ref(review.ID), ref(lqa.ID, map[string]any{"default_assignee": reviewer.ID})}); code != http.StatusOK {
		t.Fatalf("append after finish: want 200, got %d %s", code, body)
	}
	doneRows, _ := q.ListJobStepAssignments(ctx, doneJob.ID)
	if len(doneRows) != 3 || doneRows[2].StepName != "LQA" || doneRows[2].Status != "in_progress" || doneRows[2].AssignedTo == nil || *doneRows[2].AssignedTo != reviewer.ID {
		t.Fatalf("finished job should start the appended step with its default assignee, got %+v", doneRows)
	}
	doneAfter, _ := q.GetJob(ctx, doneJob.ID)
	if doneAfter.CurrentStepID == nil || *doneAfter.CurrentStepID != doneRows[2].ID {
		t.Errorf("finished job should now sit on the appended step, got current_step_id=%v", doneAfter.CurrentStepID)
	}
	closedRows, _ := q.ListJobStepAssignments(ctx, closedJob.ID)
	if len(closedRows) != 2 {
		t.Errorf("a completed job must not get new steps, got %d assignments", len(closedRows))
	}
	// The running job got it pending, as before.
	if rows := assignments(); len(rows) != 3 || rows[2].Status != "pending" {
		t.Fatalf("running job: appended step should be pending, got %+v", rows)
	}
}

// An `ai` step appended to a job whose workflow has already finished starts at
// once with nobody assigned, and the handler enqueues its QE+APE run right
// away. A job still on an earlier step gets the
// step pending and no run.
//
// Anti-tautology: drop the enqueueStartedAISteps call from
// HandleSetWorkflowSteps and this fails with "want exactly one AI enqueue …
// got []" while the assignment assertions still pass.
func TestIntegrationAppendedAIStepStartsAndEnqueues(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	enq := &testutil.RecordingEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(pool, enq)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "AppendAI")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@appendai.test", "pass", "project_manager")
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "APE", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	aiRouterID := testutil.SeedRouterForEngine(t, q, tenant.ID, engine.ID)
	translation := testutil.SeedStepDefinition(t, q, tenant.ID, "Translation", "human", "", nil)
	review := testutil.SeedStepDefinition(t, q, tenant.ID, "Review", "human", "", nil)
	autoAPE := testutil.SeedStepDefinition(t, q, tenant.ID, "Auto APE", "ai", "", &aiRouterID)

	pid := createProjectWithBody(t, router, map[string]any{"name": "AppendAI"}, pm.ID, tenant.ID)
	project, err := q.GetProject(ctx, db.GetProjectParams{ID: pid, TenantID: tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	steps, err := q.ListSettingsWorkflowSteps(ctx, project.SettingsProfileID)
	if err != nil || len(steps) != 2 {
		t.Fatalf("precondition: 2 default steps, got %d (err=%v)", len(steps), err)
	}

	// finished: every step completed, no current step, job itself not completed.
	fileDone := testutil.SeedFile(t, q, pid, "done.txt", db.FileFormatTxt)
	finished := testutil.SeedJob(t, q, fileDone.ID, "de")
	for _, st := range steps {
		if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: finished.ID, WorkflowStepID: st.ID, Status: "completed"}); err != nil {
			t.Fatal(err)
		}
	}
	// running: still on Translation — the negative control.
	fileRun := testutil.SeedFile(t, q, pid, "running.txt", db.FileFormatTxt)
	running := testutil.SeedJob(t, q, fileRun.ID, "de")
	a1, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: running.ID, WorkflowStepID: steps[0].ID, AssignedTo: &pm.ID, Status: "in_progress"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: running.ID, WorkflowStepID: steps[1].ID, Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: running.ID, CurrentStepID: &a1.ID}); err != nil {
		t.Fatal(err)
	}

	rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", pid), map[string]any{
		"steps": []map[string]any{
			{"step_definition_id": translation.ID}, {"step_definition_id": review.ID}, {"step_definition_id": autoAPE.ID},
		},
	}, pm.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	doneRows, err := q.ListJobStepAssignments(ctx, finished.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(doneRows) != 3 || doneRows[2].StepName != "Auto APE" || doneRows[2].Status != "in_progress" {
		t.Fatalf("finished job should start the appended ai step, got %+v", doneRows)
	}
	if doneRows[2].AssignedTo != nil {
		t.Errorf("an ai step has no human assignee, got %d", *doneRows[2].AssignedTo)
	}
	doneJob, err := q.GetJob(ctx, finished.ID)
	if err != nil || doneJob.CurrentStepID == nil || *doneJob.CurrentStepID != doneRows[2].ID {
		t.Errorf("finished job should sit on the ai step %d, got %v (err=%v)", doneRows[2].ID, doneJob.CurrentStepID, err)
	}
	if len(enq.AISteps) != 1 || enq.AISteps[0].JobID != finished.ID || enq.AISteps[0].TenantID != tenant.ID {
		t.Fatalf("want exactly one AI enqueue for job %d in tenant %d, got %+v", finished.ID, tenant.ID, enq.AISteps)
	}
	if enq.AISteps[0].TriggeredBy == nil || *enq.AISteps[0].TriggeredBy != pm.ID {
		t.Errorf("the AI run should be attributed to the PM %d, got %v", pm.ID, enq.AISteps[0].TriggeredBy)
	}
	runRows, err := q.ListJobStepAssignments(ctx, running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runRows) != 3 || runRows[2].StepName != "Auto APE" || runRows[2].Status != "pending" {
		t.Errorf("running job should get the ai step pending, got %+v", runRows)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// A project (or template) may run a hybrid / ai step on a different QE+APE
// system than its library definition names — a per-profile snapshot override.
// Same reached-step rule: once a job has got to the step,
// its engine is pinned.
func TestIntegrationWorkflowStepEngineOverride(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "Override")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@override.test", "pass", "project_manager")
	qeA, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "QE A", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	qeB, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "QE B", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	routerA := testutil.SeedAIRouter(t, q, tenant.ID, "Router A", &qeA.ID).ID
	routerB := testutil.SeedAIRouter(t, q, tenant.ID, "Router B", &qeB.ID).ID
	// Another tenant's router: a valid id that must read as "not found" here.
	other := testutil.SeedTenant(t, q, "Other")
	foreign := testutil.SeedAIRouter(t, q, other.ID, "Foreign", nil).ID
	translation := testutil.SeedStepDefinition(t, q, tenant.ID, "Translation", "human", "", nil)
	aiReview := testutil.SeedStepDefinition(t, q, tenant.ID, "AI review", "hybrid", "", &routerA)

	pid := createProjectWithBody(t, router, map[string]any{
		"name": "Override", "source_language": "en", "target_languages": []string{"de"},
	}, pm.ID, tenant.ID)
	put := func(steps []map[string]any) (int, string) {
		rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", pid), map[string]any{"steps": steps}, pm.ID, tenant.ID)
		return rec.Code, rec.Body.String()
	}
	routerOf := func(idx int) *int64 {
		s := projectSettings(t, router, pid, pm.ID, tenant.ID)
		if idx >= len(s.Data.WorkflowSteps) {
			t.Fatalf("no step %d in %+v", idx, s.Data.WorkflowSteps)
		}
		return s.Data.WorkflowSteps[idx].AiRouterID
	}

	// The default router comes from the definition; an override replaces it.
	if code, body := put([]map[string]any{{"step_definition_id": translation.ID}, {"step_definition_id": aiReview.ID}}); code != http.StatusOK {
		t.Fatalf("set steps: %d %s", code, body)
	}
	if e := routerOf(1); e == nil || *e != routerA {
		t.Fatalf("expected the definition's router %d, got %v", routerA, e)
	}
	if code, body := put([]map[string]any{{"step_definition_id": translation.ID}, {"step_definition_id": aiReview.ID, "ai_router_id": routerB}}); code != http.StatusOK {
		t.Fatalf("override: %d %s", code, body)
	}
	if e := routerOf(1); e == nil || *e != routerB {
		t.Fatalf("expected override router %d, got %v", routerB, e)
	}
	// A resave without ai_router_id keeps the override (the snapshot is not
	// silently re-pointed at the definition's router).
	if code, body := put([]map[string]any{{"step_definition_id": translation.ID}, {"step_definition_id": aiReview.ID}}); code != http.StatusOK {
		t.Fatalf("resave: %d %s", code, body)
	}
	if e := routerOf(1); e == nil || *e != routerB {
		t.Fatalf("resave must keep the override %d, got %v", routerB, e)
	}
	// Bad overrides: a human step, another tenant's router.
	if code, _ := put([]map[string]any{{"step_definition_id": translation.ID, "ai_router_id": routerA}, {"step_definition_id": aiReview.ID}}); code != http.StatusBadRequest {
		t.Errorf("router on a human step: want 400, got %d", code)
	}
	if code, _ := put([]map[string]any{{"step_definition_id": translation.ID}, {"step_definition_id": aiReview.ID, "ai_router_id": foreign}}); code != http.StatusBadRequest {
		t.Errorf("foreign router on a step: want 400, got %d", code)
	}
	if e := routerOf(1); e == nil || *e != routerB {
		t.Fatalf("a refused override must not change the router: want %d, got %v", routerB, e)
	}

	// Once a job has reached the step, its router is pinned.
	project, _ := q.GetProject(ctx, db.GetProjectParams{ID: pid, TenantID: tenant.ID})
	steps, _ := q.ListSettingsWorkflowSteps(ctx, project.SettingsProfileID)
	file := testutil.SeedFile(t, q, pid, "a.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: job.ID, WorkflowStepID: steps[0].ID, Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: job.ID, WorkflowStepID: steps[1].ID, Status: "in_progress"}); err != nil {
		t.Fatal(err)
	}
	if code, body := put([]map[string]any{{"step_definition_id": translation.ID}, {"step_definition_id": aiReview.ID, "ai_router_id": routerA}}); code != http.StatusConflict {
		t.Fatalf("changing the router of a reached step: want 409, got %d %s", code, body)
	}
	// Same router again is not a change.
	if code, body := put([]map[string]any{{"step_definition_id": translation.ID}, {"step_definition_id": aiReview.ID, "ai_router_id": routerB}}); code != http.StatusOK {
		t.Fatalf("resave with the same router: want 200, got %d %s", code, body)
	}
}
