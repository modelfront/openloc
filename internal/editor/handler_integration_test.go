package editor_test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/editor"
	"openloc/internal/testutil"
)

type fixture struct {
	pool     *pgxpool.Pool
	q        db.Querier
	router   http.Handler
	userID   int64
	tenantID int64
	project  db.Project
	job      db.Job
	segs     []db.Segment
	trs      map[int64]db.Translation // by segment id
}

// A file of five segments: "Hello" twice (a repetition), one untranslated,
// one locked with a long QE note, one with a cross-reference chip.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	f := &fixture{pool: pool, q: q, router: testutil.NewTestRouter(pool), trs: map[int64]db.Translation{}}
	f.userID, f.tenantID = testutil.RegisterUser(t, q, "editor-idx")
	f.project = testutil.SeedProject(t, q, f.tenantID, "Editor", "en", []string{"de"})
	file := testutil.SeedFile(t, q, f.project.ID, "a.xml", db.FileFormatDocx)
	f.job = testutil.SeedJob(t, q, file.ID, "de")
	for i, src := range []string{"Hello", "Wet or dry", "Hello", "Untranslated", "Dry"} {
		f.segs = append(f.segs, testutil.SeedSegment(t, q, file.ID, src, int32(i)))
	}
	for _, s := range f.segs {
		f.trs[s.ID] = testutil.SeedTranslation(t, q, s.ID, "de", db.TranslationStatusNeedsReview)
	}
	f.exec(t, `UPDATE translations SET active_text = 'Hallo', status = 'approved' WHERE id = $1`, f.trs[f.segs[0].ID].ID)
	f.exec(t, `UPDATE translations SET active_text = 'Nass oder trocken' WHERE id = $1`, f.trs[f.segs[1].ID].ID)
	f.exec(t, `UPDATE translations SET active_text = 'Trocken', locked = true, qe_note = 'a long vendor note' WHERE id = $1`, f.trs[f.segs[4].ID].ID)
	f.exec(t, `UPDATE segments SET segment_key = 'k-dry' WHERE id = $1`, f.segs[4].ID)
	f.exec(t, `UPDATE segments SET source_rich_text = 'Wet or <ph id="1" ref_key="k-dry">Dry</ph>' WHERE id = $1`, f.segs[1].ID)
	return f
}

func (f *fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(testutil.Ctx(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *fixture) get(t *testing.T, path string) (int, string) {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, http.MethodGet, fmt.Sprintf("/api/jobs/%d/editor/%s", f.job.ID, path), nil, f.userID, f.tenantID)
	return rec.Code, rec.Body.String()
}

func getData[T any](t *testing.T, f *fixture, path string) T {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, http.MethodGet, fmt.Sprintf("/api/jobs/%d/editor/%s", f.job.ID, path), nil, f.userID, f.tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
	}
	return testutil.DecodeData[T](t, rec)
}

func TestIntegrationEditor_IndexDescribesEverySegmentWithoutText(t *testing.T) {
	f := newFixture(t)
	idx := getData[editor.Index](t, f, "index")

	var wantIDs []int64
	for _, s := range f.segs {
		wantIDs = append(wantIDs, s.ID)
	}
	if !slices.Equal(idx.SegmentIDs, wantIDs) {
		t.Fatalf("segment ids = %v, want %v in document order", idx.SegmentIDs, wantIDs)
	}
	if idx.TranslationIDs[3] != f.trs[f.segs[3].ID].ID || idx.Statuses[3] != "needs_review" || idx.HasTarget[3] {
		t.Errorf("untranslated segment: id %d status %q has_target %v, want its row, needs_review, false", idx.TranslationIDs[3], idx.Statuses[3], idx.HasTarget[3])
	}
	if idx.TranslationIDs[0] != f.trs[f.segs[0].ID].ID || idx.Statuses[0] != "approved" || !idx.HasTarget[0] {
		t.Errorf("first segment: %d %q %v", idx.TranslationIDs[0], idx.Statuses[0], idx.HasTarget[0])
	}
	if idx.HasTarget[2] {
		t.Error("a translation with no text reads as having a target")
	}
	if !idx.Locked[4] || idx.QENoteHeads[4] != "a long" {
		t.Errorf("last segment: locked %v qe %q", idx.Locked[4], idx.QENoteHeads[4])
	}
	if want := []int8{1, 0, 2, 0, 0}; !slices.Equal(idx.Repetition, want) {
		t.Errorf("repetition = %v, want %v", idx.Repetition, want)
	}
	if want := (editor.RepetitionStats{Segments: 1, Words: 1, Characters: 5}); idx.RepetitionStats != want {
		t.Errorf("repetition stats = %+v, want %+v", idx.RepetitionStats, want)
	}
}

