package hold_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/hold"
	"openloc/internal/testutil"
)

// holdFixture: one org with a project manager, the assigned linguist and an
// unassigned linguist; a job with two segments in an in-progress task; and a
// second org with its own job.
type holdFixture struct {
	pool                   *pgxpool.Pool
	q                      db.Querier
	router                 http.Handler
	tenant                 int64
	pm, linguist, outsider db.User
	job                    db.Job
	t1, t2                 db.Translation
	otherTenant            int64
	otherUser              db.User
	otherJob               db.Job
	otherT                 db.Translation
	roles                  map[int64]string
}

func newHoldFixture(t *testing.T) (*holdFixture, func()) {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	f := &holdFixture{pool: pool, q: q, router: testutil.NewTestRouter(pool)}
	f.tenant = testutil.SeedTenant(t, q, "HoldCorp").ID
	f.pm = testutil.SeedUserWithRole(t, q, f.tenant, "pm@test.com", "pass", "project_manager")
	f.linguist = testutil.SeedUserWithRole(t, q, f.tenant, "ana@test.com", "pass", "linguist")
	f.outsider = testutil.SeedUserWithRole(t, q, f.tenant, "out@test.com", "pass", "linguist")
	f.job, f.t1, f.t2 = seedJob(t, q, f.tenant, &f.linguist.ID, "Hold project")

	f.otherTenant = testutil.SeedTenant(t, q, "OtherCorp").ID
	f.otherUser = testutil.SeedUserWithRole(t, q, f.otherTenant, "pm@other.test", "pass", "project_manager")
	f.otherJob, f.otherT, _ = seedJob(t, q, f.otherTenant, nil, "Other project")
	f.roles = map[int64]string{
		f.pm.ID: "project_manager", f.linguist.ID: "linguist", f.outsider.ID: "linguist", f.otherUser.ID: "project_manager",
	}
	return f, cleanup
}

func seedJob(t *testing.T, q db.Querier, tenantID int64, assignee *int64, name string) (db.Job, db.Translation, db.Translation) {
	t.Helper()
	p := testutil.SeedProject(t, q, tenantID, name, "en", []string{"de"})
	file := testutil.SeedFile(t, q, p.ID, "doc.txt", db.FileFormatTxt)
	s1 := testutil.SeedSegment(t, q, file.ID, "Hello", 0)
	s2 := testutil.SeedSegment(t, q, file.ID, "World", 1)
	t1 := testutil.SeedTranslation(t, q, s1.ID, "de", db.TranslationStatusNeedsReview)
	t2 := testutil.SeedTranslation(t, q, s2.ID, "de", db.TranslationStatusNeedsReview)
	job := testutil.SeedJob(t, q, file.ID, "de")
	testutil.SeedJobStep(t, q, p.SettingsProfileID, job.ID, assignee)
	return job, t1, t2
}

func (f *holdFixture) do(t *testing.T, u db.User, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	tenant := f.tenant
	if u.ID == f.otherUser.ID {
		tenant = f.otherTenant
	}
	return testutil.DoRequestWithRole(t, f.router, method, path, body, u.ID, tenant, f.roles[u.ID])
}

func holdPath(jobID, translationID int64) string {
	return fmt.Sprintf("/api/jobs/%d/holds/%d", jobID, translationID)
}

func acquire(t *testing.T, f *holdFixture, u db.User, tr db.Translation, tab string) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, u, http.MethodPut, holdPath(f.job.ID, tr.ID), map[string]any{"tab_id": tab})
}

func holderOf(t *testing.T, f *holdFixture, translationID int64) (userID int64, tab string, ok bool) {
	t.Helper()
	err := f.pool.QueryRow(context.Background(),
		`SELECT user_id, tab_id FROM translation_holds WHERE translation_id = $1 AND expires_at > now()`,
		translationID).Scan(&userID, &tab)
	if err != nil {
		return 0, "", false
	}
	return userID, tab, true
}

