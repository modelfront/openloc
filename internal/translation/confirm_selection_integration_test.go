package translation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/segmentbulk"
	"openloc/internal/testutil"
)

// confirmFixture: an es job whose project has a QA profile with an
// error-severity tag check and a read/write TM, and one row in every state
// bulk confirm tells apart.
type confirmFixture struct {
	pool             *pgxpool.Pool
	q                db.Querier
	router           http.Handler
	userID, tenantID int64
	projectID        int64
	settingsProfile  int64
	job              db.Job
	tmID             int64

	draft, qaBad, locked, empty, approved, autoApproved, otherFile int64
}

func newConfirmFixture(t *testing.T) *confirmFixture {
	t.Helper()
	ctx := context.Background()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	router := testutil.NewTestRouter(pool)
	f := &confirmFixture{pool: pool, q: q, router: router}

	// setupQABlockProject: tag_checks at error severity on the project.
	var fileID int64
	f.userID, f.tenantID, fileID, f.qaBad = setupQABlockProject(t, router, q, "bulkconfirm", "error", true)
	file, err := q.GetFile(ctx, fileID)
	if err != nil {
		t.Fatal(err)
	}
	f.projectID = file.ProjectID
	proj, err := q.GetProjectByID(ctx, f.projectID)
	if err != nil {
		t.Fatal(err)
	}
	f.settingsProfile = proj.SettingsProfileID
	tm := testutil.SeedTM(t, q, f.tenantID, "Bulk TM")
	f.tmID = tm.ID
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: proj.SettingsProfileID, TmID: tm.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}
	f.job = testutil.SeedJob(t, q, fileID, "es")

	// The fixture's own row ("Hello {name}") drops its placeholder: QA error.
	f.setText(t, f.qaBad, "Hola")

	row := func(fileID int64, src string, pos int32, status db.TranslationStatus, text string) int64 {
		seg := testutil.SeedSegment(t, q, fileID, src, pos)
		tr := testutil.SeedTranslation(t, q, seg.ID, "es", status)
		if text != "" {
			f.setText(t, tr.ID, text)
		}
		return tr.ID
	}
	f.draft = row(fileID, "Good morning", 2, db.TranslationStatusNeedsReview, "Buenos días")
	f.locked = row(fileID, "Good night", 3, db.TranslationStatusNeedsReview, "Buenas noches")
	f.exec(t, "UPDATE translations SET locked = true WHERE id = $1", f.locked)
	f.empty = row(fileID, "Thanks", 4, db.TranslationStatusNeedsReview, "")
	f.approved = row(fileID, "Yes", 5, db.TranslationStatusApproved, "Sí")
	f.autoApproved = row(fileID, "No", 6, db.TranslationStatusAutoApproved, "No")

	other := testutil.SeedFile(t, q, f.projectID, "other.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, other.ID, "es")
	f.otherFile = row(other.ID, "Maybe", 1, db.TranslationStatusNeedsReview, "Quizás")
	return f
}

func (f *confirmFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func (f *confirmFixture) setText(t *testing.T, id int64, text string) {
	t.Helper()
	f.exec(t, "UPDATE translations SET active_text = $2 WHERE id = $1", id, text)
}

func (f *confirmFixture) confirm(t *testing.T, ids []int64, role string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, "POST",
		fmt.Sprintf("/api/jobs/%d/translations/confirm", f.job.ID),
		map[string]any{"translation_ids": ids}, f.userID, f.tenantID, role)
}

