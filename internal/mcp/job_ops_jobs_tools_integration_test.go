// job_ops job-level tools over the real protocol: Apply TM, clear, find and
// replace, workflow steps (advance / reject / assign / deadline), exports and
// the read tools.
package mcp_test

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// seedTwoStepWorkflow gives job A a two-step workflow: step 0 in progress
// and assigned to `first`, step 1 pending and unassigned.
func seedTwoStepWorkflow(t *testing.T, f *fixture, first *int64) (step0, step1 int64) {
	t.Helper()
	ctx := context.Background()
	var ids [2]int64
	for i, name := range []string{"Translation", "Review"} {
		ws, err := f.q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: f.projectA.SettingsProfileID, Name: name, StepOrder: int32(i), RoleRequired: "linguist",
		})
		if err != nil {
			t.Fatalf("create workflow step: %v", err)
		}
		status, assignee := "pending", (*int64)(nil)
		if i == 0 {
			status, assignee = "in_progress", first
		}
		a, err := f.q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
			JobID: f.jobA.ID, WorkflowStepID: ws.ID, AssignedTo: assignee, Status: status,
		})
		if err != nil {
			t.Fatalf("create step assignment: %v", err)
		}
		ids[i] = a.ID
	}
	if err := f.q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: f.jobA.ID, CurrentStepID: &ids[0]}); err != nil {
		t.Fatalf("set current step: %v", err)
	}
	return ids[0], ids[1]
}

func readJob(t *testing.T, f *fixture) db.Job {
	t.Helper()
	j, err := f.q.GetJobTenant(context.Background(), db.GetJobTenantParams{ID: f.jobA.ID, TenantID: f.tenantA.ID})
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	return j
}

