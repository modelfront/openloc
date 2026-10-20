package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// stepListOut mirrors ProjectWorkflowStepList / SetWorkflowStepDeadlinesResult
// — the same shape comes back from the read and the write, on purpose.
type stepListOut struct {
	ProjectID       int64         `json:"project_id"`
	TargetLanguages []string      `json:"target_languages"`
	Items           []stepItemOut `json:"items"`
	Count           int           `json:"count"`
}

type stepItemOut struct {
	WorkflowStepID    int64  `json:"workflow_step_id"`
	Name              string `json:"name"`
	StepType          string `json:"step_type"`
	AssigneeName      string `json:"assignee_name"`
	Deadline          string `json:"deadline"`
	LanguageDeadlines []struct {
		TargetLanguage string `json:"target_language"`
		Deadline       string `json:"deadline"`
	} `json:"language_deadlines"`
}

// item returns the step named name, failing the test when it is missing — so
// an empty or partial result cannot pass an assertion made on it.
func (o stepListOut) item(t *testing.T, name string) stepItemOut {
	t.Helper()
	for _, it := range o.Items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("no step named %q in %+v", name, o.Items)
	return stepItemOut{}
}

func (o stepListOut) step(t *testing.T, name string) int64 {
	t.Helper()
	return o.item(t, name).WorkflowStepID
}

// seedTwoStepProject gives tenant A a project with de+fr and a Translation →
// Review workflow, and returns it.
func seedTwoStepProject(t *testing.T, f *fixture, name string) db.Project {
	t.Helper()
	ctx := context.Background()
	p := testutil.SeedProject(t, f.q, f.tenantA.ID, name, "en", []string{"de", "fr"})
	for i, stepName := range []string{"Translation", "Review"} {
		// Definitions are tenant-unique by name, and two projects of one tenant
		// share them — look one up before creating it.
		def, err := f.q.GetStepDefinitionByName(ctx, db.GetStepDefinitionByNameParams{TenantID: f.tenantA.ID, Name: stepName})
		if err != nil {
			def = testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, stepName, "human", "", nil)
		}
		// Translation gets a default assignee: setting a date on the OTHER step
		// must not disturb it. patchStepDeadlines rebuilds the WHOLE step list,
		// so a dropped assignee would otherwise go unnoticed.
		var assignee *int64
		if i == 0 {
			assignee = &f.ownerA.ID
		}
		if _, err := f.q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: p.SettingsProfileID, Name: stepName, StepOrder: int32(i),
			RoleRequired: "linguist", StepType: "human", StepDefinitionID: &def.ID,
			DefaultAssignee: assignee,
		}); err != nil {
			t.Fatalf("seed step %s: %v", stepName, err)
		}
	}
	return p
}

// The read tool is what makes the write usable: it is the only surface that
// hands out workflow_step_ids, and it reports the project's target languages
// so the model knows which ones a per-language date may name.
func TestIntegrationMCPListProjectWorkflowSteps(t *testing.T) {
	f := newFixture(t)
	p := seedTwoStepProject(t, f, "Scheduled")

	s := connect(t, f.url, f.tokenA)
	var out stepListOut
	if isErr, text := callTool(t, s, "openloc_list_project_workflow_steps",
		map[string]any{"project_id": p.ID}, &out); isErr {
		t.Fatalf("list steps failed: %s", text)
	}
	if out.Count != 2 || out.Items[0].Name != "Translation" || out.Items[1].Name != "Review" {
		t.Fatalf("expected Translation then Review, got %+v", out.Items)
	}
	if len(out.TargetLanguages) != 2 {
		t.Errorf("target_languages = %v, want the project's two", out.TargetLanguages)
	}
	if out.Items[0].Deadline != "" || len(out.Items[0].LanguageDeadlines) != 0 {
		t.Errorf("a fresh project should have no dates, got %+v", out.Items[0])
	}

	// Another tenant's project is a not-found, not a peek into its workflow.
	if isErr, _ := callTool(t, s, "openloc_list_project_workflow_steps",
		map[string]any{"project_id": f.projectB.ID}, nil); !isErr {
		t.Fatal("tenant A could read tenant B's workflow steps")
	}
}

