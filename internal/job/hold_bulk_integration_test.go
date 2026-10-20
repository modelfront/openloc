package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The bulk writes beside the editor — Apply TM, Apply MT and clearing a whole
// job or project — skip the segments another user has open and say how many.
// The user who runs them passes their own holds.

type holdBulkFixture struct {
	*ptFixture
	other db.User
}

func newHoldBulkFixture(t *testing.T) *holdBulkFixture {
	t.Helper()
	f := newPTFixture(t)
	other := testutil.SeedUserWithRole(t, f.q, f.tenant.ID, "ben@pt-hold.test", "pass", "project_manager")
	return &holdBulkFixture{ptFixture: f, other: other}
}

func (f *holdBulkFixture) hold(t *testing.T, id, userID int64) {
	t.Helper()
	testutil.SeedHold(t, f.q, f.tenant.ID, f.job.ID, id, userID)
}

// filled seeds a row with a draft, so a clear has something to clear.
func (f *holdBulkFixture) filled(t *testing.T, src string) int64 {
	t.Helper()
	id := f.row(t, f.file.ID, src)
	setActiveText(t, f.pool, id, "Entwurf")
	return id
}

func (f *holdBulkFixture) post(t *testing.T, path string, body any) map[string]any {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "POST", path, body, f.user.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func wantCount(t *testing.T, out map[string]any, key string, want int) {
	t.Helper()
	got, ok := out[key].(float64)
	if !ok || int(got) != want {
		t.Errorf("%s = %v, want %d (response %v)", key, out[key], want, out)
	}
}

func TestIntegrationApplyMT_SkipsSegmentsAnotherUserHolds(t *testing.T) {
	f := newHoldBulkFixture(t)
	open := f.row(t, f.file.ID, "Hello world")
	theirs := f.row(t, f.file.ID, "Hello moon")
	mine := f.row(t, f.file.ID, "Hello sun")
	f.hold(t, theirs, f.other.ID)
	f.hold(t, mine, f.user.ID)

	out := f.apply(t, "mt", nil)
	wantCount(t, out, "held", 1)
	for _, id := range []int64{open, mine} {
		if txt, _ := f.text(t, id); txt == nil {
			t.Errorf("row %d not filled by MT", id)
		}
	}
	if txt, _ := f.text(t, theirs); txt != nil {
		t.Errorf("row another user holds filled by MT: %q", *txt)
	}
}

// An exact TM match also confirms the row; the confirm passes the runner's own
// hold just like the fill, so the row is not left filled but unconfirmed.
func TestIntegrationApplyTM_SkipsSegmentsAnotherUserHolds(t *testing.T) {
	f := newHoldBulkFixture(t)
	open := f.row(t, f.file.ID, "Good morning")
	theirs := f.row(t, f.file.ID, "Good morning")
	mine := f.row(t, f.file.ID, "Good morning")
	f.hold(t, theirs, f.other.ID)
	f.hold(t, mine, f.user.ID)

	out := f.apply(t, "tm", nil)
	wantCount(t, out, "held", 1)
	for _, id := range []int64{open, mine} {
		if txt, st := f.text(t, id); txt == nil || *txt != "Guten Morgen" || st != db.TranslationStatusApproved {
			t.Errorf("row %d = %v / %s, want the TM match, confirmed", id, txt, st)
		}
	}
	if txt, st := f.text(t, theirs); txt != nil || st != db.TranslationStatusNeedsReview {
		t.Errorf("row another user holds written by TM: %v / %s", txt, st)
	}
}

// The history of an Apply MT fill names the user who ran it.
func TestIntegrationApplyMT_HistoryNamesTheRunner(t *testing.T) {
	f := newPTFixture(t)
	id := f.row(t, f.file.ID, "Hello world")
	f.apply(t, "mt", nil)

	var createdBy *int64
	if err := f.pool.QueryRow(context.Background(),
		"SELECT created_by FROM translation_history WHERE translation_id = $1 AND kind = 'mt'", id).Scan(&createdBy); err != nil {
		t.Fatalf("read MT history: %v", err)
	}
	if createdBy == nil || *createdBy != f.user.ID {
		t.Errorf("MT history created_by = %v, want the runner %d", createdBy, f.user.ID)
	}
}

func TestIntegrationClearJob_SkipsSegmentsAnotherUserHolds(t *testing.T) {
	f := newHoldBulkFixture(t)
	open := f.filled(t, "One")
	theirs := f.filled(t, "Two")
	mine := f.filled(t, "Three")
	f.hold(t, theirs, f.other.ID)
	f.hold(t, mine, f.user.ID)

	out := f.post(t, fmt.Sprintf("/api/jobs/%d/clear-translations", f.job.ID), map[string]string{"scope": "all"})
	wantCount(t, out, "cleared", 2)
	wantCount(t, out, "held", 1)
	for _, id := range []int64{open, mine} {
		if txt, _ := f.text(t, id); txt != nil {
			t.Errorf("row %d not cleared: %q", id, *txt)
		}
	}
	if txt, _ := f.text(t, theirs); txt == nil {
		t.Error("row another user holds was cleared")
	}
}

// The held count covers only what the clear would have cleared: an empty held
// row, or a confirmed one under "non_confirmed", is not a skip.
func TestIntegrationClearJob_HeldCountsOnlyClearableRows(t *testing.T) {
	f := newHoldBulkFixture(t)
	empty := f.row(t, f.file.ID, "Empty")
	confirmed := f.filled(t, "Confirmed")
	if _, err := f.pool.Exec(context.Background(), "UPDATE translations SET status = 'approved' WHERE id = $1", confirmed); err != nil {
		t.Fatal(err)
	}
	f.hold(t, empty, f.other.ID)
	f.hold(t, confirmed, f.other.ID)

	out := f.post(t, fmt.Sprintf("/api/jobs/%d/clear-translations", f.job.ID), map[string]string{"scope": "non_confirmed"})
	wantCount(t, out, "cleared", 0)
	wantCount(t, out, "held", 0)
}

func TestIntegrationClearProject_SkipsSegmentsAnotherUserHolds(t *testing.T) {
	f := newHoldBulkFixture(t)
	open := f.filled(t, "One")
	theirs := f.filled(t, "Two")
	mine := f.filled(t, "Three")
	f.hold(t, theirs, f.other.ID)
	f.hold(t, mine, f.user.ID)

	out := f.post(t, fmt.Sprintf("/api/projects/%d/clear-translations", f.file.ProjectID), map[string]string{"scope": "all"})
	wantCount(t, out, "cleared", 2)
	wantCount(t, out, "held", 1)
	if txt, _ := f.text(t, open); txt != nil {
		t.Errorf("free row not cleared: %q", *txt)
	}
	if txt, _ := f.text(t, theirs); txt == nil {
		t.Error("row another user holds was cleared")
	}
}
