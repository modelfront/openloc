package editor_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/editor"
	"openloc/internal/testutil"
	tm "openloc/internal/translationmemory"
)

// scopeFixture is a project with three German files (A, B, Outside) and one
// French file, each job with a step in progress assigned to the linguist only
// for A. "Save" is in A and B; A holds a confirmed "Open the file", B the
// near match "Open the files", Outside a confirmed "Open the files!" that is
// not in the scope.
type scopeFixture struct {
	pool             *pgxpool.Pool
	q                db.Querier
	router           http.Handler
	ownerID, tenant  int64
	linguist         db.User
	project          db.Project
	jobA, jobB       db.Job
	jobOutside, jobF db.Job
	seg              map[string]db.Segment     // "A:Save" → segment
	tr               map[string]db.Translation // same keys
}

func newScopeFixture(t *testing.T) *scopeFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	f := &scopeFixture{pool: pool, q: q, router: testutil.NewTestRouter(pool),
		seg: map[string]db.Segment{}, tr: map[string]db.Translation{}}
	f.ownerID, f.tenant = testutil.RegisterUser(t, q, "editor-scope")
	f.linguist = testutil.SeedUserWithRole(t, q, f.tenant, "scope-linguist@test.com", "pw", "linguist")
	f.project = testutil.SeedProject(t, q, f.tenant, "Scope", "en", []string{"de", "fr"})
	seedFile := func(name, lang string, sources ...string) db.Job {
		file := testutil.SeedFile(t, q, f.project.ID, name, db.FileFormatJsonI18n)
		job := testutil.SeedJob(t, q, file.ID, lang)
		for i, src := range sources {
			s := testutil.SeedSegment(t, q, file.ID, src, int32(i))
			key := name + ":" + src
			f.seg[key] = s
			f.tr[key] = testutil.SeedTranslation(t, q, s.ID, lang, db.TranslationStatusNeedsReview)
		}
		return job
	}
	f.jobA = seedFile("A", "de", "Save", "Open the file")
	f.jobB = seedFile("B", "de", "Open the files", "Save", "Cancel")
	f.jobOutside = seedFile("Outside", "de", "Open the files!")
	f.jobF = seedFile("F", "fr", "Save")
	f.approve(t, "A:Open the file", "Datei öffnen")
	f.approve(t, "Outside:Open the files!", "Dateien öffnen!")
	assignee := map[int64]*int64{f.jobA.ID: &f.linguist.ID}
	for _, j := range []db.Job{f.jobA, f.jobB, f.jobOutside, f.jobF} {
		testutil.SeedJobStep(t, q, f.project.SettingsProfileID, j.ID, assignee[j.ID])
	}
	return f
}

func (f *scopeFixture) approve(t *testing.T, key, text string) {
	t.Helper()
	if _, err := f.pool.Exec(testutil.Ctx(), `UPDATE translations SET active_text = $2, status = 'approved' WHERE id = $1`,
		f.tr[key].ID, text); err != nil {
		t.Fatal(err)
	}
}

func scopePath(projectID int64, route string, jobs ...db.Job) string {
	ids := make([]string, len(jobs))
	for i, j := range jobs {
		ids[i] = fmt.Sprint(j.ID)
	}
	sep := "?"
	if strings.Contains(route, "?") {
		sep = "&"
	}
	return fmt.Sprintf("/api/projects/%d/editor/%s%sjobs=%s", projectID, route, sep, strings.Join(ids, ","))
}

// get sends a GET as userID in the fixture's tenant, with the linguist's
// role for the linguist.
func (f *scopeFixture) get(t *testing.T, userID int64, path string) *httptest.ResponseRecorder {
	t.Helper()
	role := "owner"
	if userID == f.linguist.ID {
		role = "linguist"
	}
	return testutil.DoRequestWithRole(t, f.router, http.MethodGet, path, nil, userID, f.tenant, role)
}