// The write sets a step-level date and a per-language one, and reports back
// exactly what a follow-up read would show.
func TestIntegrationMCPSetWorkflowStepDeadlines(t *testing.T) {
	f := newFixture(t)
	p := seedTwoStepProject(t, f, "Datable")

	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	var listed stepListOut
	if isErr, text := callTool(t, s, "openloc_list_project_workflow_steps",
		map[string]any{"project_id": p.ID}, &listed); isErr {
		t.Fatalf("list steps failed: %s", text)
	}
	translation := listed.step(t, "Translation")
	review := listed.step(t, "Review")

	stepDL := "2027-06-01T17:00:00Z"
	deDL := "2027-05-20T09:00:00Z"
	reviewDL := "2027-06-05T17:00:00Z"
	var out stepListOut
	if isErr, text := callTool(t, s, "openloc_set_workflow_step_deadlines", map[string]any{
		"project_id": p.ID,
		"deadlines": []map[string]any{
			{"workflow_step_id": translation, "deadline": stepDL},
			{"workflow_step_id": translation, "target_language": "de", "deadline": deDL},
			{"workflow_step_id": review, "deadline": reviewDL},
		},
	}, &out); isErr {
		t.Fatalf("set deadlines failed: %s", text)
	}

	if tr := out.item(t, "Translation"); tr.Deadline != stepDL {
		t.Errorf("Translation step deadline = %q, want %q", tr.Deadline, stepDL)
	} else if len(tr.LanguageDeadlines) != 1 || tr.LanguageDeadlines[0].TargetLanguage != "de" ||
		tr.LanguageDeadlines[0].Deadline != deDL {
		t.Errorf("Translation language deadlines = %+v, want de=%s", tr.LanguageDeadlines, deDL)
	}
	if rv := out.item(t, "Review"); rv.Deadline != reviewDL {
		t.Errorf("Review deadline = %q, want %q", rv.Deadline, reviewDL)
	}

	// A second call naming only Review must leave Translation's dates alone —
	// this is a patch, so one tool call cannot silently wipe the rest.
	if isErr, text := callTool(t, s, "openloc_set_workflow_step_deadlines", map[string]any{
		"project_id": p.ID,
		"deadlines":  []map[string]any{{"workflow_step_id": review, "deadline": "2027-07-01T17:00:00Z"}},
	}, &out); isErr {
		t.Fatalf("second set failed: %s", text)
	}
	tr := out.item(t, "Translation")
	if tr.Deadline != stepDL || len(tr.LanguageDeadlines) != 1 {
		t.Errorf("patching Review disturbed Translation's dates: %+v", tr)
	}
	// Identity and assignment survive the full-list rebuild too.
	if tr.AssigneeName == "" {
		t.Errorf("patching Review dropped Translation's default assignee: %+v", tr)
	}

	// An empty deadline clears one language without touching the step's own.
	if isErr, text := callTool(t, s, "openloc_set_workflow_step_deadlines", map[string]any{
		"project_id": p.ID,
		"deadlines":  []map[string]any{{"workflow_step_id": translation, "target_language": "de", "deadline": ""}},
	}, &out); isErr {
		t.Fatalf("clear failed: %s", text)
	}
	if tr := out.item(t, "Translation"); len(tr.LanguageDeadlines) != 0 || tr.Deadline != stepDL {
		t.Errorf("clearing de should leave the step date alone: %+v", tr)
	}
}

