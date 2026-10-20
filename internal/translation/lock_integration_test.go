package translation_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// A lock is a manager's decision that the text is final: locking approves the
// segment (unless QE+APE already auto-approved it), like a confirm. Only
// managers lock; anyone who may edit the file may unlock it to change it.

type lockFixture struct {
	pool     *pgxpool.Pool
	q        db.Querier
	router   chi.Router
	tenantID int64
	pmID     int64
	lingID   int64
	fileID   int64
	jobID    int64
	stepID   int64
	tmID     int64
	position int32
}

func setupLock(t *testing.T) *lockFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "LockCo")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@lock.test", "pass", "project_manager")
	ling := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@lock.test", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "Lock", "en", []string{"de"})
	tm := testutil.SeedTM(t, q, tenant.ID, "Lock TM")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID, TmID: tm.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatalf("assign TM: %v", err)
	}
	file := testutil.SeedFile(t, q, project.ID, "lock.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	// The linguist is assigned, so the file middleware lets them through and
	// only the role gate can refuse them.
	stepID := testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &ling.ID)
	return &lockFixture{
		pool: pool, q: q, router: testutil.NewTestRouter(pool), tenantID: tenant.ID,
		pmID: pm.ID, lingID: ling.ID, fileID: file.ID, jobID: job.ID, stepID: stepID, tmID: tm.ID,
	}
}

// row seeds a segment with the given status and target text ("" = empty).
func (f *lockFixture) row(t *testing.T, source string, status db.TranslationStatus, text string) int64 {
	t.Helper()
	f.position++
	seg := testutil.SeedSegment(t, f.q, f.fileID, source, f.position)
	tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", status)
	if text != "" {
		if _, err := f.pool.Exec(context.Background(),
			`UPDATE translations SET active_text = $1 WHERE id = $2`, text, tr.ID); err != nil {
			t.Fatalf("set text: %v", err)
		}
	}
	return tr.ID
}

func (f *lockFixture) post(t *testing.T, path string, userID int64, role string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, "POST", path, body, userID, f.tenantID, role)
}

func (f *lockFixture) lockOne(t *testing.T, id, userID int64, role string) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(t, fmt.Sprintf("/api/files/%d/translations/%d/lock", f.fileID, id), userID, role, nil)
}

func (f *lockFixture) unlockOne(t *testing.T, id, userID int64, role string) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(t, fmt.Sprintf("/api/files/%d/translations/%d/unlock", f.fileID, id), userID, role, nil)
}

func (f *lockFixture) get(t *testing.T, id int64) db.Translation {
	t.Helper()
	tr, err := f.q.GetTranslation(context.Background(), id)
	if err != nil {
		t.Fatalf("get translation %d: %v", id, err)
	}
	return tr
}