func scopeGet[T any](t *testing.T, f *scopeFixture, userID int64, path string) T {
	t.Helper()
	rec := f.get(t, userID, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
	}
	return testutil.DecodeData[T](t, rec)
}

func TestIntegrationEditorScope_IndexListsTheFilesInTheOrderAsked(t *testing.T) {
	f := newScopeFixture(t)
	idx := scopeGet[editor.Index](t, f, f.ownerID, scopePath(f.project.ID, "index", f.jobB, f.jobA))

	want := []int64{
		f.seg["B:Open the files"].ID, f.seg["B:Save"].ID, f.seg["B:Cancel"].ID,
		f.seg["A:Save"].ID, f.seg["A:Open the file"].ID,
	}
	if !slices.Equal(idx.SegmentIDs, want) {
		t.Fatalf("segment ids = %v, want B's rows then A's: %v", idx.SegmentIDs, want)
	}
	if len(idx.Files) != 2 {
		t.Fatalf("files = %+v, want B and A", idx.Files)
	}
	b, a := idx.Files[0], idx.Files[1]
	if b.JobID != f.jobB.ID || b.FirstRow != 0 || b.RowCount != 3 || b.FileName != "B" {
		t.Errorf("first file = %+v, want job B, rows 0..2", b)
	}
	if a.JobID != f.jobA.ID || a.FirstRow != 3 || a.RowCount != 2 {
		t.Errorf("second file = %+v, want job A, rows 3..4", a)
	}
	// "Save" repeats across the two files: B's comes first in the scope.
	if idx.Repetition[1] != editor.RepetitionSource || idx.Repetition[3] != editor.RepetitionRepeat {
		t.Errorf("repetition roles = %v, want B:Save the source and A:Save its repeat", idx.Repetition)
	}
	if idx.RepetitionStats.Segments != 1 {
		t.Errorf("repeated segments = %d, want 1", idx.RepetitionStats.Segments)
	}
}

// A one-row file before another file's first row is not a join: positions
// restart in each file, so position 0 meets position 0.
func TestIntegrationEditorScope_NoJoinAcrossFiles(t *testing.T) {
	f := newScopeFixture(t)
	idx := scopeGet[editor.Index](t, f, f.ownerID, scopePath(f.project.ID, "index", f.jobOutside, f.jobA))
	if idx.JoinsNext[0] {
		t.Errorf("joins_next on the last row of a file = true, want false")
	}
}

func TestIntegrationEditorScope_FilesSayWhetherTheCallerMayEdit(t *testing.T) {
	f := newScopeFixture(t)
	if _, err := f.pool.Exec(testutil.Ctx(), `UPDATE job_step_assignments SET status = 'completed' WHERE job_id = $1`, f.jobA.ID); err != nil {
		t.Fatal(err)
	}
	// A linguist may not reopen a completed step.
	if _, err := f.pool.Exec(testutil.Ctx(), `UPDATE job_step_assignments SET assigned_to = $1 WHERE job_id = $2`, f.linguist.ID, f.jobB.ID); err != nil {
		t.Fatal(err)
	}
	idx := scopeGet[editor.Index](t, f, f.linguist.ID, scopePath(f.project.ID, "index", f.jobA, f.jobB))
	if blk := idx.Files[0].EditBlock; blk == nil || blk.Code != "task_completed" {
		t.Errorf("completed file's edit block = %+v, want task_completed", blk)
	}
	if blk := idx.Files[1].EditBlock; blk != nil {
		t.Errorf("file in progress has edit block %+v, want none", blk)
	}
	// A manager's edit would reopen it, so the manager may edit.
	idx = scopeGet[editor.Index](t, f, f.ownerID, scopePath(f.project.ID, "index", f.jobA))
	if blk := idx.Files[0].EditBlock; blk != nil {
		t.Errorf("manager's edit block on a completed file = %+v, want none", blk)
	}
}