func TestIntegrationHold_SecondUserIsRefusedWithTheHoldersName(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()

	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-ana"), http.StatusOK)

	rec := acquire(t, f, f.pm, f.t1, "tab-pm")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Holder struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"holder"`
	}
	testutil.DecodeJSON(t, rec, &body)
	if body.Error.Code != "segment_held" || body.Holder.ID != f.linguist.ID || body.Holder.Name == "" {
		t.Fatalf("refusal = %+v, want code segment_held naming the linguist", body)
	}
	if uid, _, ok := holderOf(t, f, f.t1.ID); !ok || uid != f.linguist.ID {
		t.Errorf("hold now belongs to %d (live %v), want the linguist", uid, ok)
	}

	// The same project manager may take a segment nobody holds.
	testutil.AssertStatus(t, acquire(t, f, f.pm, f.t2, "tab-pm"), http.StatusOK)
}

func TestIntegrationHold_RenewMoveAndRelease(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	ctx := context.Background()

	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-1"), http.StatusOK)
	var acquiredAt string
	if err := f.pool.QueryRow(ctx, `SELECT acquired_at::text FROM translation_holds WHERE translation_id = $1`, f.t1.ID).Scan(&acquiredAt); err != nil {
		t.Fatal(err)
	}

	// Renewing keeps acquired_at and moves expires_at forward.
	if _, err := f.pool.Exec(ctx, `UPDATE translation_holds SET expires_at = now() + interval '5 seconds' WHERE translation_id = $1`, f.t1.ID); err != nil {
		t.Fatal(err)
	}
	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-1"), http.StatusOK)
	var extended bool
	if err := f.pool.QueryRow(ctx, `SELECT expires_at > now() + interval '60 seconds' FROM translation_holds WHERE translation_id = $1`, f.t1.ID).Scan(&extended); err != nil {
		t.Fatal(err)
	}
	if !extended {
		t.Error("a renewal did not push expires_at forward")
	}
	var again string
	if err := f.pool.QueryRow(ctx, `SELECT acquired_at::text FROM translation_holds WHERE translation_id = $1`, f.t1.ID).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if again != acquiredAt {
		t.Errorf("renewal moved acquired_at from %s to %s", acquiredAt, again)
	}

	// Moving the tab to the next segment releases the first.
	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t2, "tab-1"), http.StatusOK)
	if _, _, ok := holderOf(t, f, f.t1.ID); ok {
		t.Error("moving to segment 2 kept the hold on segment 1")
	}
	testutil.AssertStatus(t, acquire(t, f, f.pm, f.t1, "tab-pm"), http.StatusOK)

	// Release frees it for others.
	rec := f.do(t, f.linguist, http.MethodDelete, holdPath(f.job.ID, f.t2.ID)+"?tab_id=tab-1", nil)
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	testutil.AssertStatus(t, acquire(t, f, f.pm, f.t2, "tab-pm"), http.StatusOK)
}

func TestIntegrationHold_SameUserSecondTabTakesItOver(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()

	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-1"), http.StatusOK)
	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-2"), http.StatusOK)
	if _, tab, _ := holderOf(t, f, f.t1.ID); tab != "tab-2" {
		t.Fatalf("hold tab = %q, want tab-2", tab)
	}
	// Closing tab 1 leaves tab 2's hold alone.
	rec := f.do(t, f.linguist, http.MethodDelete, holdPath(f.job.ID, f.t1.ID)+"?tab_id=tab-1", nil)
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	if _, _, ok := holderOf(t, f, f.t1.ID); !ok {
		t.Error("tab 1's release dropped tab 2's hold")
	}
	// Another user's release never touches it.
	rec = f.do(t, f.pm, http.MethodDelete, holdPath(f.job.ID, f.t1.ID)+"?tab_id=tab-2", nil)
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	if _, _, ok := holderOf(t, f, f.t1.ID); !ok {
		t.Error("another user's release dropped the hold")
	}
}

func TestIntegrationHold_ExpiredHoldIsFree(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()

	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-1"), http.StatusOK)
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE translation_holds SET expires_at = now() - interval '1 second' WHERE translation_id = $1`, f.t1.ID); err != nil {
		t.Fatal(err)
	}
	testutil.AssertStatus(t, acquire(t, f, f.pm, f.t1, "tab-pm"), http.StatusOK)
	if uid, _, _ := holderOf(t, f, f.t1.ID); uid != f.pm.ID {
		t.Errorf("expired hold not taken over: holder %d", uid)
	}
}

func TestIntegrationHold_ListShowsLiveHoldsWithNames(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()

	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-1"), http.StatusOK)
	testutil.AssertStatus(t, acquire(t, f, f.pm, f.t2, "tab-pm"), http.StatusOK)
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE translation_holds SET expires_at = now() - interval '1 second' WHERE translation_id = $1`, f.t2.ID); err != nil {
		t.Fatal(err)
	}

	rec := f.do(t, f.pm, http.MethodGet, fmt.Sprintf("/api/jobs/%d/holds", f.job.ID), nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var body struct {
		Data []struct {
			TranslationID int64  `json:"translation_id"`
			UserID        int64  `json:"user_id"`
			HolderName    string `json:"holder_name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &body)
	if len(body.Data) != 1 || body.Data[0].TranslationID != f.t1.ID || body.Data[0].UserID != f.linguist.ID || body.Data[0].HolderName == "" {
		t.Fatalf("holds = %+v, want only the linguist's live hold on segment 1", body.Data)
	}
}