func TestIntegrationMCPWorkflowStepTools(t *testing.T) {
	f := newFixture(t)
	step0, step1 := seedTwoStepWorkflow(t, f, &f.linguistA.ID)
	ling := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.linguistA.ID))
	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	// The note left for this step is part of the step: an agent working it
	// through MCP reads it where the editor shows it.
	if _, err := f.pool.Exec(context.Background(),
		"UPDATE job_step_assignments SET handoff_note = $1 WHERE id = $2", "Keep the product name in English", step0); err != nil {
		t.Fatal(err)
	}
	var steps struct {
		Steps []struct {
			StepAssignmentID int64  `json:"step_assignment_id"`
			StepName         string `json:"step_name"`
			Status           string `json:"status"`
			HandoffNote      string `json:"handoff_note"`
		} `json:"steps"`
	}
	if isErr, text := callTool(t, ling, "openloc_list_job_steps", map[string]any{"job_id": f.jobA.ID}, &steps); isErr {
		t.Fatalf("list_job_steps: %s", text)
	}
	if len(steps.Steps) != 2 || steps.Steps[0].Status != "in_progress" || steps.Steps[1].StepAssignmentID != step1 {
		t.Fatalf("unexpected steps: %+v", steps.Steps)
	}
	if steps.Steps[0].HandoffNote != "Keep the product name in English" || steps.Steps[1].HandoffNote != "" {
		t.Fatalf("handoff_note: want it on the step in progress only, got %+v", steps.Steps)
	}

	// Three unconfirmed segments: refused until the user agrees.
	isErr, text := callTool(t, ling, "openloc_advance_step", map[string]any{"job_id": f.jobA.ID}, nil)
	if !isErr || !strings.Contains(text, "3 segment") {
		t.Fatalf("advance with unconfirmed segments should be refused with the count, got isErr=%v %q", isErr, text)
	}
	if j := readJob(t, f); j.CurrentStepID == nil || *j.CurrentStepID != step0 {
		t.Fatal("a refused advance moved the job")
	}
	// A comment the editor's dialog would refuse is refused here too.
	isErr, text = callTool(t, ling, "openloc_advance_step",
		map[string]any{"job_id": f.jobA.ID, "allow_unconfirmed": true, "comment": "check\x00terms"}, nil)
	if !isErr || !strings.Contains(text, "cannot be stored") {
		t.Fatalf("advance with a control character in the comment should be refused, got isErr=%v %q", isErr, text)
	}
	if j := readJob(t, f); j.CurrentStepID == nil || *j.CurrentStepID != step0 {
		t.Fatal("an advance with a refused comment moved the job")
	}
	var adv struct {
		Status       string `json:"status"`
		NextStepName string `json:"next_step_name"`
	}
	if isErr, text := callTool(t, ling, "openloc_advance_step",
		map[string]any{"job_id": f.jobA.ID, "allow_unconfirmed": true, "comment": "  Keep the brand names in English.\n"}, &adv); isErr {
		t.Fatalf("advance: %s", text)
	}
	if adv.Status != "advanced" || adv.NextStepName != "Review" {
		t.Errorf("want advanced to Review, got %+v", adv)
	}
	if j := readJob(t, f); j.CurrentStepID == nil || *j.CurrentStepID != step1 {
		t.Fatalf("job is not on the review step: %+v", j.CurrentStepID)
	}
	// The comment is the review step's hand-off note, trimmed like the editor's.
	steps.Steps = nil
	if isErr, text := callTool(t, ling, "openloc_list_job_steps", map[string]any{"job_id": f.jobA.ID}, &steps); isErr {
		t.Fatalf("list_job_steps: %s", text)
	}
	if steps.Steps[1].HandoffNote != "Keep the brand names in English." {
		t.Errorf("review step handoff_note = %q, want the trimmed comment", steps.Steps[1].HandoffNote)
	}

	// The linguist is not the review step's assignee: refused.
	isErr, text = callTool(t, ling, "openloc_reject_step", map[string]any{"job_id": f.jobA.ID, "reason": "x"}, nil)
	if !isErr || !strings.Contains(text, "assigned user or a manager") {
		t.Errorf("a non-assignee linguist rejected the step: isErr=%v %q", isErr, text)
	}
	// A manager may, and the previous step reopens.
	var rej struct {
		ReopenedStepAssignmentID int64 `json:"reopened_step_assignment_id"`
	}
	if isErr, text := callTool(t, owner, "openloc_reject_step",
		map[string]any{"job_id": f.jobA.ID, "reason": "terminology"}, &rej); isErr {
		t.Fatalf("reject: %s", text)
	}
	if rej.ReopenedStepAssignmentID != step0 {
		t.Errorf("want step %d reopened, got %d", step0, rej.ReopenedStepAssignmentID)
	}

	// Manager tools: assign the review step and set its deadline.
	if isErr, text := callTool(t, owner, "openloc_assign_step", map[string]any{
		"job_id": f.jobA.ID, "step_assignment_id": step1, "user_id": f.linguistA.ID,
	}, nil); isErr {
		t.Fatalf("assign_step: %s", text)
	}
	if isErr, text := callTool(t, owner, "openloc_assign_step", map[string]any{
		"job_id": f.jobA.ID, "step_assignment_id": step1, "user_id": f.ownerB.ID,
	}, nil); !isErr || !strings.Contains(text, "active member") {
		t.Errorf("assigning another tenant's user must be refused, got isErr=%v %q", isErr, text)
	}
	if isErr, text := callTool(t, owner, "openloc_set_step_deadline", map[string]any{
		"job_id": f.jobA.ID, "step_assignment_id": step1, "deadline": "2030-01-02T15:00:00Z",
	}, nil); isErr {
		t.Fatalf("set_step_deadline: %s", text)
	}
	if isErr, _ := callTool(t, owner, "openloc_set_step_deadline", map[string]any{
		"job_id": f.jobA.ID, "step_assignment_id": step1, "deadline": "next friday",
	}, nil); !isErr {
		t.Error("an unparseable deadline was accepted")
	}
	steps.Steps = nil
	if isErr, text := callTool(t, owner, "openloc_list_job_steps", map[string]any{"job_id": f.jobA.ID}, &steps); isErr {
		t.Fatalf("list_job_steps: %s", text)
	}
	rows, err := f.q.ListJobStepAssignments(context.Background(), f.jobA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rows[1].AssignedTo == nil || *rows[1].AssignedTo != f.linguistA.ID || !rows[1].Deadline.Valid {
		t.Errorf("review step not assigned / no deadline: %+v", rows[1])
	}
	// A step of another job is not reachable through this job.
	if isErr, text := callTool(t, owner, "openloc_assign_step", map[string]any{
		"job_id": f.jobA.ID, "step_assignment_id": 999999, "user_id": f.linguistA.ID,
	}, nil); !isErr {
		t.Error("a foreign step assignment id was accepted")
	} else {
		assertOpaqueNotFound(t, text)
	}

	// Owner runs the workflow to its end: the job becomes completed.
	var done struct {
		Status       string `json:"status"`
		JobCompleted bool   `json:"job_completed"`
		Warning      string `json:"warning"`
	}
	for i := 0; i < 2; i++ {
		if isErr, text := callTool(t, owner, "openloc_advance_step",
			map[string]any{"job_id": f.jobA.ID, "allow_unconfirmed": true}, &done); isErr {
			t.Fatalf("advance %d: %s", i, text)
		}
	}
	if done.Status != "workflow_completed" || !done.JobCompleted {
		t.Fatalf("want workflow_completed + job_completed after the last step, got %+v", done)
	}
	if j := readJob(t, f); j.Status != "completed" {
		t.Errorf("the last advance must complete the job, status %q", j.Status)
	}

	// Cross-tenant.
	other := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	for _, tool := range []string{"openloc_list_job_steps", "openloc_advance_step", "openloc_reject_step"} {
		isErr, text := callTool(t, other, tool, map[string]any{"job_id": f.jobA.ID}, nil)
		if !isErr {
			t.Errorf("%s: another tenant got through", tool)
			continue
		}
		assertOpaqueNotFound(t, text)
	}
}