func (f *lockFixture) tmEntries(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM translation_memory WHERE tm_id = $1`, f.tmID).Scan(&n); err != nil {
		t.Fatalf("count TM: %v", err)
	}
	return n
}

func (f *lockFixture) approvedEvents(t *testing.T, id int64) []db.ListTranslationHistoryRow {
	t.Helper()
	rows, err := f.q.ListTranslationHistory(context.Background(), id)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var out []db.ListTranslationHistoryRow
	for _, r := range rows {
		if r.Kind == db.TranslationHistoryKindApproved {
			out = append(out, r)
		}
	}
	return out
}

func TestIntegrationLock_LinguistRefused(t *testing.T) {
	f := setupLock(t)
	id := f.row(t, "Hello", db.TranslationStatusNeedsReview, "Hallo")

	testutil.AssertStatus(t, f.lockOne(t, id, f.lingID, "linguist"), http.StatusForbidden)

	if got := f.get(t, id); got.Locked || got.Status != db.TranslationStatusNeedsReview {
		t.Errorf("after a refused lock: locked=%v status=%s", got.Locked, got.Status)
	}
}

func TestIntegrationLock_ApprovesLikeAConfirm(t *testing.T) {
	f := setupLock(t)
	id := f.row(t, "Hello", db.TranslationStatusNeedsReview, "Hallo")

	testutil.AssertStatus(t, f.lockOne(t, id, f.pmID, "project_manager"), http.StatusOK)

	got := f.get(t, id)
	if !got.Locked || got.Status != db.TranslationStatusApproved || got.ConfirmedVia != "user" {
		t.Fatalf("locked=%v status=%s confirmed_via=%s, want locked approved user", got.Locked, got.Status, got.ConfirmedVia)
	}
	events := f.approvedEvents(t, id)
	if len(events) != 1 || events[0].CreatedBy == nil || *events[0].CreatedBy != f.pmID ||
		events[0].JobStepAssignmentID == nil || *events[0].JobStepAssignmentID != f.stepID {
		t.Errorf("approved history = %+v, want one event by the PM in the current step", events)
	}
	if n := f.tmEntries(t); n != 1 {
		t.Errorf("TM entries = %d, want 1 (a lock writes TM like a confirm)", n)
	}
}

// Auto-approved stays auto-approved: locking never sends machine-approved
// text to TM.
func TestIntegrationLock_KeepsAutoApproved(t *testing.T) {
	f := setupLock(t)
	id := f.row(t, "Hello", db.TranslationStatusAutoApproved, "Hallo")

	testutil.AssertStatus(t, f.lockOne(t, id, f.pmID, "project_manager"), http.StatusOK)

	if got := f.get(t, id); !got.Locked || got.Status != db.TranslationStatusAutoApproved {
		t.Errorf("locked=%v status=%s, want locked auto_approved", got.Locked, got.Status)
	}
	if len(f.approvedEvents(t, id)) != 0 || f.tmEntries(t) != 0 {
		t.Error("locking an auto-approved segment wrote an approval or a TM entry")
	}
}

// A segment a person already confirmed is locked without a second approval.
func TestIntegrationLock_AlreadyApprovedNoSecondApproval(t *testing.T) {
	f := setupLock(t)
	id := f.row(t, "Hello", db.TranslationStatusApproved, "Hallo")

	testutil.AssertStatus(t, f.lockOne(t, id, f.pmID, "project_manager"), http.StatusOK)

	if got := f.get(t, id); !got.Locked || got.Status != db.TranslationStatusApproved {
		t.Errorf("locked=%v status=%s", got.Locked, got.Status)
	}
	if n := len(f.approvedEvents(t, id)); n != 0 {
		t.Errorf("approved events = %d, want 0", n)
	}
}

// An empty segment cannot be approved, so it cannot be locked.
func TestIntegrationLock_EmptyRefused(t *testing.T) {
	f := setupLock(t)
	id := f.row(t, "Hello", db.TranslationStatusNeedsReview, "")

	testutil.AssertStatus(t, f.lockOne(t, id, f.pmID, "project_manager"), http.StatusConflict)

	if got := f.get(t, id); got.Locked {
		t.Error("empty segment got locked")
	}
}

// A translator unlocks to edit. The status stays until they change the text.
func TestIntegrationUnlock_LinguistAllowed(t *testing.T) {
	f := setupLock(t)
	id := f.row(t, "Hello", db.TranslationStatusNeedsReview, "Hallo")
	testutil.AssertStatus(t, f.lockOne(t, id, f.pmID, "project_manager"), http.StatusOK)

	testutil.AssertStatus(t, f.unlockOne(t, id, f.lingID, "linguist"), http.StatusOK)

	if got := f.get(t, id); got.Locked || got.Status != db.TranslationStatusApproved {
		t.Errorf("locked=%v status=%s, want unlocked approved", got.Locked, got.Status)
	}
}

// The bulk routes have the same gates and the same approval. Empty rows are
// skipped and reported.
func TestIntegrationLockSelection(t *testing.T) {
	f := setupLock(t)
	draft := f.row(t, "One", db.TranslationStatusNeedsReview, "Eins")
	auto := f.row(t, "Two", db.TranslationStatusAutoApproved, "Zwei")
	empty := f.row(t, "Three", db.TranslationStatusNeedsReview, "")
	body := map[string]any{"translation_ids": []int64{draft, auto, empty}}
	path := fmt.Sprintf("/api/jobs/%d/translations/lock", f.jobID)

	testutil.AssertStatus(t, f.post(t, path, f.lingID, "linguist", body), http.StatusForbidden)
	if f.get(t, draft).Locked {
		t.Fatal("linguist's bulk lock went through")
	}

	rec := f.post(t, path, f.pmID, "project_manager", body)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if got := f.get(t, draft); !got.Locked || got.Status != db.TranslationStatusApproved {
		t.Errorf("draft: locked=%v status=%s, want locked approved", got.Locked, got.Status)
	}
	if got := f.get(t, auto); !got.Locked || got.Status != db.TranslationStatusAutoApproved {
		t.Errorf("auto: locked=%v status=%s, want locked auto_approved", got.Locked, got.Status)
	}
	if f.get(t, empty).Locked {
		t.Error("empty row got locked")
	}
	if n := f.tmEntries(t); n != 1 {
		t.Errorf("TM entries = %d, want 1 (the draft only)", n)
	}

	unlock := fmt.Sprintf("/api/jobs/%d/translations/unlock", f.jobID)
	testutil.AssertStatus(t, f.post(t, unlock, f.lingID, "linguist", map[string]any{"translation_ids": []int64{draft}}), http.StatusOK)
	if f.get(t, draft).Locked {
		t.Error("linguist's bulk unlock did not unlock")
	}
}

// Propagation never overwrites a locked segment.
func TestIntegrationPropagate_SkipsLocked(t *testing.T) {
	f := setupLock(t)
	source := f.row(t, "Same", db.TranslationStatusNeedsReview, "Gleich")
	locked := f.row(t, "Same", db.TranslationStatusNeedsReview, "Fest")
	open := f.row(t, "Same", db.TranslationStatusNeedsReview, "Offen")
	testutil.AssertStatus(t, f.lockOne(t, locked, f.pmID, "project_manager"), http.StatusOK)

	rec := f.post(t, fmt.Sprintf("/api/files/%d/propagate", f.fileID), f.pmID, "project_manager", map[string]any{
		"source_text": "Same", "target_language": "de", "human_text": "Neu", "exclude_id": source,
	})
	testutil.AssertStatus(t, rec, http.StatusOK)

	if got := text(f.get(t, locked)); got != "Fest" {
		t.Errorf("locked text = %q, want it unchanged", got)
	}
	if got := text(f.get(t, open)); got != "Neu" {
		t.Errorf("unlocked repeat = %q, want the propagated text", got)
	}
}

func text(tr db.Translation) string {
	if tr.ActiveText == nil {
		return ""
	}
	return *tr.ActiveText
}