func TestIntegrationEditorScope_RefusesWhatItMayNotOpen(t *testing.T) {
	f := newScopeFixture(t)
	otherUser, otherTenant := testutil.RegisterUser(t, f.q, "editor-scope-other")
	otherProject := testutil.SeedProject(t, f.q, otherTenant, "Other", "en", []string{"de"})
	otherFile := testutil.SeedFile(t, f.q, otherProject.ID, "X", db.FileFormatJsonI18n)
	otherJob := testutil.SeedJob(t, f.q, otherFile.ID, "de")
	sameTenantProject := testutil.SeedProject(t, f.q, f.tenant, "Second", "en", []string{"de"})
	secondFile := testutil.SeedFile(t, f.q, sameTenantProject.ID, "Y", db.FileFormatJsonI18n)
	secondJob := testutil.SeedJob(t, f.q, secondFile.ID, "de")

	for _, tc := range []struct {
		name string
		user int64
		path string
		want int
	}{
		{"another tenant's job", f.ownerID, scopePath(f.project.ID, "index", f.jobA, otherJob), http.StatusNotFound},
		{"a job of another project", f.ownerID, scopePath(f.project.ID, "index", f.jobA, secondJob), http.StatusNotFound},
		{"another tenant asking", otherUser, scopePath(f.project.ID, "index", f.jobA), http.StatusNotFound},
		{"two target languages", f.ownerID, scopePath(f.project.ID, "index", f.jobA, f.jobF), http.StatusUnprocessableEntity},
		{"the same job twice", f.ownerID, scopePath(f.project.ID, "index", f.jobA, f.jobA), http.StatusBadRequest},
		{"no jobs", f.ownerID, fmt.Sprintf("/api/projects/%d/editor/index?jobs=", f.project.ID), http.StatusBadRequest},
		{"a linguist on a job not theirs", f.linguist.ID, scopePath(f.project.ID, "index", f.jobA, f.jobB), http.StatusForbidden},
		{"a linguist's own job", f.linguist.ID, scopePath(f.project.ID, "index", f.jobA), http.StatusOK},
		{"rows: a linguist on a job not theirs", f.linguist.ID,
			scopePath(f.project.ID, fmt.Sprintf("rows?segment_ids=%d", f.seg["B:Save"].ID), f.jobA, f.jobB), http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.get(t, tc.user, tc.path)
			if tc.user == otherUser {
				rec = testutil.DoRequest(t, f.router, http.MethodGet, tc.path, nil, otherUser, otherTenant)
			}
			if rec.Code != tc.want {
				t.Errorf("status = %d (%s), want %d", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}

	tooMany := make([]string, editor.MaxScopeJobs+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprint(i + 1)
	}
	path := fmt.Sprintf("/api/projects/%d/editor/index?jobs=%s", f.project.ID, strings.Join(tooMany, ","))
	if rec := f.get(t, f.ownerID, path); rec.Code != http.StatusBadRequest {
		t.Errorf("%d jobs: status %d, want 400", len(tooMany), rec.Code)
	}
}

func TestIntegrationEditorScope_RowsComeFromEveryFileButNoOther(t *testing.T) {
	f := newScopeFixture(t)
	ids := fmt.Sprintf("%d,%d,%d", f.seg["A:Save"].ID, f.seg["B:Cancel"].ID, f.seg["Outside:Open the files!"].ID)
	rows := scopeGet[[]editor.Row](t, f, f.ownerID, scopePath(f.project.ID, "rows?segment_ids="+ids, f.jobA, f.jobB))
	var got []int64
	for _, r := range rows {
		got = append(got, r.Segment.ID)
	}
	if want := []int64{f.seg["A:Save"].ID, f.seg["B:Cancel"].ID}; !slices.Equal(got, want) {
		t.Errorf("rows = %v, want A:Save and B:Cancel only (Outside is not in the scope)", got)
	}
}

func TestIntegrationEditorScope_FuzzyFindsConfirmedNearMatchesInTheScope(t *testing.T) {
	f := newScopeFixture(t)
	query := f.seg["B:Open the files"]
	path := scopePath(f.project.ID, fmt.Sprintf("fuzzy?segment_id=%d", query.ID), f.jobA, f.jobB)
	got := scopeGet[[]editor.FuzzyMatch](t, f, f.ownerID, path)

	if len(got) != 1 {
		t.Fatalf("matches = %+v, want only A's \"Open the file\" (Outside is not in the scope)", got)
	}
	m := got[0]
	score, _ := tm.LevenshteinScorer{}.Score(query.SourceText, "Open the file")
	if m.SegmentID != f.seg["A:Open the file"].ID || m.FileID != f.jobA.FileID || m.TargetText != "Datei öffnen" ||
		m.Percent != tm.ScoreToPercent(score) {
		t.Errorf("match = %+v, want A's segment at %d%%, as a TM match scores it", m, tm.ScoreToPercent(score))
	}

	// An unconfirmed near match is no match.
	if _, err := f.pool.Exec(testutil.Ctx(), `UPDATE translations SET status = 'needs_review' WHERE id = $1`, f.tr["A:Open the file"].ID); err != nil {
		t.Fatal(err)
	}
	if got := scopeGet[[]editor.FuzzyMatch](t, f, f.ownerID, path); len(got) != 0 {
		t.Errorf("matches after unconfirming = %+v, want none", got)
	}

	// A segment outside the scope is not looked up.
	outside := scopePath(f.project.ID, fmt.Sprintf("fuzzy?segment_id=%d", f.seg["Outside:Open the files!"].ID), f.jobA, f.jobB)
	if rec := f.get(t, f.ownerID, outside); rec.Code != http.StatusNotFound {
		t.Errorf("fuzzy for a segment outside the scope: %d, want 404", rec.Code)
	}
}

// Two files with the same key: each chip points at the key in its own file.
func TestIntegrationEditorScope_RefsStayInTheirFile(t *testing.T) {
	f := newScopeFixture(t)
	for _, key := range []string{"A:Open the file", "B:Cancel"} {
		if _, err := f.pool.Exec(testutil.Ctx(), `UPDATE segments SET segment_key = 'k' WHERE id = $1`, f.seg[key].ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(testutil.Ctx(), `UPDATE segments SET source_rich_text = 'See <ph id="1" ref_key="k">x</ph>' WHERE id = $1`,
		f.seg["A:Save"].ID); err != nil {
		t.Fatal(err)
	}
	refs := scopeGet[[]editor.Ref](t, f, f.ownerID, scopePath(f.project.ID, "refs", f.jobA, f.jobB))
	if len(refs) != 1 || refs[0].SegmentID != f.seg["A:Open the file"].ID || refs[0].FileID != f.jobA.FileID {
		t.Errorf("refs = %+v, want A's key k only (no chip in B points at k)", refs)
	}
}

// The scope's hold list covers every open file, and keeps the scope's gate.
func TestIntegrationEditorScope_HoldsCoverEveryFile(t *testing.T) {
	f := newScopeFixture(t)
	testutil.SeedHold(t, f.q, f.tenant, f.jobA.ID, f.tr["A:Save"].ID, f.ownerID)
	testutil.SeedHold(t, f.q, f.tenant, f.jobB.ID, f.tr["B:Save"].ID, f.ownerID)
	testutil.SeedHold(t, f.q, f.tenant, f.jobOutside.ID, f.tr["Outside:Open the files!"].ID, f.ownerID)

	type holder struct {
		TranslationID int64 `json:"translation_id"`
	}
	holds := scopeGet[[]holder](t, f, f.ownerID, scopePath(f.project.ID, "holds", f.jobA, f.jobB))
	var got []int64
	for _, h := range holds {
		got = append(got, h.TranslationID)
	}
	slices.Sort(got)
	want := []int64{f.tr["A:Save"].ID, f.tr["B:Save"].ID}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("holds = %v, want A's and B's only: %v", got, want)
	}
	if rec := f.get(t, f.linguist.ID, scopePath(f.project.ID, "holds", f.jobA, f.jobB)); rec.Code != http.StatusForbidden {
		t.Errorf("linguist on a job not theirs: %d, want 403", rec.Code)
	}
}

// The scope's comment counts are the open comments of its own files: a
// deleted comment and a file outside the scope count for nothing.
func TestIntegrationEditorScope_CommentCountsCoverTheOpenFilesOnly(t *testing.T) {
	f := newScopeFixture(t)
	comment := func(key string, deleted bool) {
		t.Helper()
		if _, err := f.pool.Exec(testutil.Ctx(),
			`INSERT INTO segment_comments (translation_id, user_id, content, deleted_at)
			 VALUES ($1, $2, 'note', CASE WHEN $3 THEN now() END)`, f.tr[key].ID, f.ownerID, deleted); err != nil {
			t.Fatal(err)
		}
	}
	comment("A:Save", false)
	comment("A:Save", false)
	comment("A:Save", true)
	comment("B:Cancel", false)
	comment("Outside:Open the files!", false)

	type count struct {
		TranslationID int64 `json:"translation_id"`
		CommentCount  int   `json:"comment_count"`
	}
	got := map[int64]int{}
	for _, c := range scopeGet[[]count](t, f, f.ownerID, scopePath(f.project.ID, "comment-counts", f.jobA, f.jobB)) {
		got[c.TranslationID] = c.CommentCount
	}
	want := map[int64]int{f.tr["A:Save"].ID: 2, f.tr["B:Cancel"].ID: 1}
	if len(got) != len(want) || got[f.tr["A:Save"].ID] != 2 || got[f.tr["B:Cancel"].ID] != 1 {
		t.Errorf("counts = %v, want %v (Outside not open, deleted comment left out)", got, want)
	}
	if rec := f.get(t, f.linguist.ID, scopePath(f.project.ID, "comment-counts", f.jobA, f.jobB)); rec.Code != http.StatusForbidden {
		t.Errorf("linguist on a job not theirs: %d, want 403", rec.Code)
	}
}

// A file with a finished step round says so, so the multi-file editor offers
// track changes; a file whose rounds are all open does not.
func TestIntegrationEditorScope_FilesSayWhetherAStepFinished(t *testing.T) {
	f := newScopeFixture(t)
	if _, err := f.pool.Exec(testutil.Ctx(), `INSERT INTO translation_step_statuses
		(tenant_id, translation_id, job_step_assignment_id, round, ended_at, end_reason, status)
		SELECT $1, $2, jsa.id, 1, now(), 'completed', 'approved' FROM job_step_assignments jsa WHERE jsa.job_id = $3`,
		f.tenant, f.tr["B:Save"].ID, f.jobB.ID); err != nil {
		t.Fatal(err)
	}
	// A round still open is no baseline.
	if _, err := f.pool.Exec(testutil.Ctx(), `INSERT INTO translation_step_statuses
		(tenant_id, translation_id, job_step_assignment_id, round)
		SELECT $1, $2, jsa.id, 1 FROM job_step_assignments jsa WHERE jsa.job_id = $3`,
		f.tenant, f.tr["A:Save"].ID, f.jobA.ID); err != nil {
		t.Fatal(err)
	}
	idx := scopeGet[editor.Index](t, f, f.ownerID, scopePath(f.project.ID, "index", f.jobA, f.jobB))
	if idx.Files[0].HasPreviousStep {
		t.Errorf("file A has_previous_step = true, want false: its only round is open")
	}
	if !idx.Files[1].HasPreviousStep {
		t.Errorf("file B has_previous_step = false, want true")
	}
}