func decodeResult(t *testing.T, rec *httptest.ResponseRecorder) segmentbulk.Result {
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

func (f *confirmFixture) status(t *testing.T, id int64) (db.TranslationStatus, string) {
	t.Helper()
	var st db.TranslationStatus
	var via string
	if err := f.pool.QueryRow(context.Background(),
		"SELECT status, COALESCE(confirmed_via, '') FROM translations WHERE id = $1", id).Scan(&st, &via); err != nil {
		t.Fatal(err)
	}
	return st, via
}

func (f *confirmFixture) inTM(t *testing.T, source string) bool {
	t.Helper()
	_, err := f.q.LookupTM(context.Background(), db.LookupTMParams{
		TmID: f.tmID, SourceLanguage: "en", TargetLanguage: "es", SourceText: source,
	})
	return err == nil
}

// Bulk confirm writes only rows a person could confirm as they stand: the
// draft is approved as the user and lands in TM; locked, empty, already
// approved, auto-approved, QA-blocked and foreign rows are skipped with their
// reason and left exactly as they were — the auto-approved one never reaches TM.
func TestIntegrationConfirmSelection_SkipsAndWrites(t *testing.T) {
	f := newConfirmFixture(t)
	ids := []int64{f.draft, f.qaBad, f.locked, f.empty, f.approved, f.autoApproved, f.otherFile}
	res := decodeResult(t, f.confirm(t, ids, "owner"))

	if !reflect.DeepEqual(res.Applied, []int64{f.draft}) {
		t.Errorf("applied = %v, want [%d]", res.Applied, f.draft)
	}
	got := map[int64]segmentbulk.SkipReason{}
	for _, s := range res.Skipped {
		got[s.ID] = s.Reason
	}
	want := map[int64]segmentbulk.SkipReason{
		f.qaBad:        segmentbulk.QAError,
		f.locked:       segmentbulk.Locked,
		f.empty:        segmentbulk.Empty,
		f.approved:     segmentbulk.Unchanged,
		f.autoApproved: segmentbulk.AutoApproved,
		f.otherFile:    segmentbulk.NotFound,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("skipped = %v, want %v", got, want)
	}

	if st, via := f.status(t, f.draft); st != db.TranslationStatusApproved || via != "user" {
		t.Errorf("draft: status %s via %q, want approved via user", st, via)
	}
	for id, wantSt := range map[int64]db.TranslationStatus{
		f.qaBad: db.TranslationStatusNeedsReview, f.locked: db.TranslationStatusNeedsReview,
		f.autoApproved: db.TranslationStatusAutoApproved, f.otherFile: db.TranslationStatusNeedsReview,
	} {
		if st, _ := f.status(t, id); st != wantSt {
			t.Errorf("row %d: status %s, want %s (untouched)", id, st, wantSt)
		}
	}

	if !f.inTM(t, "Good morning") {
		t.Error("confirmed row not written to TM")
	}
	for _, src := range []string{"Hello {name}", "No", "Good night", "Maybe"} {
		if f.inTM(t, src) {
			t.Errorf("skipped row %q reached TM", src)
		}
	}

	// The confirmed row gets the history row a single confirm writes (text
	// unchanged since the last history row, so `approved`).
	hist, err := f.q.ListTranslationHistory(context.Background(), f.draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) == 0 || hist[0].CreatedBy == nil || *hist[0].CreatedBy != f.userID {
		t.Errorf("no history row by the user on the confirmed row: %+v", hist)
	}

	// The QA-blocked row's finding is stored, so the editor can show why.
	var n int
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM qa_results WHERE translation_id = $1 AND severity = 'error'", f.qaBad).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("QA-blocked row has no stored finding")
	}
}

// The task guard runs once for the whole request: on a completed task a
// non-manager is refused and nothing is written; a manager's confirm reopens
// the task and says so.
func TestIntegrationConfirmSelection_CompletedTask(t *testing.T) {
	f := newConfirmFixture(t)
	testutil.SeedWorkflow(t, f.q, f.settingsProfile, f.job.ID,
		testutil.WorkflowStepSpec{Name: "Translate", Assignee: &f.userID, Status: "completed"})

	rec := f.confirm(t, []int64{f.draft}, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	if !strings.Contains(rec.Body.String(), `"task_completed"`) {
		t.Fatalf("refused for another reason: %s", rec.Body.String())
	}
	if st, _ := f.status(t, f.draft); st != db.TranslationStatusNeedsReview {
		t.Fatalf("row written despite refusal: %s", st)
	}

	res := decodeResult(t, f.confirm(t, []int64{f.draft}, "owner"))
	if !res.TaskReopened || !reflect.DeepEqual(res.Applied, []int64{f.draft}) {
		t.Errorf("manager confirm: %+v, want applied [%d] with task_reopened", res, f.draft)
	}
}

// A request with nothing to write reopens nothing.
func TestIntegrationConfirmSelection_NothingToWriteKeepsTaskClosed(t *testing.T) {
	f := newConfirmFixture(t)
	testutil.SeedWorkflow(t, f.q, f.settingsProfile, f.job.ID,
		testutil.WorkflowStepSpec{Name: "Translate", Assignee: &f.userID, Status: "completed"})
	res := decodeResult(t, f.confirm(t, []int64{f.approved, f.locked}, "owner"))
	if res.TaskReopened || len(res.Applied) != 0 {
		t.Errorf("got %+v, want nothing applied and no reopen", res)
	}
	if _, err := f.q.GetActiveStepForJob(context.Background(), f.job.ID); err == nil {
		t.Error("task reopened although nothing was confirmed")
	}
}
