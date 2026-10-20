package publicapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/testutil"
)

// wantFailedRead sends the request through a router whose pool fails the
// reads (open says where), and checks that it answers 500 and that its log
// line names the failed read, which fails with "context canceled".
func (f *fixture) wantFailedRead(t *testing.T, open func(testing.TB, ...string) *pgxpool.Pool, reads []string, app bool, method, path string, body any) {
	t.Helper()
	logs := testutil.CaptureLogs(t)
	wantStatus(t, f.sendAs(t, testutil.NewTestRouter(open(t, reads...)), app, false, method, path, body), http.StatusInternalServerError)
	for _, rec := range testutil.ErrorRecords(t, logs) {
		msg, _ := rec[logging.KeyErrorMessage].(string)
		if rec[logging.KeyEventAction] == "http-request-error" && strings.Contains(msg, context.Canceled.Error()) {
			return
		}
	}
	t.Errorf("the 500 was logged without the failed read as its cause: %v", testutil.ErrorRecords(t, logs))
}

// nested names the one row a create sends in a list: the list's key in the
// response, and a field of the row with the value it must come back with.
type nested struct {
	list, field string
	want        any
}

// heldBy reports whether the response's data carries exactly that one row.
func (n nested) heldBy(t *testing.T, rec *httptest.ResponseRecorder) bool {
	t.Helper()
	rows, _ := testutil.DecodeData[map[string]any](t, rec)[n.list].([]any)
	if len(rows) != 1 {
		return false
	}
	row, _ := rows[0].(map[string]any)
	return row[n.field] == n.want
}

// The sqlc queries that read each kind of item back for its response.
var (
	conceptReads  = []string{"GetConcept", "ListTermsForConcepts"}
	promptReads   = []string{"ListAIPromptLanguageAdditions", "ListAIPromptLanguageAdditionsForPrompt"}
	workflowReads = []string{"ListWorkflowDefinitionSteps"}
)

// nestedRows returns the one workflow step and one prompt language addition a
// write sends, and the rows its answer must carry back.
func (f *fixture) nestedRows() (steps, additions []map[string]any, step, addition nested) {
	steps = []map[string]any{{"step_definition_id": f.ids["stepDefinitionID"]}}
	additions = []map[string]any{{"target_languages": []string{"de"}, "text": "Use the formal address."}}
	return steps, additions,
		nested{"steps", "step_definition_id", float64(f.ids["stepDefinitionID"])},
		nested{"language_additions", "text", "Use the formal address."}
}

// sendAs sends the request as the owner: with a session on an app route, else
// with an access key on a v1 route. Only validate sends go through the
// validating client, since the spec documents no plain 500.
func (f *fixture) sendAs(t *testing.T, router http.Handler, app, validate bool, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	switch {
	case app:
		return testutil.DoRequestWithRole(t, router, method, path, body, f.users["owner"].ID, f.tenant.ID, "owner")
	case validate:
		return newV1Client(t, router).do(method, path, body, f.key(t, "owner"))
	default:
		return testutil.DoRequestWithRawToken(t, router, method, path, body, f.key(t, "owner"))
	}
}

