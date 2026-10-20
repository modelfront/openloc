package translation_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/segmentbulk"
	"openloc/internal/testutil"
)

// "Approve all and complete" in the Complete step dialog: every translated,
// unapproved segment of the job is confirmed like a bulk Confirm — history,
// TM write-back, the QA gate — and the ones a QA error blocks are reported so
// the editor can stop before completing.

func (f *confirmFixture) confirmAll(t *testing.T, userID int64, role string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, "POST",
		fmt.Sprintf("/api/jobs/%d/translations/confirm-all", f.job.ID), nil, userID, f.tenantID, role)
}

func TestIntegrationConfirmAll_ApprovesEveryTranslatedSegment(t *testing.T) {
	f := newConfirmFixture(t)

	res := decodeResult(t, f.confirmAll(t, f.userID, "owner"))

	if !reflect.DeepEqual(res.Applied, []int64{f.draft}) {
		t.Errorf("applied = %v, want [%d]", res.Applied, f.draft)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].ID != f.qaBad || res.Skipped[0].Reason != segmentbulk.QAError {
		t.Errorf("skipped = %+v, want only the QA-error row as qa_error", res.Skipped)
	}
	for name, c := range map[string]struct {
		id   int64
		want db.TranslationStatus
	}{
		"draft":         {f.draft, db.TranslationStatusApproved},
		"QA error":      {f.qaBad, db.TranslationStatusNeedsReview},
		"empty":         {f.empty, db.TranslationStatusNeedsReview},
		"auto-approved": {f.autoApproved, db.TranslationStatusAutoApproved},
		"other file":    {f.otherFile, db.TranslationStatusNeedsReview},
	} {
		if got, _ := f.status(t, c.id); got != c.want {
			t.Errorf("%s: status = %s, want %s", name, got, c.want)
		}
	}
	if !f.inTM(t, "Good morning") {
		t.Error("the approved draft is not in TM — approve all must write TM like a confirm")
	}
}