func TestIntegrationMCPApplyTMFindReplaceAndClearJob(t *testing.T) {
	f := newFixture(t)
	tm := seedWriteTM(t, f)
	if _, err := f.q.InsertTM(context.Background(), db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Hello world", TargetText: "Hallo Welt", CreatedBy: "test",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	first, _ := firstTranslation(t, s, f.jobA.ID)

	// The entry was stored between the same neighbours as the first segment:
	// asked by segment it is a 101% in-context match, by source text alone 100%.
	if _, err := f.pool.Exec(context.Background(), `
		UPDATE translation_memory SET prev_source_hash = md5(''), next_source_hash = md5('Second segment')
		WHERE tm_id = $1`, tm.ID); err != nil {
		t.Fatalf("set context: %v", err)
	}
	type tmMatches struct {
		Matches []struct {
			TargetText string  `json:"target_text"`
			Score      float64 `json:"score"`
			Percent    int16   `json:"percent"`
		} `json:"matches"`
	}
	var matches tmMatches
	if isErr, text := callTool(t, s, "openloc_get_tm_matches",
		map[string]any{"job_id": f.jobA.ID, "translation_id": first}, &matches); isErr {
		t.Fatalf("get_tm_matches: %s", text)
	}
	if len(matches.Matches) != 1 || matches.Matches[0].TargetText != "Hallo Welt" {
		t.Fatalf("want the TM entry as a match, got %+v", matches.Matches)
	}
	if got := matches.Matches[0].Percent; got != 101 {
		t.Errorf("by segment: percent %d, want 101 (in context)", got)
	}
	var bySource tmMatches
	if isErr, text := callTool(t, s, "openloc_get_tm_matches",
		map[string]any{"job_id": f.jobA.ID, "source_text": "Hello world"}, &bySource); isErr {
		t.Fatalf("get_tm_matches by source: %s", text)
	}
	if len(bySource.Matches) != 1 || bySource.Matches[0].Percent != 100 {
		t.Errorf("by source text: want one match at 100%%, got %+v", bySource.Matches)
	}

	var run struct {
		Stopped bool `json:"stopped"`
	}
	if isErr, text := callTool(t, s, "openloc_apply_tm", map[string]any{"job_id": f.jobA.ID}, &run); isErr {
		t.Fatalf("apply_tm: %s", text)
	}
	if got := readTranslation(t, f, f.tenantA.ID, first); ptr.Deref(got.ActiveText) != "Hallo Welt" {
		t.Fatalf("apply_tm should fill the segment from TM, got %q", ptr.Deref(got.ActiveText))
	}
	// The run lands in the project history with what it filled, read back
	// through the history tool.
	var history struct {
		Events []struct {
			EventType string         `json:"event_type"`
			Details   map[string]any `json:"details"`
		} `json:"events"`
	}
	if isErr, text := callTool(t, s, "openloc_list_project_events",
		map[string]any{"project_id": f.projectA.ID, "type": "job.tm_apply_started"}, &history); isErr {
		t.Fatalf("list_project_events: %s", text)
	}
	if len(history.Events) != 1 || history.Events[0].Details["tm_exact"] != float64(1) || history.Events[0].Details["tm_fuzzy"] != float64(0) {
		t.Errorf("apply_tm history = %+v, want one event with tm_exact 1, tm_fuzzy 0", history.Events)
	}
	if isErr, text := callTool(t, s, "openloc_apply_mt", map[string]any{"job_id": f.jobA.ID}, nil); !isErr || !strings.Contains(text, "No MT engine") {
		t.Errorf("apply_mt without an engine should explain what to configure, got isErr=%v %q", isErr, text)
	}
	// A job with no workflow step has no AI step: a failed run, not a
	// missing job.
	if isErr, text := callTool(t, s, "openloc_rerun_ai_step", map[string]any{"job_id": f.jobA.ID}, nil); !isErr || strings.Contains(text, "not found") {
		t.Errorf("rerun_ai_step without a workflow step should fail as a run, got isErr=%v %q", isErr, text)
	}

	// Find and replace: the preview lists the change and writes nothing.
	fr := map[string]any{"job_id": f.jobA.ID, "find": "Welt", "replace": "Erde"}
	var g guardedOut
	if isErr, text := callTool(t, s, "openloc_find_replace_job", fr, &g); isErr {
		t.Fatalf("find_replace preview: %s", text)
	}
	if g.Preview == nil || !strings.Contains(strings.Join(g.Preview.Effects, "\n"), "Hallo Erde") {
		t.Fatalf("preview must show the new text: %+v", g.Preview)
	}
	if ptr.Deref(readTranslation(t, f, f.tenantA.ID, first).ActiveText) != "Hallo Welt" {
		t.Fatal("the find/replace preview changed text")
	}
	if isErr, text := confirmedCall(t, s, "openloc_find_replace_job", fr, nil); isErr {
		t.Fatalf("find_replace: %s", text)
	}
	got := readTranslation(t, f, f.tenantA.ID, first)
	if ptr.Deref(got.ActiveText) != "Hallo Erde" || got.Status != db.TranslationStatusNeedsReview {
		t.Fatalf("want the replaced text saved as a draft, got %q / %s", ptr.Deref(got.ActiveText), got.Status)
	}
	if n := tmEntryCount(t, f, tm.ID); n != 1 {
		t.Errorf("find/replace wrote to TM: %d entries", n)
	}

	// Clear everything: the preview counts, the confirm clears.
	clr := map[string]any{"job_id": f.jobA.ID, "scope": "all"}
	g = guardedOut{}
	if isErr, text := callTool(t, s, "openloc_clear_job_translations", clr, &g); isErr {
		t.Fatalf("clear preview: %s", text)
	}
	if g.Preview == nil || !strings.Contains(g.Preview.Summary, "Clear 1 segment") {
		t.Errorf("preview must give the exact count: %+v", g.Preview)
	}
	if ptr.Deref(readTranslation(t, f, f.tenantA.ID, first).ActiveText) == "" {
		t.Fatal("the clear preview cleared text")
	}
	if isErr, _ := callTool(t, s, "openloc_clear_job_translations",
		map[string]any{"job_id": f.jobA.ID, "scope": "everything"}, nil); !isErr {
		t.Error("an invalid scope was accepted at preview")
	}
	var cleared struct {
		Cleared int64 `json:"cleared"`
	}
	if isErr, text := confirmedCall(t, s, "openloc_clear_job_translations", clr, &cleared); isErr {
		t.Fatalf("clear: %s", text)
	}
	if cleared.Cleared != 1 || readTranslation(t, f, f.tenantA.ID, first).ActiveText != nil {
		t.Errorf("want 1 segment cleared, got %d", cleared.Cleared)
	}

	// Unassigned linguist and another tenant see none of it.
	ling := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.linguistA.ID))
	other := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	for _, sess := range []sessionT{ling, other} {
		for tool, args := range map[string]map[string]any{
			"openloc_apply_tm":               {"job_id": f.jobA.ID},
			"openloc_clear_job_translations": {"job_id": f.jobA.ID, "scope": "all"},
			"openloc_find_replace_job":       {"job_id": f.jobA.ID, "find": "x", "replace": "y"},
			"openloc_get_job_analysis":       {"job_id": f.jobA.ID},
		} {
			isErr, text := callTool(t, sess, tool, args, nil)
			if !isErr {
				t.Errorf("%s: got through without access", tool)
				continue
			}
			assertOpaqueNotFound(t, text)
		}
	}
}