// The deadline write is manager + write, tenant-scoped, and refuses a language
// the project does not translate into — the same rules the REST path applies.
func TestIntegrationMCPSetWorkflowStepDeadlinesGates(t *testing.T) {
	f := newFixture(t)
	p := seedTwoStepProject(t, f, "Gated")

	write := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	var listed stepListOut
	if isErr, text := callTool(t, write, "openloc_list_project_workflow_steps",
		map[string]any{"project_id": p.ID}, &listed); isErr {
		t.Fatalf("list steps failed: %s", text)
	}
	translation := listed.step(t, "Translation")
	deadline := "2027-06-01T17:00:00Z"

	// A read-only token may not schedule.
	if isErr, text := callTool(t, connect(t, f.url, f.tokenA), "openloc_set_workflow_step_deadlines", map[string]any{
		"project_id": p.ID,
		"deadlines":  []map[string]any{{"workflow_step_id": translation, "deadline": deadline}},
	}, nil); !isErr {
		t.Fatalf("read-only token was allowed to set deadlines: %s", text)
	}

	// A language outside the project's targets is refused.
	if isErr, text := callTool(t, write, "openloc_set_workflow_step_deadlines", map[string]any{
		"project_id": p.ID,
		"deadlines":  []map[string]any{{"workflow_step_id": translation, "target_language": "ja", "deadline": deadline}},
	}, nil); !isErr || !strings.Contains(text, "target languages") {
		t.Fatalf("unknown target language accepted: %v %s", isErr, text)
	}

	// Another tenant's project is a not-found, even with a step id that really
	// is part of that project — so only the tenant check can refuse it.
	ctx := context.Background()
	bDL := time.Date(2027, 2, 2, 12, 0, 0, 0, time.UTC)
	bStep, err := f.q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: f.projectB.SettingsProfileID, Name: "Translation", StepOrder: 0,
		RoleRequired: "linguist", StepType: "human", Deadline: pgtype.Timestamptz{Time: bDL, Valid: true},
	})
	if err != nil {
		t.Fatalf("seed tenant B step: %v", err)
	}
	if _, err := f.q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: f.jobB.ID, WorkflowStepID: bStep.ID, Status: "in_progress",
		Deadline: pgtype.Timestamptz{Time: bDL, Valid: true},
	}); err != nil {
		t.Fatalf("seed tenant B assignment: %v", err)
	}
	if isErr, text := callTool(t, write, "openloc_set_workflow_step_deadlines", map[string]any{
		"project_id": f.projectB.ID,
		"deadlines":  []map[string]any{{"workflow_step_id": bStep.ID, "deadline": deadline}},
	}, nil); !isErr || !strings.Contains(text, "not found") {
		t.Fatalf("tenant A scheduling tenant B's project: want a not-found, got %v %s", isErr, text)
	}
	bSteps, err := f.q.ListSettingsWorkflowSteps(ctx, f.projectB.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	stepSeen := false
	for _, st := range bSteps {
		if st.ID != bStep.ID {
			continue
		}
		stepSeen = true
		if !st.Deadline.Valid || !st.Deadline.Time.Equal(bDL) {
			t.Errorf("tenant B step deadline = %v, want it untouched at %v", st.Deadline, bDL)
		}
	}
	if !stepSeen {
		t.Errorf("tenant B step %d is gone from its profile", bStep.ID)
	}
	bRows, err := f.q.ListJobStepAssignments(ctx, f.jobB.ID)
	if err != nil {
		t.Fatal(err)
	}
	rowSeen := false
	for _, r := range bRows {
		if r.WorkflowStepID != bStep.ID {
			continue
		}
		rowSeen = true
		if !r.Deadline.Valid || !r.Deadline.Time.Equal(bDL) {
			t.Errorf("tenant B assignment deadline = %v, want it untouched at %v", r.Deadline, bDL)
		}
	}
	if !rowSeen {
		t.Errorf("tenant B assignment on step %d is gone", bStep.ID)
	}

	// A step id from a different project of the SAME tenant is refused too —
	// tenant scoping alone would let it through.
	other := seedTwoStepProject(t, f, "Neighbour")
	var otherSteps stepListOut
	if isErr, _ := callTool(t, write, "openloc_list_project_workflow_steps",
		map[string]any{"project_id": other.ID}, &otherSteps); isErr {
		t.Fatal("list neighbour steps failed")
	}
	if isErr, text := callTool(t, write, "openloc_set_workflow_step_deadlines", map[string]any{
		"project_id": p.ID,
		"deadlines":  []map[string]any{{"workflow_step_id": otherSteps.step(t, "Translation"), "deadline": deadline}},
	}, nil); !isErr || !strings.Contains(text, "not part of this project") {
		t.Fatalf("a step from another project was accepted: %v %s", isErr, text)
	}
}