// A job's rows are the segments with a translation in its language: a shared
// segment that a split replaced in this language, and the rows another
// language made, are not rows of the job.
func TestIntegrationEditor_IndexHoldsOnlyTheJobsRows(t *testing.T) {
	f := newFixture(t)
	replaced := f.segs[1]
	piece := testutil.SeedLanguageSegment(t, f.q, replaced.FileID, "de", "Wet", replaced.Position, 0, "k-wet")
	f.exec(t, `UPDATE translations SET segment_id = $1 WHERE id = $2`, piece.ID, f.trs[replaced.ID].ID)
	second := testutil.SeedLanguageSegment(t, f.q, replaced.FileID, "de", "or dry", replaced.Position, 1, "k-wet")
	testutil.SeedTranslation(t, f.q, second.ID, "de", db.TranslationStatusNeedsReview)
	other := testutil.SeedLanguageSegment(t, f.q, replaced.FileID, "fr", "Mouillé", replaced.Position, 0, "k-wet")
	testutil.SeedTranslation(t, f.q, other.ID, "fr", db.TranslationStatusNeedsReview)

	idx := getData[editor.Index](t, f, "index")
	want := []int64{f.segs[0].ID, piece.ID, second.ID, f.segs[2].ID, f.segs[3].ID, f.segs[4].ID}
	if !slices.Equal(idx.SegmentIDs, want) {
		t.Fatalf("segment ids = %v, want %v", idx.SegmentIDs, want)
	}
	if !slices.Equal(idx.Positions, []int32{0, 1, 1, 2, 3, 4}) || !slices.Equal(idx.SubPositions, []int32{0, 0, 1, 0, 0, 0}) {
		t.Errorf("positions %v sub-positions %v", idx.Positions, idx.SubPositions)
	}
	if idx.TranslationIDs[1] != f.trs[replaced.ID].ID {
		t.Errorf("the first piece carries translation %d, want the moved row %d", idx.TranslationIDs[1], f.trs[replaced.ID].ID)
	}

	hits := getData[editor.SearchResult](t, f, "search?q=Mouill")
	if len(hits.SegmentIDs) != 0 {
		t.Errorf("search found another language's row: %v", hits.SegmentIDs)
	}

	rows := getData[[]editor.Row](t, f, fmt.Sprintf("rows?segment_ids=%d,%d,%d", piece.ID, second.ID, other.ID))
	if len(rows) != 2 || rows[0].Segment.ID != piece.ID || rows[1].Segment.ID != second.ID {
		t.Fatalf("rows = %+v, want the job's two pieces and not the other language's row", rows)
	}
	if rows[0].Translation == nil || rows[0].Translation.ID != f.trs[replaced.ID].ID {
		t.Errorf("the first piece's row carries %+v, want the moved translation", rows[0].Translation)
	}
}

func TestIntegrationEditor_RowsByIDInDocumentOrder(t *testing.T) {
	f := newFixture(t)

	rows := getData[[]editor.Row](t, f, fmt.Sprintf("rows?segment_ids=%d,%d,%d", f.segs[4].ID, f.segs[3].ID, f.segs[1].ID))
	if len(rows) != 3 || rows[0].Segment.ID != f.segs[1].ID || rows[1].Segment.ID != f.segs[3].ID || rows[2].Segment.ID != f.segs[4].ID {
		t.Fatalf("rows = %+v, want segments 1, 3 and 4 in document order", rows)
	}
	if tr := rows[0].Translation; tr == nil || tr.ActiveText == nil || *tr.ActiveText != "Nass oder trocken" {
		t.Errorf("row text missing: %+v", tr)
	}
	if tr := rows[1].Translation; tr == nil || tr.ActiveText != nil {
		t.Errorf("the untranslated segment: %+v, want its row with no text", rows[1].Translation)
	}

	many := strings.TrimSuffix(strings.Repeat("1,", editor.MaxRows+1), ",")
	for _, path := range []string{"rows", "rows?segment_ids=", "rows?segment_ids=x", "rows?segment_ids=" + many} {
		if code, body := f.get(t, path); code != http.StatusBadRequest {
			t.Errorf("GET %s = %d %s, want 400", path, code, body)
		}
	}
}

