package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/segmentbulk"
	"openloc/internal/testutil"
)

// bulkFixture is one de job with rows in every state a bulk action has to
// tell apart, plus rows the job's id list must never reach: the same file's
// fr job, another file's de job, and another tenant's job.
type bulkFixture struct {
	pool    *pgxpool.Pool
	q       db.Querier
	router  chi.Router
	tenant  db.Tenant
	owner   db.User
	project db.Project
	job     db.Job

	approved, draft, locked, empty, bystander int64 // rows of the job
	sameFileFr, otherFile, otherTenant        int64 // rows of other jobs
}

func newBulkFixture(t *testing.T) *bulkFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	f := &bulkFixture{pool: pool, q: q, router: testutil.NewTestRouter(pool)}

	f.tenant = testutil.SeedTenant(t, q, "BulkSegCo")
	f.owner = testutil.SeedUser(t, q, f.tenant.ID, "owner@bulk-seg.test", "pass")
	f.project = testutil.SeedProject(t, q, f.tenant.ID, "Bulk", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, f.project.ID, "a.txt", db.FileFormatTxt)
	f.job = testutil.SeedJob(t, q, file.ID, "de")
	testutil.SeedJob(t, q, file.ID, "fr")

	row := func(fileID int64, lang, src string, pos int32, status db.TranslationStatus, text string) int64 {
		seg := testutil.SeedSegment(t, q, fileID, src, pos)
		tr := testutil.SeedTranslation(t, q, seg.ID, lang, status)
		if text != "" {
			setActiveText(t, pool, tr.ID, text)
		}
		return tr.ID
	}
	f.approved = row(file.ID, "de", "one", 1, db.TranslationStatusApproved, "eins")
	f.draft = row(file.ID, "de", "two", 2, db.TranslationStatusNeedsReview, "zwei")
	f.locked = row(file.ID, "de", "three", 3, db.TranslationStatusNeedsReview, "drei")
	setLocked(t, pool, f.locked)
	f.empty = row(file.ID, "de", "four", 4, db.TranslationStatusNeedsReview, "")
	f.bystander = row(file.ID, "de", "five", 5, db.TranslationStatusNeedsReview, "fünf")

	// The fr row hangs off the "one" segment, so only the language tells it apart.
	segs, err := q.ListSegmentsByFile(context.Background(), file.ID)
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	fr := testutil.SeedTranslation(t, q, segs[0].ID, "fr", db.TranslationStatusNeedsReview)
	setActiveText(t, pool, fr.ID, "un")
	f.sameFileFr = fr.ID

	fileB := testutil.SeedFile(t, q, f.project.ID, "b.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, fileB.ID, "de")
	f.otherFile = row(fileB.ID, "de", "six", 1, db.TranslationStatusNeedsReview, "sechs")

	other := testutil.SeedTenant(t, q, "OtherCo")
	otherProject := testutil.SeedProject(t, q, other.ID, "Other", "en", []string{"de"})
	otherFile := testutil.SeedFile(t, q, otherProject.ID, "c.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, otherFile.ID, "de")
	f.otherTenant = row(otherFile.ID, "de", "seven", 1, db.TranslationStatusNeedsReview, "sieben")
	return f
}

func (f *bulkFixture) post(t *testing.T, action string, ids []int64, role string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, "POST",
		fmt.Sprintf("/api/jobs/%d/translations/%s", f.job.ID, action),
		map[string]any{"translation_ids": ids}, f.owner.ID, f.tenant.ID, role)
}