// Setting a deadline over MCP reaches the jobs that already exist, resolved
// per target language — the same propagation the REST path does.
func TestIntegrationMCPSetWorkflowStepDeadlinesReachesJobs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := seedTwoStepProject(t, f, "Live")
	file := testutil.SeedFile(t, f.q, p.ID, "live.txt", db.FileFormatTxt)
	deJob := testutil.SeedJob(t, f.q, file.ID, "de")
	frJob := testutil.SeedJob(t, f.q, file.ID, "fr")

	steps, err := f.q.ListSettingsWorkflowSteps(ctx, p.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	for _, jobID := range []int64{deJob.ID, frJob.ID} {
		if _, err := f.q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
			JobID: jobID, WorkflowStepID: steps[0].ID, Status: "in_progress",
		}); err != nil {
			t.Fatal(err)
		}
	}

	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	if isErr, text := callTool(t, s, "openloc_set_workflow_step_deadlines", map[string]any{
		"project_id": p.ID,
		"deadlines": []map[string]any{
			{"workflow_step_id": steps[0].ID, "deadline": "2027-06-01T17:00:00Z"},
			{"workflow_step_id": steps[0].ID, "target_language": "de", "deadline": "2027-05-20T09:00:00Z"},
		},
	}, nil); isErr {
		t.Fatalf("set deadlines failed: %s", text)
	}

	want := map[int64]time.Time{
		deJob.ID: time.Date(2027, 5, 20, 9, 0, 0, 0, time.UTC),
		frJob.ID: time.Date(2027, 6, 1, 17, 0, 0, 0, time.UTC),
	}
	for jobID, wantDL := range want {
		rows, err := f.q.ListJobStepAssignments(ctx, jobID)
		if err != nil || len(rows) != 1 {
			t.Fatalf("job %d assignments: %+v (err=%v)", jobID, rows, err)
		}
		if !rows[0].Deadline.Valid || !rows[0].Deadline.Time.Equal(wantDL) {
			t.Errorf("job %d deadline = %v, want %v", jobID, rows[0].Deadline, wantDL)
		}
	}
}

// Replacing the workflow to change an assignee keeps each step's
// per-language dates unless the call names them: the caller may never have
// seen them, and dropping them silently moves real deadlines.
func TestIntegrationMCPSetProjectWorkflowStepsKeepsLanguageDeadlines(t *testing.T) {
	f := newFixture(t)
	p := seedTwoStepProject(t, f, "Kept dates")
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	list := func() stepListOut {
		t.Helper()
		var out stepListOut
		if isErr, text := callTool(t, s, "openloc_list_project_workflow_steps", map[string]any{"project_id": p.ID}, &out); isErr {
			t.Fatalf("list steps failed: %s", text)
		}
		return out
	}
	listed := list()
	translation, review := listed.step(t, "Translation"), listed.step(t, "Review")
	deDL := "2027-05-20T09:00:00Z"
	if isErr, text := callTool(t, s, "openloc_set_workflow_step_deadlines", map[string]any{
		"project_id": p.ID,
		"deadlines":  []map[string]any{{"workflow_step_id": translation, "target_language": "de", "deadline": deDL}},
	}, nil); isErr {
		t.Fatalf("set deadlines failed: %s", text)
	}

	// Only Review's assignee changes; Translation's German date is not named.
	steps := []map[string]any{
		{"existing_step_id": translation, "default_assignee": f.ownerA.ID},
		{"existing_step_id": review, "default_assignee": f.ownerA.ID},
	}
	if isErr, text := confirmedCall(t, s, "openloc_set_project_workflow_steps", map[string]any{"project_id": p.ID, "steps": steps}, nil); isErr {
		t.Fatalf("set steps: %s", text)
	}
	if tr := list().item(t, "Translation"); len(tr.LanguageDeadlines) != 1 || tr.LanguageDeadlines[0].Deadline != deDL {
		t.Fatalf("changing an assignee dropped Translation's German deadline: %+v", tr.LanguageDeadlines)
	}

	// Named, they are replaced: an empty list clears them.
	steps[0]["language_deadlines"] = []map[string]any{}
	if isErr, text := confirmedCall(t, s, "openloc_set_project_workflow_steps", map[string]any{"project_id": p.ID, "steps": steps}, nil); isErr {
		t.Fatalf("set steps with an empty list: %s", text)
	}
	if tr := list().item(t, "Translation"); len(tr.LanguageDeadlines) != 0 {
		t.Errorf("an empty language_deadlines list did not clear them: %+v", tr.LanguageDeadlines)
	}
}
