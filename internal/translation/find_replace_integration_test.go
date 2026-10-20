package translation_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
	"openloc/internal/translation"
)

// frFixture is one es job; rows are added per test with row().
type frFixture struct {
	pool             *pgxpool.Pool
	q                db.Querier
	router           http.Handler
	userID, tenantID int64
	projectID        int64
	fileID           int64
	job              db.Job
	pos              int32
}

func newFRFixture(t *testing.T) *frFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	f := &frFixture{pool: pool, q: q, router: testutil.NewTestRouter(pool)}
	f.userID, f.tenantID = registerUser(t, q, "fr-draft")
	f.projectID = createProject(t, f.router, "FR Draft Project", f.userID, f.tenantID)
	file := testutil.SeedFile(t, q, f.projectID, "fr.txt", db.FileFormatTxt)
	f.fileID = file.ID
	f.job = testutil.SeedJob(t, q, file.ID, "es")
	return f
}

// row seeds a translation with the given text, rich text (nil = none) and status.
func (f *frFixture) row(t *testing.T, fileID int64, text string, rich *string, status db.TranslationStatus) int64 {
	t.Helper()
	f.pos++
	seg := testutil.SeedSegment(t, f.q, fileID, fmt.Sprintf("source %d", f.pos), f.pos)
	tr := testutil.SeedTranslation(t, f.q, seg.ID, "es", status)
	if _, err := f.pool.Exec(context.Background(),
		"UPDATE translations SET active_text = $2, active_rich_text = $3 WHERE id = $1", tr.ID, text, rich); err != nil {
		t.Fatal(err)
	}
	return tr.ID
}

type frRow struct {
	text, rich *string
	status     db.TranslationStatus
	editedBy   *int64
}

func (f *frFixture) read(t *testing.T, id int64) frRow {
	t.Helper()
	var r frRow
	if err := f.pool.QueryRow(context.Background(),
		"SELECT active_text, active_rich_text, status, edited_by FROM translations WHERE id = $1", id,
	).Scan(&r.text, &r.rich, &r.status, &r.editedBy); err != nil {
		t.Fatal(err)
	}
	return r
}

type frResp struct {
	Data struct {
		Replaced    int `json:"replaced"`
		Segments    int `json:"segments"`
		Locked      int `json:"locked"`
		SkippedTags int `json:"skipped_tags"`
		Conflicts   int `json:"conflicts"`
	} `json:"data"`
}

func (f *frFixture) replace(t *testing.T, body map[string]any) frResp {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/find-replace", f.job.ID), body, f.userID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp frResp
	testutil.DecodeJSON(t, rec, &resp)
	return resp
}

// Replace All is an edit, not a sign-off: a confirmed row it changes goes back
// to needs_review as the user's draft, like any other edit.
func TestIntegrationFindReplace_ReplaceAllWritesDrafts(t *testing.T) {
	f := newFRFixture(t)
	id := f.row(t, f.fileID, "Hello World", nil, db.TranslationStatusApproved)

	resp := f.replace(t, map[string]any{"find": "Hello", "replace": "Hola", "case_sensitive": true})
	if resp.Data.Replaced != 1 || resp.Data.Segments != 1 {
		t.Errorf("response = %+v, want 1 replaced in 1 segment", resp.Data)
	}
	got := f.read(t, id)
	if got.text == nil || *got.text != "Hola World" {
		t.Fatalf("text = %v, want Hola World", got.text)
	}
	if got.status != db.TranslationStatusNeedsReview {
		t.Errorf("status = %s, want needs_review (a replace must not confirm)", got.status)
	}
	if got.editedBy == nil || *got.editedBy != f.userID {
		t.Errorf("edited_by = %v, want the user", got.editedBy)
	}
}

