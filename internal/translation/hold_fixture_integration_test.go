package translation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// holdFixture: one org with the holder, another project manager and an admin,
// and a de job whose rows all hold a draft. "Save" occurs three times, so a
// confirm of one has two propagation siblings: the held row and twin2.
type holdFixture struct {
	pool                 *pgxpool.Pool
	q                    db.Querier
	router               http.Handler
	tenant               int64
	holder, other, admin db.User
	roles                map[int64]string
	fileID               int64
	settingsProfile      int64
	job                  db.Job
	// held: source "Save"; free: "Open"; twin, twin2: "Save".
	held, free, twin, twin2 db.Translation
}

func newHoldFixture(t *testing.T) *holdFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	f := &holdFixture{pool: pool, q: q, router: testutil.NewTestRouter(pool)}
	f.tenant = testutil.SeedTenant(t, q, "HoldWrites").ID
	f.holder = testutil.SeedUserWithRole(t, q, f.tenant, "ana@holdwrites.test", "pw", "project_manager")
	f.other = testutil.SeedUserWithRole(t, q, f.tenant, "ben@holdwrites.test", "pw", "project_manager")
	f.admin = testutil.SeedUserWithRole(t, q, f.tenant, "ada@holdwrites.test", "pw", "admin")
	f.roles = map[int64]string{f.holder.ID: "project_manager", f.other.ID: "project_manager", f.admin.ID: "admin"}

	p := testutil.SeedProject(t, q, f.tenant, "Hold writes", "en", []string{"de"})
	file := testutil.SeedFile(t, q, p.ID, "doc.txt", db.FileFormatTxt)
	f.fileID = file.ID
	f.settingsProfile = p.SettingsProfileID
	f.job = testutil.SeedJob(t, q, file.ID, "de")
	row := func(src string, pos int32, text string) db.Translation {
		seg := testutil.SeedSegment(t, q, file.ID, src, pos)
		tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
		out, err := q.SaveTranslationDraft(context.Background(), db.SaveTranslationDraftParams{ID: tr.ID, HumanText: &text})
		if err != nil {
			t.Fatalf("seed draft: %v", err)
		}
		return out
	}
	f.held = row("Save", 0, "Speichern")
	f.free = row("Open", 1, "Öffnen")
	f.twin = row("Save", 2, "Speichern")
	f.twin2 = row("Save", 3, "Speichern")
	return f
}

// hold makes u hold tr for 90 seconds.
func (f *holdFixture) hold(t *testing.T, tr db.Translation, u db.User) {
	t.Helper()
	testutil.SeedHold(t, f.q, f.tenant, f.job.ID, tr.ID, u.ID)
}

// expire moves tr's hold into the past.
func (f *holdFixture) expire(t *testing.T, tr db.Translation) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(),
		"UPDATE translation_holds SET expires_at = now() - interval '1 minute' WHERE translation_id = $1", tr.ID); err != nil {
		t.Fatal(err)
	}
}

func (f *holdFixture) do(t *testing.T, u db.User, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, method, path, body, u.ID, f.tenant, f.roles[u.ID])
}

func (f *holdFixture) trPath(tr db.Translation, suffix string) string {
	return fmt.Sprintf("/api/files/%d/translations/%d%s", f.fileID, tr.ID, suffix)
}

func (f *holdFixture) get(t *testing.T, tr db.Translation) db.Translation {
	t.Helper()
	got, err := f.q.GetTranslation(context.Background(), tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// assertUnchanged fails when the row's text, status, lock or version moved.
func (f *holdFixture) assertUnchanged(t *testing.T, before db.Translation) {
	t.Helper()
	got := f.get(t, before)
	if ptr.Deref(got.ActiveText) != ptr.Deref(before.ActiveText) || got.Status != before.Status ||
		got.Locked != before.Locked || !got.UpdatedAt.Time.Equal(before.UpdatedAt.Time) {
		t.Errorf("held row %d changed: text %q status %s locked %v, want %q %s %v",
			before.ID, ptr.Deref(got.ActiveText), got.Status, got.Locked, ptr.Deref(before.ActiveText), before.Status, before.Locked)
	}
}

// wantHeld asserts a 409 segment_held naming the holder.
func wantHeld(t *testing.T, rec *httptest.ResponseRecorder, holder db.User) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if body.Error.Code != "segment_held" || !strings.Contains(body.Error.Message, holder.Name) {
		t.Fatalf("error = %+v, want code segment_held naming %q", body.Error, holder.Name)
	}
}
