// list_projects, list_jobs and list_my_tasks over the real protocol: the
// customer comes from the Customer label, the status filter takes the real
// statuses, the project list pages with a cursor, both lists carry the workflow
// steps, and "my tasks" includes the caller's steps that have not started.
package mcp_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/label"
	"openloc/internal/testutil"
)

// systemLabelValue adds a value to one of the tenant's built-in labels
// ("customer" or "domain").
func systemLabelValue(t *testing.T, f *fixture, tenantID int64, key, name string) label.Value {
	t.Helper()
	ctx := context.Background()
	labels, err := label.List(ctx, f.q, tenantID)
	if err != nil {
		t.Fatalf("list labels: %v", err)
	}
	for _, l := range labels {
		if l.SystemKey == key {
			v, err := label.AddValue(ctx, f.q, tenantID, l.ID, name)
			if err != nil {
				t.Fatalf("add %s value: %v", key, err)
			}
			return v
		}
	}
	t.Fatalf("tenant has no %s label", key)
	return label.Value{}
}

func pickLabels(t *testing.T, f *fixture, tenantID, projectID int64, valueIDs ...int64) {
	t.Helper()
	if _, err := label.SetProjectPicks(context.Background(), f.pool, f.q, tenantID, projectID, valueIDs); err != nil {
		t.Fatalf("set project labels: %v", err)
	}
}

// settingsProfile returns a project's settings profile id, for SeedWorkflow.
func settingsProfile(t *testing.T, f *fixture, tenantID, projectID int64) int64 {
	t.Helper()
	p, err := f.q.GetProject(context.Background(), db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	return p.SettingsProfileID
}

type projectRow struct {
	ID            int64    `json:"id"`
	Status        string   `json:"status"`
	CustomerName  string   `json:"customer_name"`
	CustomerID    *int64   `json:"customer_id"`
	WorkflowSteps []string `json:"workflow_steps"`
	StepCount     int      `json:"step_count"`
}

type projectPage struct {
	Projects   []projectRow `json:"projects"`
	Count      int          `json:"count"`
	NextCursor int64        `json:"next_cursor"`
}

func findProject(t *testing.T, rows []projectRow, id int64) projectRow {
	t.Helper()
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("project %d missing from %+v", id, rows)
	return projectRow{}
}

// The Customer label is where a project's customer lives. projects.customer_id
// is a frozen copy, so a project whose label and frozen column disagree must
// report the label.
func TestIntegrationMCPProjectCustomerComesFromTheLabel(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	stale, err := f.q.CreateCustomer(ctx, db.CreateCustomerParams{TenantID: f.tenantA.ID, Name: "Frozen Name"})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, err := f.q.UpdateProjectMetadata(ctx, db.UpdateProjectMetadataParams{
		ID: f.projectA.ID, TenantID: f.tenantA.ID, CustomerID: &stale.ID,
	}); err != nil {
		t.Fatalf("attach frozen customer: %v", err)
	}
	cust := systemLabelValue(t, f, f.tenantA.ID, "customer", "Fake Customer")
	pickLabels(t, f, f.tenantA.ID, f.projectA.ID, cust.ID)
	// A project whose customer is only a label value, with no
	// projects.customer_id.
	labelOnly := testutil.SeedProject(t, f.q, f.tenantA.ID, "Label Only", "en", []string{"de"})
	pickLabels(t, f, f.tenantA.ID, labelOnly.ID, cust.ID)
	bare := testutil.SeedProject(t, f.q, f.tenantA.ID, "No Customer", "en", []string{"de"})

	s := connect(t, f.url, f.tokenA)

	var page projectPage
	if isErr, text := callTool(t, s, "openloc_list_projects", nil, &page); isErr {
		t.Fatalf("list_projects: %s", text)
	}
	for _, id := range []int64{f.projectA.ID, labelOnly.ID} {
		got := findProject(t, page.Projects, id)
		if got.CustomerName != "Fake Customer" {
			t.Errorf("project %d customer_name = %q, want the label's %q", id, got.CustomerName, "Fake Customer")
		}
		if got.CustomerID == nil || *got.CustomerID != cust.ID {
			t.Errorf("project %d customer_id = %v, want the label value %d", id, got.CustomerID, cust.ID)
		}
	}
	if got := findProject(t, page.Projects, bare.ID); got.CustomerName != "" || got.CustomerID != nil {
		t.Errorf("project without a customer reports %q / %v", got.CustomerName, got.CustomerID)
	}

	var detail projectRow
	if isErr, text := callTool(t, s, "openloc_get_project", map[string]any{"project_id": labelOnly.ID}, &detail); isErr {
		t.Fatalf("get_project: %s", text)
	}
	if detail.CustomerName != "Fake Customer" || detail.CustomerID == nil || *detail.CustomerID != cust.ID {
		t.Errorf("get_project customer = %q / %v, want %q / %d", detail.CustomerName, detail.CustomerID, "Fake Customer", cust.ID)
	}
	// update_project answers with the same label pick, not the frozen column.
	var updated projectRow
	if isErr, text := callTool(t, connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID)), "openloc_update_project",
		map[string]any{"project_id": f.projectA.ID, "name": "Alpha Renamed"}, &updated); isErr {
		t.Fatalf("update_project: %s", text)
	}
	if updated.CustomerName != "Fake Customer" || updated.CustomerID == nil || *updated.CustomerID != cust.ID {
		t.Errorf("update_project customer = %q / %v, want the label's %q / %d", updated.CustomerName, updated.CustomerID, "Fake Customer", cust.ID)
	}

	// The filter takes the label value, so both projects match and the bare one does not.
	if isErr, text := callTool(t, s, "openloc_list_projects", map[string]any{"customer_id": cust.ID}, &page); isErr {
		t.Fatalf("list_projects by customer: %s", text)
	}
	var ids []int64
	for _, p := range page.Projects {
		ids = append(ids, p.ID)
	}
	slices.Sort(ids)
	want := []int64{f.projectA.ID, labelOnly.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Errorf("customer filter returned %v, want %v", ids, want)
	}

	// Tenant B passing tenant A's value id sees nothing of tenant A.
	bSession := connect(t, f.url, f.tokenB)
	if isErr, text := callTool(t, bSession, "openloc_list_projects", map[string]any{"customer_id": cust.ID}, &page); isErr {
		t.Fatalf("tenant B list_projects: %s", text)
	}
	if page.Count != 0 {
		t.Errorf("tenant B filtered on tenant A's customer and got %+v", page.Projects)
	}
}

