package editor_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/editor"
	"openloc/internal/stepround"
	"openloc/internal/testutil"
)

// Each editor row carries its track-changes baseline: the text the segment held
// when its last finished step round ended. A confirmed edit and an unconfirmed
// draft both count, a later edit does not move it, and after a reject it is the
// text the rejected round left.
func TestIntegrationEditor_RowsCarryThePreviousStepText(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "TrackChangesCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@trackchanges.test", "pass", "owner")
	stranger := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@trackchanges.test", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "Track Changes", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	confirmedSeg := testutil.SeedSegment(t, q, file.ID, "Hello", 0)
	draftSeg := testutil.SeedSegment(t, q, file.ID, "World", 1)
	emptySeg := testutil.SeedSegment(t, q, file.ID, "Untouched", 2)
	confirmed := testutil.SeedTranslation(t, q, confirmedSeg.ID, "de", db.TranslationStatusNeedsReview)
	draft := testutil.SeedTranslation(t, q, draftSeg.ID, "de", db.TranslationStatusNeedsReview)
	untouched := testutil.SeedTranslation(t, q, emptySeg.ID, "de", db.TranslationStatusNeedsReview)
	j := testutil.SeedJob(t, q, file.ID, "de")
	steps := testutil.SeedWorkflow(t, q, project.SettingsProfileID, j.ID,
		testutil.WorkflowStepSpec{Name: "Translation", Status: "in_progress", Assignee: &owner.ID},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &owner.ID},
	)
	if err := stepround.Open(ctx, q, j.ID, steps[0], stepround.Keep); err != nil {
		t.Fatal(err)
	}

	as := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		return testutil.DoRequestWithRole(t, router, method, path, body, owner.ID, tenant.ID, "owner")
	}
	save := func(trID int64, text string, suffix string) {
		t.Helper()
		rec := as("PUT", fmt.Sprintf("/api/files/%d/translations/%d%s", file.ID, trID, suffix), map[string]any{"human_text": text})
		testutil.AssertStatus(t, rec, http.StatusOK)
	}
	path := fmt.Sprintf("/api/jobs/%d/editor/rows?segment_ids=%d,%d,%d", j.ID, confirmedSeg.ID, draftSeg.ID, emptySeg.ID)
	baselines := func() map[int64]*string {
		t.Helper()
		rec := as("GET", path, nil)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []editor.Row `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		out := map[int64]*string{}
		for _, r := range resp.Data {
			if r.Translation != nil {
				out[r.Translation.ID] = r.PreviousStepText
			}
		}
		return out
	}
	want := func(label string, got map[int64]*string, wantTexts map[int64]string) {
		t.Helper()
		for _, id := range []int64{confirmed.ID, draft.ID, untouched.ID} {
			text, ok := wantTexts[id]
			switch {
			case !ok && got[id] != nil:
				t.Errorf("%s: translation %d baseline = %q, want none", label, id, *got[id])
			case ok && (got[id] == nil || *got[id] != text):
				t.Errorf("%s: translation %d baseline = %v, want %q", label, id, got[id], text)
			}
		}
	}

	save(confirmed.ID, "Hallo", "")
	save(draft.ID, "Welt", "/draft")
	want("first step, nothing finished", baselines(), map[int64]string{})

	testutil.AssertStatus(t, as("POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", j.ID), nil), http.StatusOK)
	save(confirmed.ID, "Hallo zusammen", "")
	save(draft.ID, "Die Welt", "/draft")
	want("in review", baselines(), map[int64]string{confirmed.ID: "Hallo", draft.ID: "Welt"})

	testutil.AssertStatus(t, as("POST", fmt.Sprintf("/api/jobs/%d/workflow/reject", j.ID), map[string]any{"reason": "Check the terms"}), http.StatusOK)
	want("after a reject", baselines(), map[int64]string{confirmed.ID: "Hallo zusammen", draft.ID: "Die Welt"})

	// A linguist the job is not assigned to is refused; another organization
	// does not see the job.
	rec := testutil.DoRequestWithRole(t, router, "GET", path, nil, stranger.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	otherUser, otherTenant := testutil.RegisterUser(t, q, "trackchanges-other")
	rec = testutil.DoRequestWithRole(t, router, "GET", path, nil, otherUser, otherTenant, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}
