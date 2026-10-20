package translation_test

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// bulkResult is a segmentbulk result as the client reads it.
type bulkResult struct {
	Applied []int64 `json:"applied"`
	Skipped []struct {
		ID     int64  `json:"id"`
		Reason string `json:"reason"`
	} `json:"skipped"`
}

func (r bulkResult) reasons() map[int64]string {
	out := map[int64]string{}
	for _, s := range r.Skipped {
		out[s.ID] = s.Reason
	}
	return out
}

// Batch draft writes the free row and reports the held one in `held`.
func TestIntegrationHold_BatchDraftReportsHeld(t *testing.T) {
	f := newHoldFixture(t)
	f.hold(t, f.held, f.holder)
	before := f.get(t, f.held)

	type result struct {
		Applied []db.Translation `json:"applied"`
		Held    []int64          `json:"held"`
	}
	rec := f.do(t, f.other, http.MethodPut, fmt.Sprintf("/api/files/%d/translations/batch-draft", f.fileID),
		map[string]any{"fixes": []map[string]any{
			{"translation_id": f.held.ID, "human_text": "Sichern"},
			{"translation_id": f.free.ID, "human_text": "Öffnen!"},
		}})
	testutil.AssertStatus(t, rec, http.StatusOK)
	res := testutil.DecodeData[result](t, rec)
	if len(res.Applied) != 1 || res.Applied[0].ID != f.free.ID || !reflect.DeepEqual(res.Held, []int64{f.held.ID}) {
		t.Fatalf("applied %d rows, held %v; want the free row applied and [%d] held", len(res.Applied), res.Held, f.held.ID)
	}
	f.assertUnchanged(t, before)
}

// include_locked lifts a lock for an admin, never a hold.
func TestIntegrationHold_IncludeLockedNeverCoversHeld(t *testing.T) {
	f := newHoldFixture(t)
	if _, err := f.pool.Exec(context.Background(), "UPDATE translations SET locked = true WHERE id = $1", f.held.ID); err != nil {
		t.Fatal(err)
	}
	f.hold(t, f.held, f.holder)
	before := f.get(t, f.held)

	type result struct {
		Applied []db.Translation `json:"applied"`
		Locked  []int64          `json:"locked"`
		Held    []int64          `json:"held"`
	}
	rec := f.do(t, f.admin, http.MethodPut, fmt.Sprintf("/api/files/%d/translations/batch-draft", f.fileID),
		map[string]any{"include_locked": true, "fixes": []map[string]any{{"translation_id": f.held.ID, "human_text": "Sichern"}}})
	testutil.AssertStatus(t, rec, http.StatusOK)
	res := testutil.DecodeData[result](t, rec)
	if len(res.Applied) != 0 || !reflect.DeepEqual(res.Held, []int64{f.held.ID}) {
		t.Fatalf("applied %d, locked %v, held %v; want nothing applied and the row held", len(res.Applied), res.Locked, res.Held)
	}
	f.assertUnchanged(t, before)
}

// Find & replace leaves the held row alone and counts it in `held`.
func TestIntegrationHold_FindReplaceSkipsHeld(t *testing.T) {
	f := newHoldFixture(t)
	f.hold(t, f.held, f.holder)
	before := f.get(t, f.held)

	type result struct {
		Segments int `json:"segments"`
		Held     int `json:"held"`
	}
	rec := f.do(t, f.other, http.MethodPost, fmt.Sprintf("/api/jobs/%d/find-replace", f.job.ID),
		map[string]any{"find": "Speichern", "replace": "Sichern", "mode": "replace_all"})
	testutil.AssertStatus(t, rec, http.StatusOK)
	res := testutil.DecodeData[result](t, rec)
	if res.Segments != 2 || res.Held != 1 {
		t.Fatalf("segments %d held %d, want 2 written (the twins) and 1 held", res.Segments, res.Held)
	}
	f.assertUnchanged(t, before)
	if got := f.get(t, f.twin); ptr.Deref(got.ActiveText) != "Sichern" {
		t.Errorf("free twin = %q, want Sichern", ptr.Deref(got.ActiveText))
	}
}