// The status filter takes the statuses a project really has. An unknown one is
// an error the model can correct, never an empty list that reads as "none".
func TestIntegrationMCPListProjectsStatusFilter(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	held := testutil.SeedProject(t, f.q, f.tenantA.ID, "Held", "en", []string{"de"})
	status := "on_hold"
	if _, err := f.q.UpdateProjectMetadata(ctx, db.UpdateProjectMetadataParams{
		ID: held.ID, TenantID: f.tenantA.ID, Status: db.NullProjectStatus{ProjectStatus: db.ProjectStatus(status), Valid: true},
	}); err != nil {
		t.Fatalf("put project on hold: %v", err)
	}
	s := connect(t, f.url, f.tokenA)

	var page projectPage
	if isErr, text := callTool(t, s, "openloc_list_projects", map[string]any{"status": "on_hold"}, &page); isErr {
		t.Fatalf("status on_hold: %s", text)
	}
	if page.Count != 1 || page.Projects[0].ID != held.ID {
		t.Errorf("on_hold returned %+v, want only project %d", page.Projects, held.ID)
	}

	isErr, text := callTool(t, s, "openloc_list_projects", map[string]any{"status": "active"}, &page)
	if !isErr {
		t.Fatalf("status \"active\" accepted and returned %+v", page.Projects)
	}
	for _, want := range []string{"draft", "in_progress", "completed", "on_hold"} {
		if !strings.Contains(text, want) {
			t.Errorf("error %q does not name the valid status %q", text, want)
		}
	}
}

// The language and name filters each narrow the list to what they name, and
// stay inside the tenant.
func TestIntegrationMCPListProjectsLanguageAndNameFilters(t *testing.T) {
	f := newFixture(t)
	brochure := testutil.SeedProject(t, f.q, f.tenantA.ID, "Gamma Brochure", "fr", []string{"en"})
	italian := testutil.SeedProject(t, f.q, f.tenantA.ID, "Delta Manual", "en", []string{"it", "es"})
	testutil.SeedProject(t, f.q, f.tenantB.ID, "Brochure Elsewhere", "fr", []string{"it"})
	s := connect(t, f.url, f.tokenA)

	for name, tc := range map[string]struct {
		args map[string]any
		want []int64
	}{
		"source_language": {map[string]any{"source_language": "FR"}, []int64{brochure.ID}},
		"target_language": {map[string]any{"target_language": "it"}, []int64{italian.ID}},
		"query":           {map[string]any{"query": "brochure"}, []int64{brochure.ID}},
	} {
		t.Run(name, func(t *testing.T) {
			var page projectPage
			if isErr, text := callTool(t, s, "openloc_list_projects", tc.args, &page); isErr {
				t.Fatalf("list_projects %v: %s", tc.args, text)
			}
			var ids []int64
			for _, p := range page.Projects {
				ids = append(ids, p.ID)
			}
			if !slices.Equal(ids, tc.want) {
				t.Errorf("list_projects %v = %v, want %v", tc.args, ids, tc.want)
			}
		})
	}
}