func TestIntegrationEditor_SearchMatchesLikeTheFilterBar(t *testing.T) {
	f := newFixture(t)
	ids := func(i ...int) []int64 {
		var out []int64
		for _, n := range i {
			out = append(out, f.segs[n].ID)
		}
		return out
	}
	for _, tc := range []struct {
		query string
		want  []int64
	}{
		{"q=dry", ids(1, 4)},
		{"q=%20dry%20", ids(1, 4)},
		{"q=Dry&case=1", ids(4)},
		{"q=dry&target=0", ids(1, 4)},
		{"q=trocken&source=0", ids(1, 4)},
		{"q=trocken&source=0&case=1", ids(1)},
		{"q=" + url.QueryEscape("^(Hello|Hallo)$") + "&regex=1", ids(0, 2)},
		{"q=" + url.QueryEscape("^hello") + "&regex=1", ids(0, 2)},
		{"q=" + url.QueryEscape("("), nil},
		{"q=dry&source=0&target=0", nil},
	} {
		got := getData[editor.SearchResult](t, f, "search?"+tc.query)
		if !slices.Equal(got.SegmentIDs, tc.want) {
			t.Errorf("search %s = %v, want %v", tc.query, got.SegmentIDs, tc.want)
		}
	}
	if code, _ := f.get(t, "search?q="+url.QueryEscape("(")+"&regex=1"); code != http.StatusBadRequest {
		t.Errorf("an invalid regex answered %d, want 400", code)
	}
}

func TestIntegrationEditor_RefsResolveTheReferencedSegment(t *testing.T) {
	f := newFixture(t)
	refs := getData[[]editor.Ref](t, f, "refs")
	want := []editor.Ref{{FileID: f.job.FileID, Key: "k-dry", SegmentID: f.segs[4].ID, Source: "Dry", Target: "Trocken"}}
	if !slices.Equal(refs, want) {
		t.Fatalf("refs = %+v, want %+v", refs, want)
	}
}

func TestIntegrationEditor_WordCandidatesContainTheWord(t *testing.T) {
	f := newFixture(t)
	got := getData[[]editor.WordCandidate](t, f, "word?word=trocken")
	if len(got) != 1 || got[0].ID != f.trs[f.segs[1].ID].ID {
		t.Fatalf("candidates for \"trocken\" = %+v, want only the case-sensitive substring match", got)
	}
	if !got[0].UpdatedAt.Valid {
		t.Error("candidate has no updated_at, which a write needs for its version check")
	}
	if code, _ := f.get(t, "word?word="); code != http.StatusBadRequest {
		t.Errorf("an empty word answered %d, want 400", code)
	}
}

// Every editor route is scoped to the caller's organization and, for a
// linguist, to jobs assigned to them.
func TestIntegrationEditor_RoutesAreScopedToTheJob(t *testing.T) {
	f := newFixture(t)
	otherUser, otherTenant := testutil.RegisterUser(t, f.q, "editor-other")
	linguist := testutil.SeedUserWithRole(t, f.q, f.tenantID, "editor-linguist@test.com", "pass", "linguist")

	// Another file's segment in the same organization is not returned by id.
	otherFile := testutil.SeedFile(t, f.q, f.project.ID, "b.xml", db.FileFormatDocx)
	foreign := testutil.SeedSegment(t, f.q, otherFile.ID, "foreign", 0)
	rows := getData[[]editor.Row](t, f, fmt.Sprintf("rows?segment_ids=%d", foreign.ID))
	if len(rows) != 0 {
		t.Errorf("another file's segment came back: %+v", rows)
	}

	for _, path := range []string{"index", fmt.Sprintf("rows?segment_ids=%d", f.segs[0].ID), "search?q=dry", "refs", "word?word=Hallo"} {
		p := fmt.Sprintf("/api/jobs/%d/editor/%s", f.job.ID, path)
		if rec := testutil.DoRequest(t, f.router, http.MethodGet, p, nil, otherUser, otherTenant); rec.Code != http.StatusNotFound {
			t.Errorf("another organization: GET %s = %d, want 404", path, rec.Code)
		}
		if rec := testutil.DoRequestWithRole(t, f.router, http.MethodGet, p, nil, linguist.ID, f.tenantID, "linguist"); rec.Code != http.StatusForbidden {
			t.Errorf("unassigned linguist: GET %s = %d, want 403", path, rec.Code)
		}
	}
}