func TestIntegrationMCPJobReadTools(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)

	var exp struct {
		DownloadPath string `json:"download_path"`
		Filename     string `json:"filename"`
	}
	if isErr, text := callTool(t, s, "openloc_export_job", map[string]any{"job_id": f.jobA.ID, "format": "xliff"}, &exp); isErr {
		t.Fatalf("export_job: %s", text)
	}
	if exp.DownloadPath != "/api/jobs/"+itoa(f.jobA.ID)+"/export?format=xliff" || exp.Filename != "alpha_de.xlf" {
		t.Errorf("unexpected export link: %+v", exp)
	}
	if isErr, _ := callTool(t, s, "openloc_export_job", map[string]any{"job_id": f.jobA.ID, "format": "pdf"}, nil); !isErr {
		t.Error("an unknown export format was accepted")
	}

	var analysis struct {
		JobID    int64 `json:"job_id"`
		Analysis struct {
			Total struct {
				Segments int `json:"segments"`
			} `json:"total"`
		} `json:"analysis"`
	}
	if isErr, text := callTool(t, s, "openloc_get_job_analysis", map[string]any{"job_id": f.jobA.ID}, &analysis); isErr {
		t.Fatalf("get_job_analysis: %s", text)
	}
	if analysis.Analysis.Total.Segments != 3 {
		t.Errorf("want 3 segments in the analysis, got %+v", analysis)
	}

	// preview_mt needs a segment of this job or free text.
	if isErr, text := callTool(t, s, "openloc_preview_mt", map[string]any{"job_id": f.jobA.ID}, nil); !isErr || !strings.Contains(text, "source_text") {
		t.Errorf("preview_mt with no source should explain, got isErr=%v %q", isErr, text)
	}
	foreign, _ := firstTranslation(t, connect(t, f.url, f.tokenB), f.jobB.ID)
	if isErr, text := callTool(t, s, "openloc_get_tm_matches",
		map[string]any{"job_id": f.jobA.ID, "translation_id": foreign}, nil); !isErr {
		t.Error("a segment from another tenant's job was accepted")
	} else {
		assertOpaqueNotFound(t, text)
	}

	other := connect(t, f.url, f.tokenB)
	for tool, args := range map[string]map[string]any{
		"openloc_export_job":       {"job_id": f.jobA.ID},
		"openloc_get_job_analysis": {"job_id": f.jobA.ID},
		"openloc_preview_mt":       {"job_id": f.jobA.ID, "source_text": "x"},
	} {
		isErr, text := callTool(t, other, tool, args, nil)
		if !isErr {
			t.Errorf("%s: another tenant got through", tool)
			continue
		}
		assertOpaqueNotFound(t, text)
	}
}