// Replace edits only the text runs of the tagged form, so the tags survive. A
// match that straddles a tag cannot be replaced safely: that row is skipped
// and left as it was.
func TestIntegrationFindReplace_KeepsTags(t *testing.T) {
	f := newFRFixture(t)
	tagged := f.row(t, f.fileID, "Click Save now", ptr.To(`Click <g id="1">Save</g> now`), db.TranslationStatusNeedsReview)
	straddle := f.row(t, f.fileID, "Save now", ptr.To(`<g id="1">Sa</g>ve now`), db.TranslationStatusNeedsReview)

	resp := f.replace(t, map[string]any{"find": "Save now", "replace": "Store today", "case_sensitive": true})
	if resp.Data.Segments != 0 || resp.Data.SkippedTags != 2 {
		t.Errorf("response = %+v, want both rows skipped for tags", resp.Data)
	}
	if got := f.read(t, straddle); *got.rich != `<g id="1">Sa</g>ve now` || *got.text != "Save now" {
		t.Errorf("skipped row changed: %q / %q", *got.text, *got.rich)
	}

	resp = f.replace(t, map[string]any{"find": "now", "replace": "today", "case_sensitive": true})
	if resp.Data.Segments != 2 {
		t.Errorf("response = %+v, want 2 segments", resp.Data)
	}
	got := f.read(t, tagged)
	if *got.text != "Click Save today" || got.rich == nil || *got.rich != `Click <g id="1">Save</g> today` {
		t.Errorf("tagged row = %q / %v, want tags kept", *got.text, got.rich)
	}
}

// Case-insensitive matching is done on the text itself, so a letter whose
// lower case is longer in bytes (İ) does not shift where the replacement lands.
func TestIntegrationFindReplace_CaseInsensitiveMultibyte(t *testing.T) {
	f := newFRFixture(t)
	id := f.row(t, f.fileID, "İstanbul abc def", nil, db.TranslationStatusNeedsReview)
	f.replace(t, map[string]any{"find": "ABC", "replace": "x", "case_sensitive": false})
	if got := f.read(t, id); *got.text != "İstanbul x def" {
		t.Errorf("text = %q, want %q", *got.text, "İstanbul x def")
	}
}

// translation_ids limits find and replace to those rows of the job; an id from
// another file is ignored.
func TestIntegrationFindReplace_Selection(t *testing.T) {
	f := newFRFixture(t)
	in := f.row(t, f.fileID, "Hello one", nil, db.TranslationStatusNeedsReview)
	out := f.row(t, f.fileID, "Hello two", nil, db.TranslationStatusNeedsReview)
	other := testutil.SeedFile(t, f.q, f.projectID, "other.txt", db.FileFormatTxt)
	testutil.SeedJob(t, f.q, other.ID, "es")
	foreign := f.row(t, other.ID, "Hello three", nil, db.TranslationStatusNeedsReview)

	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/find-replace", f.job.ID), map[string]any{
		"find": "Hello", "mode": "find", "case_sensitive": true, "translation_ids": []int64{in, foreign},
	}, f.userID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var found struct {
		Data struct {
			TranslationIDs []int64 `json:"translation_ids"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &found)
	if len(found.Data.TranslationIDs) != 1 || found.Data.TranslationIDs[0] != in {
		t.Errorf("find in selection = %v, want [%d]", found.Data.TranslationIDs, in)
	}

	resp := f.replace(t, map[string]any{"find": "Hello", "replace": "Hola", "case_sensitive": true, "translation_ids": []int64{in, foreign}})
	if resp.Data.Segments != 1 {
		t.Errorf("response = %+v, want 1 segment", resp.Data)
	}
	if got := f.read(t, in); *got.text != "Hola one" {
		t.Errorf("selected row = %q", *got.text)
	}
	for _, id := range []int64{out, foreign} {
		if got := f.read(t, id); (*got.text)[:5] != "Hello" {
			t.Errorf("row %d outside the selection changed to %q", id, *got.text)
		}
	}
}

// Replace All writes in chunks of MaxBatchDraftFixes and adds up the result,
// so a job with more matches than one chunk is still replaced in full.
func TestIntegrationFindReplace_MoreMatchesThanOneChunk(t *testing.T) {
	f := newFRFixture(t)
	n := translation.MaxBatchDraftFixes + 3
	for i := 0; i < n; i++ {
		f.row(t, f.fileID, fmt.Sprintf("Hello %d", i), nil, db.TranslationStatusNeedsReview)
	}
	resp := f.replace(t, map[string]any{"find": "Hello", "replace": "Hola", "case_sensitive": true})
	if resp.Data.Segments != n || resp.Data.Replaced != n {
		t.Errorf("response = %+v, want %d segments and %d replaced", resp.Data, n, n)
	}
	var left int
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM translations t JOIN segments s ON s.id = t.segment_id WHERE s.file_id = $1 AND t.active_text LIKE 'Hello%'",
		f.fileID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d rows still say Hello", left)
	}
}