// Every project of the tenant is reachable by following next_cursor, each
// once, and the last page says it is the last.
func TestIntegrationMCPListProjectsPages(t *testing.T) {
	f := newFixture(t)
	want := map[int64]bool{f.projectA.ID: true}
	for _, name := range []string{"P1", "P2", "P3", "P4"} {
		want[testutil.SeedProject(t, f.q, f.tenantA.ID, name, "en", []string{"de"}).ID] = true
	}
	s := connect(t, f.url, f.tokenA)

	seen := map[int64]int{}
	cursor := int64(0)
	for calls := 0; ; calls++ {
		if calls > 10 {
			t.Fatal("paging never ended")
		}
		args := map[string]any{"limit": 2}
		if cursor != 0 {
			args["cursor"] = cursor
		}
		var page projectPage
		if isErr, text := callTool(t, s, "openloc_list_projects", args, &page); isErr {
			t.Fatalf("page %d: %s", calls, text)
		}
		if len(page.Projects) > 2 {
			t.Fatalf("page %d has %d rows, limit 2", calls, len(page.Projects))
		}
		for _, p := range page.Projects {
			seen[p.ID]++
		}
		if page.NextCursor == 0 {
			break
		}
		cursor = page.NextCursor
	}
	for id := range want {
		if seen[id] != 1 {
			t.Errorf("project %d seen %d times, want once", id, seen[id])
		}
	}
	if seen[f.projectB.ID] != 0 {
		t.Error("tenant B's project appeared in tenant A's pages")
	}
	if len(seen) != len(want) {
		t.Errorf("saw %d projects, want %d", len(seen), len(want))
	}
}

// A project's workflow rides the list, so "which projects have more than one
// step?" is one call.
func TestIntegrationMCPListProjectsCarryWorkflowSteps(t *testing.T) {
	f := newFixture(t)
	testutil.SeedWorkflow(t, f.q, settingsProfile(t, f, f.tenantA.ID, f.projectA.ID), f.jobA.ID,
		testutil.WorkflowStepSpec{Name: "Translation"},
		testutil.WorkflowStepSpec{Name: "Review"},
	)
	none := testutil.SeedProject(t, f.q, f.tenantA.ID, "No Steps", "en", []string{"de"})
	s := connect(t, f.url, f.tokenA)

	var page projectPage
	if isErr, text := callTool(t, s, "openloc_list_projects", nil, &page); isErr {
		t.Fatalf("list_projects: %s", text)
	}
	got := findProject(t, page.Projects, f.projectA.ID)
	if !slices.Equal(got.WorkflowSteps, []string{"Translation", "Review"}) || got.StepCount != 2 {
		t.Errorf("workflow = %v (%d), want [Translation Review] (2)", got.WorkflowSteps, got.StepCount)
	}
	if got := findProject(t, page.Projects, none.ID); got.WorkflowSteps == nil || len(got.WorkflowSteps) != 0 || got.StepCount != 0 {
		t.Errorf("project without steps = %v (%d), want [] (0)", got.WorkflowSteps, got.StepCount)
	}
}

type jobStepRow struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	AssigneeID   *int64 `json:"assignee_id"`
	AssigneeName string `json:"assignee_name"`
}

type jobRowWithSteps struct {
	ID    int64        `json:"id"`
	Steps []jobStepRow `json:"steps"`
}