// A create that fails leaves nothing behind. These creates read the new item
// back for their response; the test makes that read fail and checks that the
// create answers 500 with nothing saved, so the client's resend makes exactly
// one item instead of a duplicate or a name conflict. The resend runs with the
// read failing after a commit, which only a read outside the write's
// transaction would hit, and must answer the item with what it nests. Each v1
// create runs next to the app route it mirrors.
func TestIntegrationV1_FailedReadBackSavesNothing(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Readback")
	ctx := context.Background()

	concepts := func() int {
		n, err := q.CountTermBaseEntries(ctx, f.ids["glossaryID"])
		if err != nil {
			t.Fatal(err)
		}
		return int(n)
	}
	workflows := func(name string) func() int {
		return func() int { return testutil.CountWorkflowsNamed(t, q, f.tenant.ID, name) }
	}
	prompts := func(name string) func() int {
		return func() int { return testutil.CountAIPromptsNamed(t, q, f.tenant.ID, name) }
	}

	terms := []map[string]any{{"language": "en", "text": "Hello"}}
	term := nested{"terms", "text", "Hello"}
	steps, additions, step, addition := f.nestedRows()
	cases := []struct {
		name   string
		app    bool // an app route called with a session, else a v1 route called with a key
		path   string
		body   map[string]any
		reads  []string // the sqlc queries that read the new item back
		count  func() int
		nested nested
	}{
		{"v1 concept", false, f.path("/glossaries/{glossaryID}/concepts"),
			map[string]any{"terms": terms}, conceptReads, concepts, term},
		{"app concept", true, fmt.Sprintf("/api/terminology/%d/concepts", f.ids["glossaryID"]),
			map[string]any{"terms": terms}, conceptReads, concepts, term},
		{"v1 workflow", false, "/api/v1/workflows",
			map[string]any{"name": "Readback v1", "steps": steps}, workflowReads, workflows("Readback v1"), step},
		{"app workflow", true, "/api/workflows",
			map[string]any{"name": "Readback app", "steps": steps}, workflowReads, workflows("Readback app"), step},
		{"v1 AI prompt", false, "/api/v1/ai/prompts",
			map[string]any{"name": "Readback v1", "mode": "qe", "prompt_text": "Rate it.", "language_additions": additions},
			promptReads, prompts("Readback v1"), addition},
		{"app AI prompt", true, "/api/ai/prompts",
			map[string]any{"name": "Readback app", "out_status": true, "prompt_text": "Rate it.", "language_additions": additions},
			promptReads, prompts("Readback app"), addition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.count()

			// The read fails wherever it runs: the create answers 500, logs
			// why, and saves nothing.
			f.wantFailedRead(t, testutil.FailingPool, tc.reads, tc.app, http.MethodPost, tc.path, tc.body)
			if got := tc.count(); got != before {
				t.Fatalf("the failed create saved %d item(s)", got-before)
			}

			// The resend, with the same read failing only outside a
			// transaction: nothing reads the item after the commit, so it
			// lands once and answers what it nests.
			afterCommit := testutil.NewTestRouter(testutil.FailingPoolOutsideTx(t, tc.reads...))
			rec := f.sendAs(t, afterCommit, tc.app, true, http.MethodPost, tc.path, tc.body)
			wantStatus(t, rec, http.StatusCreated)
			if got := tc.count(); got != before+1 {
				t.Fatalf("after the resend there are %d new items, want 1", got-before)
			}
			if !tc.nested.heldBy(t, rec) {
				t.Errorf("the resend's answer lacks its %s row %s=%v: %s", tc.nested.list, tc.nested.field, tc.nested.want, rec.Body.String())
			}
		})
	}
}

// A failed read in a GET answers 500 and logs its cause. These reads run
// outside any transaction, so FailingPoolOutsideTx fails them too; the create
// and edit tests rely on that pool to catch a read after the commit.
func TestIntegrationV1_FailedReadLogsItsCause(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Readcause")

	cases := []struct {
		name  string
		app   bool
		path  string
		reads []string
	}{
		{"v1 workflow get", false, f.path("/workflows/{workflowID}"), workflowReads},
		{"app workflow get", true, f.fill("/api/workflows/{workflowID}"), workflowReads},
	}
	pools := []struct {
		name string
		open func(testing.TB, ...string) *pgxpool.Pool
	}{
		{"failing anywhere", testutil.FailingPool},
		{"failing outside a transaction", testutil.FailingPoolOutsideTx},
	}
	for _, tc := range cases {
		for _, pool := range pools {
			t.Run(tc.name+", "+pool.name, func(t *testing.T) {
				f.wantFailedRead(t, pool.open, tc.reads, tc.app, http.MethodGet, tc.path, nil)
			})
		}
	}
}