func TestIntegrationMCPAssignJob(t *testing.T) {
	f := newFixture(t)
	testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, f.jobA.ID, nil)
	// A task of someone else in the same tenant: list_my_tasks is "my work"
	// for every role, so neither the linguist nor the owner may see it as theirs.
	ownersFile := testutil.SeedFile(t, f.q, f.projectA.ID, "owners.txt", db.FileFormatJsonI18n)
	ownersJob := testutil.SeedJob(t, f.q, ownersFile.ID, "de")
	testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, ownersJob.ID, &f.ownerA.ID)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	if isErr, text := callTool(t, s, "openloc_assign_job",
		map[string]any{"job_id": f.jobA.ID, "user_id": f.linguistA.ID}, nil); isErr {
		t.Fatalf("assign_job: %s", text)
	}
	var tasks struct {
		Tasks []struct {
			JobID int64 `json:"job_id"`
		} `json:"tasks"`
	}
	ling := connect(t, f.url, f.tokenLingA)
	if isErr, text := callTool(t, ling, "openloc_list_my_tasks", map[string]any{}, &tasks); isErr {
		t.Fatalf("list_my_tasks: %s", text)
	}
	if len(tasks.Tasks) != 1 || tasks.Tasks[0].JobID != f.jobA.ID {
		t.Errorf("the linguist's tasks must be exactly the assigned job, got %+v", tasks.Tasks)
	}
	if isErr, text := callTool(t, connect(t, f.url, f.tokenA), "openloc_list_my_tasks", map[string]any{}, &tasks); isErr {
		t.Fatalf("list_my_tasks as owner: %s", text)
	}
	if len(tasks.Tasks) != 1 || tasks.Tasks[0].JobID != ownersJob.ID {
		t.Errorf("an owner's list_my_tasks must hold only their own task, got %+v", tasks.Tasks)
	}

	// Read-only token: refused and nothing changes.
	read := connect(t, f.url, f.tokenA)
	first, _ := firstTranslation(t, read, f.jobA.ID)
	for tool, args := range map[string]map[string]any{
		"openloc_assign_job":          {"job_id": f.jobA.ID, "user_id": f.ownerA.ID},
		"openloc_apply_tm":            {"job_id": f.jobA.ID},
		"openloc_advance_step":        {"job_id": f.jobA.ID},
		"openloc_save_drafts_batch":   {"job_id": f.jobA.ID, "drafts": []map[string]any{{"translation_id": first, "text": "x"}}},
		"openloc_confirm_translation": {"translation_id": first, "text": "x"},
		"openloc_delete_comment":      {"translation_id": first, "comment_id": 1},
	} {
		isErr, text := callTool(t, read, tool, args, nil)
		if !isErr || !strings.Contains(text, "write access") {
			t.Errorf("%s: read-only token not refused: isErr=%v %q", tool, isErr, text)
		}
	}
}
