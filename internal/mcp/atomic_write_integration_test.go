package mcp_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/testutil"
)

// A create tool that fails partway leaves nothing behind: the step, the
// language addition, the project's audit row or its customer pick fails after
// the first row is written, and the whole create rolls back, so retrying the
// tool hits no name conflict and makes no duplicate.
//
// Anti-tautology: recording project.created best-effort, or writing the
// customer pick after the create commits, leaves the failed project behind.
func TestIntegrationMCPFailedCreateSavesNothing(t *testing.T) {
	f := newFixture(t)
	step := testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, "Translate", "human", "", nil)
	customer := systemLabelValue(t, f, f.tenantA.ID, "customer", "Atomic Customer")
	profiles := func() int64 {
		return testutil.CountRows(t, f.pool, "SELECT count(*) FROM settings_profiles WHERE tenant_id = $1", f.tenantA.ID)
	}

	cases := []struct {
		name  string
		tool  string
		args  map[string]any
		fail  string // the sqlc query that fails after the row is written
		count func() int
	}{
		{"workflow", "openloc_create_workflow",
			map[string]any{"name": "Half flow", "step_definition_ids": []int64{step.ID}},
			"CreateWorkflowDefinitionStep",
			func() int { return testutil.CountWorkflowsNamed(t, f.q, f.tenantA.ID, "Half flow") }},
		{"AI prompt", "openloc_create_ai_prompt",
			map[string]any{"name": "Half prompt", "mode": "qe", "text": "Rate it.",
				"language_additions": []map[string]any{{"target_languages": []string{"de"}, "text": "Use the formal address."}}},
			"CreateAIPromptLanguageAddition",
			func() int { return testutil.CountAIPromptsNamed(t, f.q, f.tenantA.ID, "Half prompt") }},
		{"project", "openloc_create_project",
			map[string]any{"name": "Half project", "source_language": "en", "target_languages": []string{"de"}},
			"InsertProjectEvent",
			func() int { return projectsNamed(t, f, f.tenantA.ID, "Half project") }},
		{"project with a customer", "openloc_create_project",
			map[string]any{"name": "Half customer project", "source_language": "en", "target_languages": []string{"de"},
				"customer_id": customer.ID},
			"SetProjectLabel",
			func() int { return projectsNamed(t, f, f.tenantA.ID, "Half customer project") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(testutil.NewTestRouter(testutil.FailingPool(t, tc.fail)))
			t.Cleanup(srv.Close)
			s := connect(t, srv.URL, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
			profilesBefore := profiles()
			if isErr, text := callTool(t, s, tc.tool, tc.args, nil); !isErr {
				t.Fatalf("%s succeeded with its insert failing: %s", tc.tool, text)
			}
			if n := tc.count(); n != 0 {
				t.Fatalf("the failed %s left %d row(s) behind", tc.tool, n)
			}
			if n := profiles() - profilesBefore; n != 0 {
				t.Fatalf("the failed %s left %d settings profile(s) behind", tc.tool, n)
			}

			// The same call on a working database lands, so nothing of the
			// failed one is in its way.
			s = connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
			if isErr, text := callTool(t, s, tc.tool, tc.args, nil); isErr {
				t.Fatalf("the retry failed: %s", text)
			}
			if n := tc.count(); n != 1 {
				t.Fatalf("the retried %s saved %d row(s), want 1", tc.tool, n)
			}
		})
	}
}

func projectsNamed(t *testing.T, f *fixture, tenantID int64, name string) int {
	t.Helper()
	return int(testutil.CountRows(t, f.pool,
		"SELECT count(*) FROM projects WHERE tenant_id = $1 AND name = $2", tenantID, name))
}

// A workflow name the library already has is refused with the reason, and the
// library keeps the one workflow of that name.
func TestIntegrationMCPCreateWorkflowNameTaken(t *testing.T) {
	f := newFixture(t)
	step := testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, "Translate", "human", "", nil)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	args := map[string]any{"name": "Taken flow", "step_definition_ids": []int64{step.ID}}

	if isErr, text := callTool(t, s, "openloc_create_workflow", args, nil); isErr {
		t.Fatalf("first create failed: %s", text)
	}
	isErr, text := callTool(t, s, "openloc_create_workflow", args, nil)
	if !isErr || !strings.Contains(text, "already exists") {
		t.Fatalf("second create = error %v, %q; want the name-taken refusal", isErr, text)
	}
	if n := testutil.CountWorkflowsNamed(t, f.q, f.tenantA.ID, "Taken flow"); n != 1 {
		t.Fatalf("%d workflows named Taken flow, want 1", n)
	}
}

// openloc_update_workflow reads the workflow back in the update's transaction:
// when that read fails, the confirmed update answers an error and changes
// nothing; with the read failing only after a commit, it lands and answers the
// new steps.
func TestIntegrationMCPUpdateWorkflowFailedReadBack(t *testing.T) {
	f := newFixture(t)
	step := testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, "Translate", "human", "", nil)
	review := testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, "Review", "human", "", nil)
	token := writeToken(t, f, f.tenantA.ID, f.ownerA.ID)
	s := connect(t, f.url, token)
	var created struct {
		WorkflowID int64 `json:"workflow_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_workflow", map[string]any{"name": "Before flow", "step_definition_ids": []int64{step.ID}}, &created); isErr {
		t.Fatalf("create_workflow: %s", text)
	}
	update := map[string]any{"workflow_id": created.WorkflowID, "name": "After flow",
		"steps": []map[string]any{{"step_definition_id": review.ID}}}
	reads := []string{"ListWorkflowDefinitionSteps"}

	session := func(pool func(testing.TB, ...string) *pgxpool.Pool) *sdk.ClientSession {
		srv := httptest.NewServer(testutil.NewTestRouter(pool(t, reads...)))
		t.Cleanup(srv.Close)
		return connect(t, srv.URL, token)
	}

	// Each preview runs on the working server; each confirmation on a server
	// whose pool fails the reads.
	if isErr, text := confirmedCallOn(t, s, session(testutil.FailingPool), "openloc_update_workflow", update, nil); !isErr {
		t.Fatalf("the update succeeded with its read failing: %s", text)
	}
	if n := testutil.CountWorkflowsNamed(t, f.q, f.tenantA.ID, "After flow"); n != 0 {
		t.Fatalf("the failed update renamed the workflow")
	}

	var detail struct {
		Name  string `json:"name"`
		Steps []struct {
			StepDefinitionID int64 `json:"step_definition_id"`
		} `json:"steps"`
	}
	if isErr, text := confirmedCallOn(t, s, session(testutil.FailingPoolOutsideTx), "openloc_update_workflow", update, &detail); isErr {
		t.Fatalf("the update with only a read after the commit failing: %s", text)
	}
	if detail.Name != "After flow" || len(detail.Steps) != 1 || detail.Steps[0].StepDefinitionID != review.ID {
		t.Fatalf("update answered %+v, want After flow with the Review step", detail)
	}
}
