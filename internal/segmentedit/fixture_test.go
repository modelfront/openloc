package segmentedit_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/editor"
	"openloc/internal/file"
	"openloc/internal/segmentedit"
	"openloc/internal/testutil"
)

// The original file: two lines, each one segment (no sentence break inside).
const original = "Press the button and wait for the light\nNext one\n"

var sources = []string{"Press the button and wait for the light", "Next one"}

var targets = map[string][]string{
	"de": {"Drücken Sie die Taste und warten Sie auf das Licht", "Der nächste"},
	"fr": {"Appuyez sur le bouton et attendez la lumière", "Le suivant"},
}

type fixture struct {
	pool             *pgxpool.Pool
	q                db.Querier
	router           chi.Router
	store            *file.MemoryStore
	userID, tenantID int64
	project          db.Project
	file             db.File
	jobs             map[string]db.Job
	segs             []db.Segment
	trs              map[string][]db.Translation
}

// fileSegment is one segment as an upload stores it.
type fileSegment struct{ key, source string }

// newFixture is a TXT file of two lines in a project translated into German
// and French; both jobs are approved.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	segs := make([]fileSegment, len(sources))
	for i, src := range sources {
		segs[i] = fileSegment{fmt.Sprintf("line:%d", i+1), src}
	}
	return newFileFixture(t, original, segs, targets)
}

// newFileFixture is a TXT file holding content, stored as segs, in a project
// translated into the languages of targets, with each segment's target
// approved; the original sits in file storage, where a join across sentences
// reads it.
func newFileFixture(t *testing.T, content string, segs []fileSegment, targets map[string][]string) *fixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	store := file.NewMemoryStore()
	f := &fixture{pool: pool, q: q, store: store, router: testutil.NewTestRouterWithStorage(pool, nil, store), jobs: map[string]db.Job{}, trs: map[string][]db.Translation{}}
	f.userID, f.tenantID = testutil.RegisterUser(t, q, "segedit")
	langs := make([]string, 0, len(targets))
	for _, lang := range []string{"de", "fr"} {
		if _, ok := targets[lang]; ok {
			langs = append(langs, lang)
		}
	}
	f.project = testutil.SeedProject(t, q, f.tenantID, "Split and join", "en", langs)
	f.file = testutil.SeedFile(t, q, f.project.ID, "guide.txt", db.FileFormatTxt)
	if err := store.Put(context.Background(), f.file.StoragePath, strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	for i, fs := range segs {
		seg := testutil.SeedSegment(t, q, f.file.ID, fs.source, int32(i))
		f.exec(t, `UPDATE segments SET segment_key = $2, word_count = $3 WHERE id = $1`, seg.ID, fs.key, len(strings.Fields(fs.source)))
		f.segs = append(f.segs, f.segment(t, seg.ID))
	}
	for _, lang := range langs {
		f.jobs[lang] = testutil.SeedJob(t, q, f.file.ID, lang)
		for i, seg := range f.segs {
			tr := testutil.SeedTranslation(t, q, seg.ID, lang, db.TranslationStatusNeedsReview)
			f.exec(t, `UPDATE translations SET active_text = $2, status = 'approved' WHERE id = $1`, tr.ID, targets[lang][i])
			f.trs[lang] = append(f.trs[lang], f.translation(t, tr.ID))
		}
	}
	return f
}

func (f *fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *fixture) segment(t *testing.T, id int64) db.Segment {
	t.Helper()
	s, err := f.q.GetSegment(context.Background(), id)
	if err != nil {
		t.Fatalf("GetSegment %d: %v", id, err)
	}
	return s
}

func (f *fixture) translation(t *testing.T, id int64) db.Translation {
	t.Helper()
	tr, err := f.q.GetTranslation(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTranslation %d: %v", id, err)
	}
	return tr
}

func (f *fixture) post(t *testing.T, lang, action string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequest(t, f.router, http.MethodPost, fmt.Sprintf("/api/jobs/%d/segments/%s", f.jobs[lang].ID, action), body, f.userID, f.tenantID)
}

// split splits translation id at offset with its current version and
// returns the result; it fails the test unless the split succeeds.
func (f *fixture) split(t *testing.T, lang string, id int64, offset int) segmentedit.Result {
	t.Helper()
	rec := f.post(t, lang, "split", splitBody(f.translation(t, id), offset))
	if rec.Code != http.StatusOK {
		t.Fatalf("split %d at %d: %d %s", id, offset, rec.Code, rec.Body.String())
	}
	return testutil.DecodeData[segmentedit.Result](t, rec)
}

func (f *fixture) join(t *testing.T, lang string, ids ...int64) *httptest.ResponseRecorder {
	t.Helper()
	rows := make([]map[string]any, len(ids))
	for i, id := range ids {
		rows[i] = map[string]any{"translation_id": id, "expected_updated_at": f.translation(t, id).UpdatedAt.Time}
	}
	return f.post(t, lang, "join", map[string]any{"rows": rows})
}

func splitBody(tr db.Translation, offset int) map[string]any {
	return map[string]any{"translation_id": tr.ID, "offset": offset, "expected_updated_at": tr.UpdatedAt.Time}
}

func (f *fixture) index(t *testing.T, lang string) editor.Index {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, http.MethodGet, fmt.Sprintf("/api/jobs/%d/editor/index", f.jobs[lang].ID), nil, f.userID, f.tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("index %s: %d %s", lang, rec.Code, rec.Body.String())
	}
	return testutil.DecodeData[editor.Index](t, rec)
}

// counts is the job's stored row and word totals, and the same counted from
// its rows; a split or join must keep them equal.
func (f *fixture) counts(t *testing.T, lang string) (stored, counted [2]int64) {
	t.Helper()
	ctx := context.Background()
	if err := f.pool.QueryRow(ctx, `SELECT COALESCE(sum(segments), 0), COALESCE(sum(words), 0)
		FROM job_translation_stats WHERE file_id = $1 AND target_language = $2`, f.file.ID, lang).Scan(&stored[0], &stored[1]); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*), COALESCE(sum(s.word_count), 0)
		FROM segments s JOIN translations t ON t.segment_id = s.id AND t.target_language = $2
		WHERE s.file_id = $1`, f.file.ID, lang).Scan(&counted[0], &counted[1]); err != nil {
		t.Fatal(err)
	}
	return stored, counted
}

type storage struct{ data []byte }

func (s storage) Download(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}