// Every job carries all its steps with their assignees, whoever they are
// assigned to, so a manager sees other people's work without a call per job.
func TestIntegrationMCPListJobsCarrySteps(t *testing.T) {
	f := newFixture(t)
	testutil.SeedWorkflow(t, f.q, settingsProfile(t, f, f.tenantA.ID, f.projectA.ID), f.jobA.ID,
		testutil.WorkflowStepSpec{Name: "Translation", Assignee: &f.linguistA.ID, Status: "completed"},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &f.ownerA.ID, Status: "in_progress"},
	)
	other := testutil.SeedProject(t, f.q, f.tenantA.ID, "Unassigned", "en", []string{"fr"})
	otherJob := seedJobWithSegments(t, f.q, other.ID, "other.txt", "fr", []string{"One"})
	s := connect(t, f.url, f.tokenA)

	var out struct {
		Jobs []jobRowWithSteps `json:"jobs"`
	}
	if isErr, text := callTool(t, s, "openloc_list_jobs", map[string]any{}, &out); isErr {
		t.Fatalf("list_jobs: %s", text)
	}
	var jobA *jobRowWithSteps
	for i := range out.Jobs {
		if out.Jobs[i].ID == f.jobA.ID {
			jobA = &out.Jobs[i]
		}
	}
	if jobA == nil {
		t.Fatalf("jobA missing from %+v", out.Jobs)
	}
	if len(jobA.Steps) != 2 {
		t.Fatalf("jobA steps = %+v, want Translation and Review", jobA.Steps)
	}
	tr, rv := jobA.Steps[0], jobA.Steps[1]
	if tr.Name != "Translation" || tr.Status != "completed" || tr.AssigneeID == nil || *tr.AssigneeID != f.linguistA.ID || tr.AssigneeName == "" {
		t.Errorf("first step = %+v, want Translation, completed, the linguist", tr)
	}
	if rv.Name != "Review" || rv.Status != "in_progress" || rv.AssigneeID == nil || *rv.AssigneeID != f.ownerA.ID {
		t.Errorf("second step = %+v, want Review, in_progress, the owner", rv)
	}

	// get_job carries the same steps.
	var one jobRowWithSteps
	if isErr, text := callTool(t, s, "openloc_get_job", map[string]any{"job_id": f.jobA.ID}, &one); isErr {
		t.Fatalf("get_job: %s", text)
	}
	if len(one.Steps) != 2 || one.Steps[0].Name != "Translation" || one.Steps[1].Name != "Review" ||
		one.Steps[0].AssigneeID == nil || *one.Steps[0].AssigneeID != f.linguistA.ID {
		t.Errorf("get_job steps = %+v, want Translation (the linguist), Review", one.Steps)
	}

	// assignee_id keeps the jobs with a step assigned to that person.
	if isErr, text := callTool(t, s, "openloc_list_jobs", map[string]any{"assignee_id": f.linguistA.ID}, &out); isErr {
		t.Fatalf("list_jobs by assignee: %s", text)
	}
	if len(out.Jobs) != 1 || out.Jobs[0].ID != f.jobA.ID {
		t.Errorf("assignee filter returned %+v, want only job %d (not %d)", out.Jobs, f.jobA.ID, otherJob.ID)
	}
}