func TestIntegrationHold_RoutesAreScoped(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()

	// Another org's job, and another org's translation through own job.
	testutil.AssertStatus(t, f.do(t, f.pm, http.MethodPut, holdPath(f.otherJob.ID, f.otherT.ID), map[string]any{"tab_id": "x"}), http.StatusNotFound)
	testutil.AssertStatus(t, f.do(t, f.pm, http.MethodPut, holdPath(f.job.ID, f.otherT.ID), map[string]any{"tab_id": "x"}), http.StatusNotFound)
	testutil.AssertStatus(t, f.do(t, f.otherUser, http.MethodGet, fmt.Sprintf("/api/jobs/%d/holds", f.job.ID), nil), http.StatusNotFound)
	if _, _, ok := holderOf(t, f, f.otherT.ID); ok {
		t.Error("a cross-tenant acquire wrote a hold")
	}
	// A linguist not assigned to the job is refused.
	rec := f.do(t, f.outsider, http.MethodPut, holdPath(f.job.ID, f.t1.ID), map[string]any{"tab_id": "x"})
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("unassigned linguist: status %d, want 403 or 404", rec.Code)
	}
	// A tab id is required.
	testutil.AssertStatus(t, f.do(t, f.pm, http.MethodPut, holdPath(f.job.ID, f.t1.ID), map[string]any{}), http.StatusBadRequest)
	// Positive control: the same calls in the caller's own org work.
	testutil.AssertStatus(t, f.do(t, f.otherUser, http.MethodPut, holdPath(f.otherJob.ID, f.otherT.ID), map[string]any{"tab_id": "x"}), http.StatusOK)
}

func TestIntegrationHold_RefusedWhereTheUserCannotEdit(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	ctx := context.Background()

	// A locked segment takes no hold.
	if err := f.q.LockTranslation(ctx, f.t2.ID); err != nil {
		t.Fatal(err)
	}
	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t2, "tab-1"), http.StatusConflict)
	if _, _, ok := holderOf(t, f, f.t2.ID); ok {
		t.Error("a locked segment got a hold")
	}

	// The linguist holds segment 1; the task completes; the next renewal is
	// refused with the task's code and frees the segment.
	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-1"), http.StatusOK)
	if _, err := f.pool.Exec(ctx, `UPDATE job_step_assignments SET status = 'completed', completed_at = now() WHERE job_id = $1`, f.job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE jobs SET current_step_id = NULL WHERE id = $1`, f.job.ID); err != nil {
		t.Fatal(err)
	}
	rec := acquire(t, f, f.linguist, f.t1, "tab-1")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	if _, _, ok := holderOf(t, f, f.t1.ID); ok {
		t.Error("the refused renewal kept the hold")
	}
}

// A tab holds one segment at a time, refused or not: moving to a segment it
// may not hold (locked, or not in this job) still frees the previous one.
func TestIntegrationHold_RefusalFreesTheTabsPreviousSegment(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()

	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-1"), http.StatusOK)
	if err := f.q.LockTranslation(context.Background(), f.t2.ID); err != nil {
		t.Fatal(err)
	}
	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t2, "tab-1"), http.StatusConflict)
	if _, _, ok := holderOf(t, f, f.t1.ID); ok {
		t.Error("a refused move kept the tab's hold on its previous segment")
	}

	testutil.AssertStatus(t, acquire(t, f, f.linguist, f.t1, "tab-1"), http.StatusOK)
	testutil.AssertStatus(t, f.do(t, f.linguist, http.MethodPut, holdPath(f.job.ID, f.otherT.ID), map[string]any{"tab_id": "tab-1"}), http.StatusNotFound)
	if _, _, ok := holderOf(t, f, f.t1.ID); ok {
		t.Error("a move to a translation outside the job kept the previous hold")
	}
}

// Tab ids come from the client, so the cap is the server's: a user holds at
// most MaxHoldsPerUserJob segments of a job, and taking one more ends the
// hold renewed longest ago.
func TestIntegrationHold_OneUserHoldsAtMostAFewSegmentsOfAJob(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	rows := []db.Translation{f.t1, f.t2}
	for i := len(rows); i <= hold.MaxHoldsPerUserJob; i++ {
		seg := testutil.SeedSegment(t, f.q, f.job.FileID, fmt.Sprintf("Extra %d", i), int32(i))
		rows = append(rows, testutil.SeedTranslation(t, f.q, seg.ID, "de", db.TranslationStatusNeedsReview))
	}
	for i, tr := range rows {
		testutil.AssertStatus(t, acquire(t, f, f.pm, tr, fmt.Sprintf("tab-%d", i)), http.StatusOK)
	}

	live := testutil.CountRows(t, f.pool,
		`SELECT count(*) FROM translation_holds WHERE user_id = $1 AND job_id = $2 AND expires_at > now()`, f.pm.ID, f.job.ID)
	if live != hold.MaxHoldsPerUserJob {
		t.Errorf("live holds = %d, want %d", live, hold.MaxHoldsPerUserJob)
	}
	if _, _, ok := holderOf(t, f, rows[0].ID); ok {
		t.Error("the hold renewed longest ago is still live")
	}
	if uid, _, ok := holderOf(t, f, rows[len(rows)-1].ID); !ok || uid != f.pm.ID {
		t.Error("the newest hold was not kept")
	}
}