// A job larger than one selection batch is approved in full: the route is not
// held to the 500-id cap of the selection routes.
func TestIntegrationConfirmAll_MoreThanOneBatch(t *testing.T) {
	f := newConfirmFixture(t)
	n := segmentbulk.MaxIDs + 1
	f.exec(t, `
		WITH segs AS (
			INSERT INTO segments (file_id, source_text, position)
			SELECT $1, 'Extra ' || g, 100 + g FROM generate_series(1, $2::int) g
			RETURNING id
		)
		INSERT INTO translations (segment_id, target_language, status, active_text)
		SELECT id, 'es', 'needs_review', 'Extra' FROM segs`, f.job.FileID, n)

	res := decodeResult(t, f.confirmAll(t, f.userID, "owner"))

	if len(res.Applied) != n+1 { // the extra rows and the fixture's draft
		t.Errorf("applied %d rows, want %d", len(res.Applied), n+1)
	}
	var left int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.target_language = 'es' AND t.status = 'needs_review'
		  AND coalesce(t.active_text, '') <> '' AND NOT t.locked`, f.job.FileID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 { // only the QA-error row
		t.Errorf("%d translated segments left unapproved, want 1", left)
	}
}

// Same gates as the selection routes: another tenant's job is a 404, and a
// linguist not assigned to the job is refused.
func TestIntegrationConfirmAll_Gates(t *testing.T) {
	f := newConfirmFixture(t)

	otherUser, otherTenant := testutil.RegisterUser(t, f.q, "confirmall-other")
	rec := testutil.DoRequestWithRole(t, f.router, "POST",
		fmt.Sprintf("/api/jobs/%d/translations/confirm-all", f.job.ID), nil, otherUser, otherTenant, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	ling := testutil.SeedUserWithRole(t, f.q, f.tenantID, "confirmall-ling@test.com", "pass", "linguist")
	testutil.AssertStatus(t, f.confirmAll(t, ling.ID, "linguist"), http.StatusForbidden)

	if got, _ := f.status(t, f.draft); got != db.TranslationStatusNeedsReview {
		t.Errorf("draft status = %s after refused requests, want needs_review", got)
	}
}

// A step advance turns the previous step's auto-approvals back into
// unconfirmed rows that keep their AI verdict. Approve all confirms them
// without a person reading them, so they go back to auto-approved and stay
// out of TM; a row a person touched, or confirms by hand, is approved and
// goes to TM like any other.
func TestIntegrationConfirmAll_MachineApprovedStaysOutOfTM(t *testing.T) {
	f := newConfirmFixture(t)
	seed := func(src string, pos int32, text, outcome string, editedBy *int64) int64 {
		seg := testutil.SeedSegment(t, f.q, f.job.FileID, src, pos)
		tr := testutil.SeedTranslation(t, f.q, seg.ID, "es", db.TranslationStatusNeedsReview)
		f.exec(t, "UPDATE translations SET active_text = $2, predict_outcome = $3, edited_by = $4 WHERE id = $1",
			tr.ID, text, outcome, editedBy)
		return tr.ID
	}
	machine := seed("Good evening", 10, "Buenas tardes", "approved", nil)
	ape := seed("See you", 11, "Hasta luego", "ape_approved", nil)
	touched := seed("Welcome", 12, "Bienvenido", "approved", &f.userID)
	rejected := seed("Goodbye", 13, "Adiós", "rejected", nil)
	byHand := seed("Please", 14, "Por favor", "approved", nil)

	rec := testutil.DoRequestWithRole(t, f.router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", f.job.FileID, byHand),
		map[string]any{"human_text": "Por favor", "editing_time_ms": 0}, f.userID, f.tenantID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	res := decodeResult(t, f.confirmAll(t, f.userID, "owner"))

	applied := map[int64]bool{}
	for _, id := range res.Applied {
		applied[id] = true
	}
	for name, c := range map[string]struct {
		id     int64
		status db.TranslationStatus
		inTM   bool
	}{
		"machine-approved":    {machine, db.TranslationStatusAutoApproved, false},
		"APE-approved":        {ape, db.TranslationStatusAutoApproved, false},
		"touched by a person": {touched, db.TranslationStatusApproved, true},
		"rejected by the AI":  {rejected, db.TranslationStatusApproved, true},
		"confirmed by hand":   {byHand, db.TranslationStatusApproved, true},
	} {
		if got, _ := f.status(t, c.id); got != c.status {
			t.Errorf("%s: status = %s, want %s", name, got, c.status)
		}
		if c.id != byHand && !applied[c.id] {
			t.Errorf("%s: not reported as applied", name)
		}
	}
	for name, src := range map[string]string{
		"machine-approved": "Good evening", "APE-approved": "See you",
	} {
		if f.inTM(t, src) {
			t.Errorf("%s: approve all sent machine-approved text to TM", name)
		}
	}
	for name, src := range map[string]string{
		"touched by a person": "Welcome", "rejected by the AI": "Goodbye", "confirmed by hand": "Please",
	} {
		if !f.inTM(t, src) {
			t.Errorf("%s: confirmed row not written to TM", name)
		}
	}
}

// A draft another user has open is left alone: Approve all reports it as
// held and it stays unapproved and out of TM, so the editor can stop before
// completing the step. The other drafts are approved.
func TestIntegrationConfirmAll_SkipsARowSomeoneElseHolds(t *testing.T) {
	f := newConfirmFixture(t)
	holder := testutil.SeedUserWithRole(t, f.q, f.tenantID, "ana@confirmall-hold.test", "pw", "project_manager")
	testutil.SeedHold(t, f.q, f.tenantID, f.job.ID, f.draft, holder.ID)
	seg := testutil.SeedSegment(t, f.q, f.job.FileID, "Good afternoon", 7)
	free := testutil.SeedTranslation(t, f.q, seg.ID, "es", db.TranslationStatusNeedsReview).ID
	f.setText(t, free, "Buenas tardes")

	res := decodeResult(t, f.confirmAll(t, f.userID, "owner"))

	if !reflect.DeepEqual(res.Applied, []int64{free}) {
		t.Errorf("applied = %v, want only the free draft [%d]", res.Applied, free)
	}
	reasons := map[int64]segmentbulk.SkipReason{}
	for _, s := range res.Skipped {
		reasons[s.ID] = s.Reason
	}
	if reasons[f.draft] != segmentbulk.Held {
		t.Errorf("held draft skipped as %q, want %q (skipped = %+v)", reasons[f.draft], segmentbulk.Held, res.Skipped)
	}
	if got, _ := f.status(t, f.draft); got != db.TranslationStatusNeedsReview {
		t.Errorf("held draft status = %s, want needs_review", got)
	}
	if f.inTM(t, "Good morning") {
		t.Error("the held draft reached TM")
	}
	if got, _ := f.status(t, free); got != db.TranslationStatusApproved {
		t.Errorf("free draft status = %s, want approved", got)
	}
}