// create_project's customer is a Customer label value: it lands as the
// project's pick, and a value of another label, or another tenant's, is refused
// before anything is created.
func TestIntegrationMCPCreateProjectSetsCustomerLabel(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cust := systemLabelValue(t, f, f.tenantA.ID, "customer", "Fake Customer")
	foreign := systemLabelValue(t, f, f.tenantB.ID, "customer", "Their Customer")
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var out struct {
		ProjectID int64 `json:"project_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_project", map[string]any{
		"name": "With Customer", "source_language": "en", "target_languages": []string{"de"}, "customer_id": cust.ID,
	}, &out); isErr {
		t.Fatalf("create_project: %s", text)
	}
	picks, err := label.ProjectPicks(ctx, f.q, f.tenantA.ID, out.ProjectID)
	if err != nil {
		t.Fatalf("read picks: %v", err)
	}
	if len(picks) != 1 || picks[0].LabelValueID != cust.ID {
		t.Errorf("picks = %+v, want the Customer value %d", picks, cust.ID)
	}

	before := projectCount(t, f, f.tenantA.ID)
	domain := systemLabelValue(t, f, f.tenantA.ID, "domain", "Legal")
	for name, id := range map[string]int64{"a Domain value": domain.ID, "another tenant's customer": foreign.ID} {
		if isErr, text := callTool(t, s, "openloc_create_project", map[string]any{
			"name": "Refused", "source_language": "en", "target_languages": []string{"de"}, "customer_id": id,
		}, nil); !isErr {
			t.Errorf("%s accepted as customer_id", name)
		} else if !strings.Contains(text, "customer") {
			t.Errorf("%s refused with %q, want a message about the customer", name, text)
		}
	}
	if after := projectCount(t, f, f.tenantA.ID); after != before {
		t.Errorf("refused calls created %d project(s)", after-before)
	}
}

func projectCount(t *testing.T, f *fixture, tenantID int64) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM projects WHERE tenant_id = $1`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	return n
}

type myTasks struct {
	Tasks []struct {
		JobID    int64  `json:"job_id"`
		StepName string `json:"step_name"`
	} `json:"tasks"`
	Upcoming []struct {
		JobID       int64  `json:"job_id"`
		StepName    string `json:"step_name"`
		CurrentStep string `json:"current_step"`
	} `json:"upcoming"`
}

// A step assigned to you that has not started yet is your work too: "am I on
// any step other than Translation?" must see it, and only your own.
func TestIntegrationMCPListMyTasksShowsUpcomingSteps(t *testing.T) {
	f := newFixture(t)
	testutil.SeedWorkflow(t, f.q, settingsProfile(t, f, f.tenantA.ID, f.projectA.ID), f.jobA.ID,
		testutil.WorkflowStepSpec{Name: "Translation", Assignee: &f.linguistA.ID, Status: "in_progress"},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &f.ownerA.ID},
	)
	// A step the owner already finished is not upcoming.
	done := testutil.SeedProject(t, f.q, f.tenantA.ID, "Finished", "en", []string{"fr"})
	doneJob := seedJobWithSegments(t, f.q, done.ID, "done.txt", "fr", []string{"One"})
	testutil.SeedWorkflow(t, f.q, settingsProfile(t, f, f.tenantA.ID, done.ID), doneJob.ID,
		testutil.WorkflowStepSpec{Name: "Proofread", Assignee: &f.ownerA.ID, Status: "completed"},
	)
	// Nor is a pending step left on a job that is already completed.
	closed := testutil.SeedProject(t, f.q, f.tenantA.ID, "Closed", "en", []string{"it"})
	closedJob := seedJobWithSegments(t, f.q, closed.ID, "closed.txt", "it", []string{"One"})
	testutil.SeedWorkflow(t, f.q, settingsProfile(t, f, f.tenantA.ID, closed.ID), closedJob.ID,
		testutil.WorkflowStepSpec{Name: "Sign-off", Assignee: &f.ownerA.ID},
	)
	if err := f.q.UpdateJobStatus(context.Background(), db.UpdateJobStatusParams{ID: closedJob.ID, Status: "completed"}); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	var owner myTasks
	if isErr, text := callTool(t, connect(t, f.url, f.tokenA), "openloc_list_my_tasks", nil, &owner); isErr {
		t.Fatalf("owner list_my_tasks: %s", text)
	}
	if len(owner.Tasks) != 0 {
		t.Errorf("owner has no active step, got tasks %+v", owner.Tasks)
	}
	if len(owner.Upcoming) != 1 || owner.Upcoming[0].JobID != f.jobA.ID ||
		owner.Upcoming[0].StepName != "Review" || owner.Upcoming[0].CurrentStep != "Translation" {
		t.Errorf("owner upcoming = %+v, want job %d Review, waiting for Translation", owner.Upcoming, f.jobA.ID)
	}

	var linguist myTasks
	if isErr, text := callTool(t, connect(t, f.url, f.tokenLingA), "openloc_list_my_tasks", nil, &linguist); isErr {
		t.Fatalf("linguist list_my_tasks: %s", text)
	}
	if len(linguist.Tasks) != 1 || linguist.Tasks[0].StepName != "Translation" {
		t.Errorf("linguist tasks = %+v, want the Translation step", linguist.Tasks)
	}
	if linguist.Upcoming == nil || len(linguist.Upcoming) != 0 {
		t.Errorf("linguist upcoming = %+v, want [] (the Review step is the owner's)", linguist.Upcoming)
	}

	// One person in two organizations: the owner, signed in to tenant B, sees
	// none of their tenant-A steps. Only the tenant condition keeps them out,
	// since the steps are theirs.
	testutil.SeedMembership(t, f.q, f.ownerA.ID, f.tenantB.ID, "owner")
	ownerInB := testutil.SeedAPIToken(t, f.q, f.tenantB.ID, f.ownerA.ID, auth.CapabilityRead, time.Hour)
	var other myTasks
	if isErr, text := callTool(t, connect(t, f.url, ownerInB), "openloc_list_my_tasks", nil, &other); isErr {
		t.Fatalf("owner in tenant B list_my_tasks: %s", text)
	}
	if len(other.Tasks)+len(other.Upcoming) != 0 {
		t.Errorf("the owner signed in to tenant B saw tenant A's steps: %+v", other)
	}
}