func decodeBulk(t *testing.T, rec *httptest.ResponseRecorder) segmentbulk.Result {
	t.Helper()
	testutil.AssertStatus(t, rec, http.StatusOK)
	var body struct {
		Data segmentbulk.Result `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return body.Data
}

type rowState struct {
	text   *string
	locked bool
}

func (f *bulkFixture) state(t *testing.T, id int64) rowState {
	t.Helper()
	var s rowState
	if err := f.pool.QueryRow(context.Background(),
		"SELECT active_text, locked FROM translations WHERE id = $1", id).Scan(&s.text, &s.locked); err != nil {
		t.Fatalf("read translation %d: %v", id, err)
	}
	return s
}

func assertSkipped(t *testing.T, got []segmentbulk.Skip, want map[int64]segmentbulk.SkipReason) {
	t.Helper()
	m := map[int64]segmentbulk.SkipReason{}
	for _, s := range got {
		m[s.ID] = s.Reason
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("skipped = %v, want %v", m, want)
	}
}

// Locking a selection touches only the job's own rows. An id from the same
// file's other language, another file or another tenant comes back
// not_found and stays unlocked.
func TestIntegrationBulkLock_OnlyJobRows(t *testing.T) {
	f := newBulkFixture(t)
	res := decodeBulk(t, f.post(t, "lock",
		[]int64{f.approved, f.locked, f.sameFileFr, f.otherFile, f.otherTenant}, "owner"))

	if !reflect.DeepEqual(res.Applied, []int64{f.approved}) {
		t.Errorf("applied = %v, want [%d]", res.Applied, f.approved)
	}
	assertSkipped(t, res.Skipped, map[int64]segmentbulk.SkipReason{
		f.locked:      segmentbulk.Unchanged,
		f.sameFileFr:  segmentbulk.NotFound,
		f.otherFile:   segmentbulk.NotFound,
		f.otherTenant: segmentbulk.NotFound,
	})
	if !f.state(t, f.approved).locked {
		t.Error("selected row not locked")
	}
	for _, id := range []int64{f.sameFileFr, f.otherFile, f.otherTenant, f.draft} {
		if f.state(t, id).locked {
			t.Errorf("row %d outside the selection got locked", id)
		}
	}
}

// Unlock is open to any member with access to the job; already unlocked rows
// come back unchanged.
func TestIntegrationBulkUnlock_AnyMember(t *testing.T) {
	f := newBulkFixture(t)
	res := decodeBulk(t, f.post(t, "unlock", []int64{f.locked, f.draft}, "project_manager"))
	if !reflect.DeepEqual(res.Applied, []int64{f.locked}) {
		t.Errorf("applied = %v, want [%d]", res.Applied, f.locked)
	}
	assertSkipped(t, res.Skipped, map[int64]segmentbulk.SkipReason{f.draft: segmentbulk.Unchanged})
	if f.state(t, f.locked).locked {
		t.Error("manager's unlock did not unlock")
	}
}

// A linguist who is not assigned to the job cannot lock its rows.
func TestIntegrationBulkLock_UnassignedLinguistRefused(t *testing.T) {
	f := newBulkFixture(t)
	ling := testutil.SeedUserWithRole(t, f.q, f.tenant.ID, "ling@bulk-seg.test", "pass", "linguist")
	rec := testutil.DoRequestWithRole(t, f.router, "POST",
		fmt.Sprintf("/api/jobs/%d/translations/lock", f.job.ID),
		map[string]any{"translation_ids": []int64{f.draft}}, ling.ID, f.tenant.ID, "linguist")
	// RequireLinguistJobAccess answers 403; any other status means something
	// else refused the request and the gate went untested.
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	if f.state(t, f.draft).locked {
		t.Error("row got locked")
	}
}

// Clearing a selection clears exactly the selected, unlocked, non-empty rows
// of the job — confirmed ones included — drops only their QA findings, and
// records a `cleared` event per cleared row.
func TestIntegrationBulkClear_Selection(t *testing.T) {
	f := newBulkFixture(t)
	ctx := context.Background()
	for _, id := range []int64{f.approved, f.bystander} {
		if _, err := f.pool.Exec(ctx, `INSERT INTO qa_results (translation_id, check_type, severity, message, ignored)
			VALUES ($1, 'number', 'error', 'x', false)`, id); err != nil {
			t.Fatalf("seed qa: %v", err)
		}
	}

	res := decodeBulk(t, f.post(t, "clear",
		[]int64{f.approved, f.draft, f.locked, f.empty, f.sameFileFr, f.otherFile, f.otherTenant}, "owner"))

	if !reflect.DeepEqual(res.Applied, []int64{f.approved, f.draft}) {
		t.Errorf("applied = %v, want [%d %d]", res.Applied, f.approved, f.draft)
	}
	assertSkipped(t, res.Skipped, map[int64]segmentbulk.SkipReason{
		f.locked:      segmentbulk.Locked,
		f.empty:       segmentbulk.Empty,
		f.sameFileFr:  segmentbulk.NotFound,
		f.otherFile:   segmentbulk.NotFound,
		f.otherTenant: segmentbulk.NotFound,
	})
	for _, id := range []int64{f.approved, f.draft} {
		if s := f.state(t, id); s.text != nil {
			t.Errorf("row %d not cleared: %q", id, *s.text)
		}
	}
	for _, id := range []int64{f.locked, f.bystander, f.sameFileFr, f.otherFile, f.otherTenant} {
		if s := f.state(t, id); s.text == nil {
			t.Errorf("row %d outside the clear lost its text", id)
		}
	}

	var qa map[int64]int
	rows, err := f.pool.Query(ctx, "SELECT translation_id, count(*) FROM qa_results WHERE translation_id = ANY($1) GROUP BY 1",
		[]int64{f.approved, f.bystander})
	if err != nil {
		t.Fatalf("read qa: %v", err)
	}
	qa = map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			t.Fatalf("scan qa: %v", err)
		}
		qa[id] = n
	}
	rows.Close()
	if qa[f.approved] != 0 || qa[f.bystander] != 1 {
		t.Errorf("qa findings after clear = %v; want cleared row 0, bystander 1", qa)
	}

	hist, err := f.q.ListTranslationHistory(ctx, f.approved)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	found := false
	for _, h := range hist {
		if h.Kind == db.TranslationHistoryKindCleared && h.CreatedBy != nil && *h.CreatedBy == f.owner.ID {
			found = true
		}
	}
	if !found {
		t.Error("no `cleared` history row by the caller on a cleared row")
	}
	lockedHist, err := f.q.ListTranslationHistory(ctx, f.locked)
	if err != nil {
		t.Fatal(err)
	}
	if len(lockedHist) != 0 {
		t.Errorf("skipped row got %d history rows", len(lockedHist))
	}
}

// Clear on a selection has the job clear's gates: refused outside the first
// workflow step and while a pipeline run holds the job — and writes nothing.
func TestIntegrationBulkClear_Gates(t *testing.T) {
	t.Run("not first step", func(t *testing.T) {
		f := newBulkFixture(t)
		testutil.SeedWorkflow(t, f.q, f.project.SettingsProfileID, f.job.ID,
			testutil.WorkflowStepSpec{Name: "Translate", Assignee: &f.owner.ID, Status: "completed"},
			testutil.WorkflowStepSpec{Name: "Review", Assignee: &f.owner.ID, Status: "in_progress"})
		testutil.AssertStatus(t, f.post(t, "clear", []int64{f.draft}, "owner"), http.StatusForbidden)
		if f.state(t, f.draft).text == nil {
			t.Error("row cleared outside the first step")
		}
	})
	t.Run("pipeline running", func(t *testing.T) {
		f := newBulkFixture(t)
		if _, err := f.pool.Exec(context.Background(),
			"UPDATE jobs SET status = 'in_progress', current_step = 'Translating' WHERE id = $1", f.job.ID); err != nil {
			t.Fatal(err)
		}
		testutil.AssertStatus(t, f.post(t, "clear", []int64{f.draft}, "owner"), http.StatusConflict)
		if f.state(t, f.draft).text == nil {
			t.Error("row cleared while a pipeline ran")
		}
	})
}

func TestIntegrationBulkSegments_RejectsBadIDLists(t *testing.T) {
	f := newBulkFixture(t)
	tooMany := make([]int64, segmentbulk.MaxIDs+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	for name, ids := range map[string][]int64{"empty": {}, "too many": tooMany, "non-positive": {0}} {
		for _, action := range []string{"lock", "clear"} {
			if rec := f.post(t, action, ids, "owner"); rec.Code != http.StatusBadRequest {
				t.Errorf("%s %s: status %d, want 400", action, name, rec.Code)
			}
		}
	}
}

// A selection Clear that clears nothing — only locked or empty rows selected —
// leaves a finished workflow finished: there is no progress to revert.
func TestIntegrationBulkClear_NothingClearedKeepsWorkflowClosed(t *testing.T) {
	f := newBulkFixture(t)
	testutil.SeedWorkflow(t, f.q, f.project.SettingsProfileID, f.job.ID,
		testutil.WorkflowStepSpec{Name: "Translate", Assignee: &f.owner.ID, Status: "completed"})

	res := decodeBulk(t, f.post(t, "clear", []int64{f.locked, f.empty}, "owner"))
	if len(res.Applied) != 0 || res.WorkflowReopened {
		t.Errorf("result = %+v, want nothing cleared and no reopen", res)
	}
	if _, err := f.q.GetActiveStepForJob(context.Background(), f.job.ID); err == nil {
		t.Error("the workflow was reopened although nothing was cleared")
	}
}