// An edit reads the item back in its own transaction: when that read fails,
// the edit answers 500 and changes nothing; with the read failing only after a
// commit, it lands and answers what the item nests, never an item that seems
// to have lost it.
func TestIntegrationV1_FailedReadBackLeavesEditUndone(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Readedit")
	ctx := context.Background()

	promptText := func() string {
		p, err := q.GetAIPrompt(ctx, db.GetAIPromptParams{ID: f.ids["promptID"], TenantID: f.tenant.ID})
		if err != nil {
			t.Fatal(err)
		}
		return p.PromptText
	}
	workflowName := func() string {
		wf, err := q.GetWorkflow(ctx, db.GetWorkflowParams{ID: f.ids["workflowID"], TenantID: f.tenant.ID})
		if err != nil {
			t.Fatal(err)
		}
		return wf.Name
	}
	promptRows := func() string {
		rows, err := q.ListAIPromptLanguageAdditionsForPrompt(ctx, f.ids["promptID"])
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, r := range rows {
			fmt.Fprintf(&b, "%d:%v:%s ", r.ID, r.TargetLanguages, r.AdditionText)
		}
		return b.String()
	}
	workflowRows := func() string {
		rows, err := q.ListWorkflowDefinitionSteps(ctx, db.ListWorkflowDefinitionStepsParams{WorkflowID: f.ids["workflowID"], TenantID: f.tenant.ID})
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, r := range rows {
			fmt.Fprintf(&b, "%d:%d:%d ", r.ID, r.StepOrder, r.StepDefinitionID)
		}
		return b.String()
	}
	steps, additions, step, addition := f.nestedRows()

	cases := []struct {
		name   string
		app    bool
		method string
		path   string
		body   map[string]any
		reads  []string
		stored func() string // the field the edit changes
		rows   func() string // the nested rows the edit replaces
		key    string        // that field in the answer
		want   string
		nested nested
	}{
		{"app prompt edit", true, http.MethodPatch, f.fill("/api/ai/prompts/{promptID}"),
			map[string]any{"name": "Review prompt", "out_status": true, "prompt_text": "Edited in the app.", "language_additions": additions},
			promptReads, promptText, promptRows, "prompt_text", "Edited in the app.", addition},
		{"v1 prompt replace", false, http.MethodPut, f.path("/ai/prompts/{promptID}"),
			map[string]any{"name": "Review prompt", "mode": "qe", "prompt_text": "Replaced over v1.", "language_additions": additions},
			promptReads, promptText, promptRows, "prompt_text", "Replaced over v1.", addition},
		{"app workflow edit", true, http.MethodPut, f.fill("/api/workflows/{workflowID}"),
			map[string]any{"name": "Edited in the app", "steps": steps}, workflowReads, workflowName, workflowRows, "name", "Edited in the app", step},
		{"v1 workflow replace", false, http.MethodPut, f.path("/workflows/{workflowID}"),
			map[string]any{"name": "Replaced over v1", "steps": steps}, workflowReads, workflowName, workflowRows, "name", "Replaced over v1", step},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, rowsBefore := tc.stored(), tc.rows()

			f.wantFailedRead(t, testutil.FailingPool, tc.reads, tc.app, tc.method, tc.path, tc.body)
			if got := tc.stored(); got != before {
				t.Fatalf("the failed edit saved %q", got)
			}
			if got := tc.rows(); got != rowsBefore {
				t.Fatalf("the failed edit replaced its nested rows: %q, was %q", got, rowsBefore)
			}

			afterCommit := testutil.NewTestRouter(testutil.FailingPoolOutsideTx(t, tc.reads...))
			rec := f.sendAs(t, afterCommit, tc.app, true, tc.method, tc.path, tc.body)
			wantStatus(t, rec, http.StatusOK)
			if got := tc.stored(); got != tc.want {
				t.Fatalf("stored %q after the edit, want %q", got, tc.want)
			}
			// The cases edit one item in turn and send the same nested rows,
			// so only the edited field tells this answer from an earlier one.
			if got := testutil.DecodeData[map[string]any](t, rec)[tc.key]; got != tc.want {
				t.Errorf("the edit answered %s=%v, want %q", tc.key, got, tc.want)
			}
			if !tc.nested.heldBy(t, rec) {
				t.Errorf("the edit's answer lacks its %s row %s=%v: %s", tc.nested.list, tc.nested.field, tc.nested.want, rec.Body.String())
			}
		})
	}
}
