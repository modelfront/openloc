package translation_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// scopePropagateFixture: four German files with "Save" in each. A, B and C
// are opened together; Outside is not. Every job has a step in progress, A's
// "Save" is the one confirmed.
type scopePropagateFixture struct {
	pool         *pgxpool.Pool
	q            db.Querier
	router       http.Handler
	userID       int64
	tenant       int64
	project      db.Project
	jobs         map[string]db.Job
	save         map[string]db.Translation // each file's "Save"
	otherInScope db.Translation            // A's "Cancel"
	linguist     db.User
}

func newScopePropagateFixture(t *testing.T) *scopePropagateFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	f := &scopePropagateFixture{pool: pool, q: q, router: testutil.NewTestRouter(pool),
		jobs: map[string]db.Job{}, save: map[string]db.Translation{}}
	f.userID, f.tenant = testutil.RegisterUser(t, q, "scope-propagate")
	f.linguist = testutil.SeedUserWithRole(t, q, f.tenant, "scope-prop-ling@test.com", "pw", "linguist")
	f.project = testutil.SeedProject(t, q, f.tenant, "Scope propagate", "en", []string{"de"})
	for _, name := range []string{"A", "B", "C", "Outside"} {
		file := testutil.SeedFile(t, q, f.project.ID, name, db.FileFormatJsonI18n)
		f.jobs[name] = testutil.SeedJob(t, q, file.ID, "de")
		s := testutil.SeedSegment(t, q, file.ID, "Save", 0)
		f.save[name] = testutil.SeedTranslation(t, q, s.ID, "de", db.TranslationStatusNeedsReview)
		if name == "A" {
			c := testutil.SeedSegment(t, q, file.ID, "Cancel", 1)
			f.otherInScope = testutil.SeedTranslation(t, q, c.ID, "de", db.TranslationStatusNeedsReview)
		}
		testutil.SeedJobStep(t, q, f.project.SettingsProfileID, f.jobs[name].ID, &f.linguist.ID)
	}
	return f
}

func (f *scopePropagateFixture) path(names ...string) string {
	ids := make([]string, len(names))
	for i, n := range names {
		ids[i] = fmt.Sprint(f.jobs[n].ID)
	}
	return fmt.Sprintf("/api/projects/%d/editor/propagate?jobs=%s", f.project.ID, strings.Join(ids, ","))
}

func (f *scopePropagateFixture) propagate(t *testing.T, userID int64, role, path string, excludeID int64) (int, map[string]any) {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, f.router, http.MethodPost, path, map[string]any{
		"source_text": "Save", "target_language": "de", "human_text": "Speichern", "exclude_id": excludeID,
	}, userID, f.tenant, role)
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	return rec.Code, testutil.DecodeData[map[string]any](t, rec)
}