// Propagation copies onto the free sibling, skips the held one and says so.
func TestIntegrationHold_PropagationSkipsHeldSibling(t *testing.T) {
	f := newHoldFixture(t)
	f.hold(t, f.held, f.holder)
	before := f.get(t, f.held)

	type result struct {
		Propagated int `json:"propagated"`
		Held       int `json:"held"`
	}
	rec := f.do(t, f.other, http.MethodPost, fmt.Sprintf("/api/files/%d/propagate", f.fileID),
		map[string]any{"source_text": "Save", "target_language": "de", "human_text": "Sichern", "exclude_id": f.twin.ID})
	testutil.AssertStatus(t, rec, http.StatusOK)
	res := testutil.DecodeData[result](t, rec)
	if res.Propagated != 1 || res.Held != 1 {
		t.Fatalf("propagated %d held %d, want 1 and 1", res.Propagated, res.Held)
	}
	f.assertUnchanged(t, before)
	if got := f.get(t, f.twin2); ptr.Deref(got.ActiveText) != "Sichern" {
		t.Errorf("free sibling = %q, want Sichern", ptr.Deref(got.ActiveText))
	}
}

// Bulk lock: a project manager's lock skips the held row; an admin's does not.
func TestIntegrationHold_BulkLockSkipsHeldForNonAdmin(t *testing.T) {
	f := newHoldFixture(t)
	f.hold(t, f.held, f.holder)
	path := fmt.Sprintf("/api/jobs/%d/translations/lock", f.job.ID)

	rec := f.do(t, f.other, http.MethodPost, path, map[string]any{"translation_ids": []int64{f.held.ID, f.free.ID}})
	testutil.AssertStatus(t, rec, http.StatusOK)
	res := testutil.DecodeData[bulkResult](t, rec)
	if !reflect.DeepEqual(res.Applied, []int64{f.free.ID}) || res.reasons()[f.held.ID] != "held" {
		t.Fatalf("manager's bulk lock = %+v, want the free row applied and the held one skipped as held", res)
	}
	if f.get(t, f.held).Locked {
		t.Fatal("held row was locked")
	}

	rec = f.do(t, f.admin, http.MethodPost, path, map[string]any{"translation_ids": []int64{f.held.ID}})
	testutil.AssertStatus(t, rec, http.StatusOK)
	res = testutil.DecodeData[bulkResult](t, rec)
	if !reflect.DeepEqual(res.Applied, []int64{f.held.ID}) {
		t.Fatalf("admin's bulk lock = %+v, want the held row applied", res)
	}
}

// Bulk clear skips the held row and reports it.
func TestIntegrationHold_BulkClearSkipsHeld(t *testing.T) {
	f := newHoldFixture(t)
	f.hold(t, f.held, f.holder)
	before := f.get(t, f.held)

	rec := f.do(t, f.other, http.MethodPost, fmt.Sprintf("/api/jobs/%d/translations/clear", f.job.ID),
		map[string]any{"translation_ids": []int64{f.held.ID, f.free.ID}})
	testutil.AssertStatus(t, rec, http.StatusOK)
	res := testutil.DecodeData[bulkResult](t, rec)
	if !reflect.DeepEqual(res.Applied, []int64{f.free.ID}) || res.reasons()[f.held.ID] != "held" {
		t.Fatalf("bulk clear = %+v, want the free row applied and the held one skipped as held", res)
	}
	f.assertUnchanged(t, before)
}

// Bulk confirm skips a row another user holds before its QA check runs: the
// QA-failing held row is reported as held and gets no stored finding. The
// holder's own bulk confirm still writes the row they hold.
func TestIntegrationHold_BulkConfirmSkipsHeldBeforeQA(t *testing.T) {
	cf := newConfirmFixture(t)
	holder := testutil.SeedUserWithRole(t, cf.q, cf.tenantID, "ana@bulkhold.test", "pw", "project_manager")
	for _, h := range []struct{ id, user int64 }{{cf.qaBad, holder.ID}, {cf.draft, cf.userID}} {
		testutil.SeedHold(t, cf.q, cf.tenantID, cf.job.ID, h.id, h.user)
	}

	res := decodeResult(t, cf.confirm(t, []int64{cf.qaBad, cf.draft}, "owner"))
	if !reflect.DeepEqual(res.Applied, []int64{cf.draft}) {
		t.Errorf("applied = %v, want the confirmer's own held row [%d]", res.Applied, cf.draft)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].ID != cf.qaBad || res.Skipped[0].Reason != "held" {
		t.Errorf("skipped = %+v, want [%d] held", res.Skipped, cf.qaBad)
	}
	if n := testutil.CountRows(t, cf.pool, "SELECT count(*) FROM qa_results WHERE translation_id = $1", cf.qaBad); n != 0 {
		t.Errorf("held row got %d QA findings: QA ran before the hold check", n)
	}
}