func (f *scopePropagateFixture) text(t *testing.T, tr db.Translation) string {
	t.Helper()
	var text *string
	if err := f.pool.QueryRow(testutil.Ctx(), `SELECT active_text FROM translations WHERE id = $1`, tr.ID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	return ptr.Deref(text)
}

func TestIntegrationScopePropagate_ReachesEveryOpenFileOnly(t *testing.T) {
	f := newScopePropagateFixture(t)
	code, res := f.propagate(t, f.userID, "owner", f.path("A", "B", "C"), f.save["A"].ID)
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if res["propagated"] != float64(2) {
		t.Errorf("propagated = %v, want B and C", res["propagated"])
	}
	for _, name := range []string{"B", "C"} {
		if got := f.text(t, f.save[name]); got != "Speichern" {
			t.Errorf("%s's Save = %q, want Speichern", name, got)
		}
	}
	if got := f.text(t, f.save["Outside"]); got != "" {
		t.Errorf("Outside's Save = %q, want untouched: it is not open", got)
	}
}

// A file whose task takes no edit (completed, and the caller may not reopen
// it) keeps its row and is counted; the others still get theirs.
func TestIntegrationScopePropagate_SkipsAFileWhoseTaskTakesNoEdit(t *testing.T) {
	f := newScopePropagateFixture(t)
	if _, err := f.pool.Exec(testutil.Ctx(), `UPDATE job_step_assignments SET status = 'completed' WHERE job_id = $1`, f.jobs["C"].ID); err != nil {
		t.Fatal(err)
	}
	code, res := f.propagate(t, f.linguist.ID, "linguist", f.path("A", "B", "C"), f.save["A"].ID)
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if res["propagated"] != float64(1) || res["not_editable"] != float64(1) {
		t.Errorf("propagated %v, not_editable %v, want 1 and 1", res["propagated"], res["not_editable"])
	}
	if got := f.text(t, f.save["C"]); got != "" {
		t.Errorf("completed C's Save = %q, want untouched", got)
	}
	if got := f.text(t, f.save["B"]); got != "Speichern" {
		t.Errorf("B's Save = %q, want Speichern", got)
	}

	// A manager's propagation never reopens another file's completed task.
	code, res = f.propagate(t, f.userID, "owner", f.path("A", "C"), f.save["A"].ID)
	if code != http.StatusOK || res["not_editable"] != float64(1) || f.text(t, f.save["C"]) != "" {
		t.Errorf("manager: status %d, result %v, C's Save %q; want C left alone", code, res, f.text(t, f.save["C"]))
	}
}

func TestIntegrationScopePropagate_RefusesAConfirmOutsideTheScope(t *testing.T) {
	f := newScopePropagateFixture(t)
	if code, _ := f.propagate(t, f.userID, "owner", f.path("A", "B"), f.save["Outside"].ID); code != http.StatusNotFound {
		t.Errorf("exclude_id outside the scope: %d, want 404", code)
	}
	if code, _ := f.propagate(t, f.userID, "owner", f.path("A", "B"), 0); code != http.StatusBadRequest {
		t.Errorf("no exclude_id: %d, want 400", code)
	}
	if got := f.text(t, f.save["B"]); got != "" {
		t.Errorf("B's Save = %q after refused calls, want untouched", got)
	}

	otherUser, otherTenant := testutil.RegisterUser(t, f.q, "scope-propagate-other")
	rec := testutil.DoRequest(t, f.router, http.MethodPost, f.path("A", "B"), map[string]any{
		"source_text": "Save", "target_language": "de", "human_text": "Gestohlen", "exclude_id": f.save["A"].ID,
	}, otherUser, otherTenant)
	if rec.Code != http.StatusNotFound {
		t.Errorf("another tenant: %d, want 404", rec.Code)
	}
	if got := f.text(t, f.save["B"]); got != "" {
		t.Errorf("B's Save = %q after another tenant's call, want untouched", got)
	}
}

// The single-file route still stops at its file.
func TestIntegrationScopePropagate_FileRouteStaysInItsFile(t *testing.T) {
	f := newScopePropagateFixture(t)
	rec := testutil.DoRequest(t, f.router, http.MethodPost, fmt.Sprintf("/api/files/%d/propagate", f.jobs["A"].FileID), map[string]any{
		"source_text": "Cancel", "target_language": "de", "human_text": "Abbrechen", "exclude_id": 0,
	}, f.userID, f.tenant)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if got := f.text(t, f.otherInScope); got != "Abbrechen" {
		t.Errorf("A's Cancel = %q, want Abbrechen", got)
	}
	rec = testutil.DoRequest(t, f.router, http.MethodPost, fmt.Sprintf("/api/files/%d/propagate", f.jobs["A"].FileID), map[string]any{
		"source_text": "Save", "target_language": "de", "human_text": "Speichern", "exclude_id": 0,
	}, f.userID, f.tenant)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if got := f.text(t, f.save["B"]); got != "" {
		t.Errorf("B's Save = %q, want untouched by A's file route", got)
	}
}

// A confirmed translation outside the scope is refused before anything is
// written: its job's completed task stays completed, even for a manager whose
// edit would otherwise reopen it.
func TestIntegrationScopePropagate_OutOfScopeConfirmReopensNothing(t *testing.T) {
	f := newScopePropagateFixture(t)
	if _, err := f.pool.Exec(testutil.Ctx(), `UPDATE job_step_assignments SET status = 'completed' WHERE job_id = $1`, f.jobs["Outside"].ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.propagate(t, f.userID, "owner", f.path("A", "B"), f.save["Outside"].ID); code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", code)
	}
	var status string
	if err := f.pool.QueryRow(testutil.Ctx(), `SELECT status FROM job_step_assignments WHERE job_id = $1`, f.jobs["Outside"].ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Errorf("Outside's task = %s, want still completed", status)
	}
}

// Every sibling a propagation wrote gets one `propagation` history row naming
// the confirmed translation.
func TestIntegrationScopePropagate_RecordsHistoryPerSibling(t *testing.T) {
	f := newScopePropagateFixture(t)
	if code, _ := f.propagate(t, f.userID, "owner", f.path("A", "B", "C"), f.save["A"].ID); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	for _, name := range []string{"B", "C"} {
		var n int
		if err := f.pool.QueryRow(testutil.Ctx(),
			`SELECT count(*) FROM translation_history WHERE translation_id = $1 AND kind = 'propagation'
			   AND text = 'Speichern' AND created_by = $2 AND (details->>'source_translation_id')::bigint = $3`,
			f.save[name].ID, f.userID, f.save["A"].ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s: %d propagation history rows, want 1", name, n)
		}
	}
}
