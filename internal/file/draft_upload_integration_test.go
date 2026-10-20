package file_test

// Uploads into a draft project stop at the file + segments; publish finishes
// them. Anti-tautology: the first test fails if the draft
// branch in HandleUpload is removed (jobs appear on upload), the second if
// MaterializeProjectFiles stops creating any of translations / jobs / step
// assignments / pipeline enqueues.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

type draftUploadFixture struct {
	pool     *pgxpool.Pool
	q        db.Querier
	router   http.Handler
	enqueuer *recordingEnqueuer
	store    *file.MemoryStore
	tenant   db.Tenant
	pm       db.User
	draftID  int64
}

// newDraftUploadFixture creates a complete draft (en → fr, de; MT on upload)
// through the API, with in-memory storage so publish can read the source back.
func newDraftUploadFixture(t *testing.T, name string) draftUploadFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	enq := &recordingEnqueuer{}
	store := file.NewMemoryStore()
	router := testutil.NewTestRouterWithStorage(pool, enq, store)
	tenant := testutil.SeedTenant(t, q, name)
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@"+name+".test", "pass", "project_manager")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/projects", map[string]any{
		"status": "draft", "name": "Draft with files", "source_language": "en", "target_languages": []string{"fr", "de"},
		"settings": map[string]any{"mt_on_upload": true, "tm_on_upload": false},
	}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return draftUploadFixture{pool: pool, q: q, router: router, enqueuer: enq, store: store, tenant: tenant, pm: pm, draftID: resp.Data.ID}
}

func (f draftUploadFixture) upload(t *testing.T, filename, content string) int64 {
	t.Helper()
	rec := testutil.DoFileUploadWithRole(t, f.router, fmt.Sprintf("/api/projects/%d/files", f.draftID),
		filename, content, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return resp.Data.ID
}

func (f draftUploadFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func TestIntegrationDraftUploadStoresFileWithoutJobs(t *testing.T) {
	f := newDraftUploadFixture(t, "DraftUpload")
	fileID := f.upload(t, "strings.json", `{"a": "Hello world", "b": "Second line"}`)

	if n := f.count(t, `SELECT count(*) FROM segments WHERE file_id = $1`, fileID); n != 2 {
		t.Errorf("segments = %d, want 2", n)
	}
	if n := f.count(t, `SELECT count(*) FROM jobs WHERE file_id = $1`, fileID); n != 0 {
		t.Errorf("jobs on a draft upload = %d, want 0", n)
	}
	if n := f.count(t, `SELECT count(*) FROM translations tr JOIN segments s ON s.id = tr.segment_id WHERE s.file_id = $1`, fileID); n != 0 {
		t.Errorf("translations on a draft upload = %d, want 0", n)
	}
	if calls := f.enqueuer.snapshot(); len(calls) != 0 {
		t.Errorf("pre-translation enqueued for a draft: %d calls", len(calls))
	}
	// The upload itself is still recorded, marked as a draft upload.
	rec := testutil.DoRequestWithRole(t, f.router, "GET", fmt.Sprintf("/api/projects/%d/events", f.draftID), nil, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var page struct {
		Data []struct {
			EventType string         `json:"event_type"`
			Details   map[string]any `json:"details"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &page)
	// Newest first: the upload, after the project.created written at draft start.
	if len(page.Data) != 2 || page.Data[0].EventType != "file.uploaded" || page.Data[0].Details["draft"] != true ||
		page.Data[1].EventType != "project.created" {
		t.Errorf("draft upload history = %+v, want file.uploaded with draft=true, then project.created", page.Data)
	}
}

func TestIntegrationDraftPublishMaterializesFiles(t *testing.T) {
	f := newDraftUploadFixture(t, "DraftPublishFiles")
	first := f.upload(t, "one.json", `{"a": "Hello world", "b": "Second line"}`)
	second := f.upload(t, "two.json", `{"x": "Another file"}`)

	rec := testutil.DoRequestWithRole(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", f.draftID),
		map[string]any{"analyze": false}, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)

	for _, fileID := range []int64{first, second} {
		jobs, err := f.q.ListJobsByFile(context.Background(), fileID)
		if err != nil || len(jobs) != 2 {
			t.Fatalf("file %d: jobs = %v (%v), want one per target language", fileID, jobs, err)
		}
		langs := map[string]bool{}
		for _, j := range jobs {
			langs[j.TargetLanguage] = true
			steps, err := f.q.ListJobStepAssignments(context.Background(), j.ID)
			if err != nil || len(steps) == 0 {
				t.Errorf("job %d: step assignments = %v (%v), want the default workflow", j.ID, steps, err)
			}
			if j.WordCount == 0 {
				t.Errorf("job %d: word_count = 0, want the file's count", j.ID)
			}
		}
		if !langs["fr"] || !langs["de"] {
			t.Errorf("file %d: job languages = %v, want fr + de", fileID, langs)
		}
		segs := f.count(t, `SELECT count(*) FROM segments WHERE file_id = $1`, fileID)
		if n := f.count(t, `SELECT count(*) FROM translations tr JOIN segments s ON s.id = tr.segment_id WHERE s.file_id = $1`, fileID); n != segs*2 {
			t.Errorf("file %d: translations = %d, want segments × languages = %d", fileID, n, segs*2)
		}
	}
	// MT-on-upload: one pre-translation per job, attributed to the publisher.
	calls := f.enqueuer.snapshot()
	if len(calls) != 4 {
		t.Fatalf("pipeline enqueues after publish = %d, want 4 (2 files × 2 languages)", len(calls))
	}
	for _, c := range calls {
		if !c.Phases.UseMT || c.Phases.UseTM || c.TriggeredBy == nil || *c.TriggeredBy != f.pm.ID {
			t.Errorf("enqueue = %+v, want MT-only triggered by the publisher", c)
		}
	}

	// The upload path after publish is the normal one: jobs right away.
	third := f.upload(t, "three.json", `{"y": "Live upload"}`)
	if n := f.count(t, `SELECT count(*) FROM jobs WHERE file_id = $1`, third); n != 2 {
		t.Errorf("jobs on a post-publish upload = %d, want 2", n)
	}
}

// Files are deletable by their drafting manager while the project is a
// draft; once live, deleting stays owner/admin. RED without the draft check
// in HandleDelete (PM gets 403 on the draft) or with the route left open (PM
// gets 204 on the live project).
func TestIntegrationDraftFileDeleteIsManagerOnlyWhileDrafting(t *testing.T) {
	f := newDraftUploadFixture(t, "DraftFileDelete")
	admin := testutil.SeedUserWithRole(t, f.q, f.tenant.ID, "admin@draftfiledelete.test", "pass", "admin")
	fileID := f.upload(t, "one.json", `{"a": "Hello"}`)

	rec := testutil.DoRequestWithRole(t, f.router, "DELETE", fmt.Sprintf("/api/projects/%d/files/%d", f.draftID, fileID),
		nil, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	if n := f.count(t, `SELECT count(*) FROM files WHERE id = $1`, fileID); n != 0 {
		t.Errorf("file still present after the drafting PM deleted it")
	}

	kept := f.upload(t, "two.json", `{"b": "World"}`)
	rec = testutil.DoRequestWithRole(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", f.draftID),
		nil, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequestWithRole(t, f.router, "DELETE", fmt.Sprintf("/api/projects/%d/files/%d", f.draftID, kept),
		nil, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	if n := f.count(t, `SELECT count(*) FROM files WHERE id = $1`, kept); n != 1 {
		t.Errorf("live project's file deleted by a PM")
	}
	rec = testutil.DoRequestWithRole(t, f.router, "DELETE", fmt.Sprintf("/api/projects/%d/files/%d", f.draftID, kept),
		nil, admin.ID, f.tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
}

// The draft carve-out widens the role, never the tenant: the route has no
// RequireRole middleware, so the tenant check lives in HandleDelete. RED if it
// drops the GetFileTenant lookup or the 403 it gates the manager path on.
func TestIntegrationDraftFileDeleteIsTenantScoped(t *testing.T) {
	f := newDraftUploadFixture(t, "DraftFileTenant")
	fileID := f.upload(t, "mine.json", `{"a": "Hello"}`)

	other := testutil.SeedTenant(t, f.q, "DraftFileTenantB")
	intruderPM := testutil.SeedUserWithRole(t, f.q, other.ID, "pm@draftfiletenantb.test", "pass", "project_manager")
	intruderAdmin := testutil.SeedUserWithRole(t, f.q, other.ID, "admin@draftfiletenantb.test", "pass", "admin")
	path := fmt.Sprintf("/api/projects/%d/files/%d", f.draftID, fileID)

	// A manager of another tenant cannot see the file, so the draft branch
	// never opens for them.
	rec := testutil.DoRequestWithRole(t, f.router, "DELETE", path, nil, intruderPM.ID, other.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// An admin of another tenant reaches the delete, but it is tenant-scoped,
	// so it matches nothing — 204 is this route's historical no-op-delete
	// answer, and the row has to survive it.
	rec = testutil.DoRequestWithRole(t, f.router, "DELETE", path, nil, intruderAdmin.ID, other.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	if n := f.count(t, `SELECT count(*) FROM files WHERE id = $1`, fileID); n != 1 {
		t.Fatalf("file rows after two cross-tenant deletes = %d, want 1", n)
	}
	// The owning tenant's manager still can.
	rec = testutil.DoRequestWithRole(t, f.router, "DELETE", path, nil, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	if n := f.count(t, `SELECT count(*) FROM files WHERE id = $1`, fileID); n != 0 {
		t.Errorf("file survived its own tenant's delete")
	}
}

// Publishing a draft whose stored segments differ from a parse of its file —
// an extra segment for an empty value, or the same segments with a source text
// in another form, as an older parser stored them — replaces them with the
// parsed segments. Each case changes one thing, so each check decides alone.
func TestIntegrationDraftPublishReparsesWhenStoredSegmentsDiffer(t *testing.T) {
	cases := []struct {
		name  string
		stale string // makes the stored segments differ from a parse
	}{
		{"extra segment", `INSERT INTO segments (file_id, source_text, segment_key, position) VALUES ($1, '', 'empty', 2)`},
		{"same count, other text", `UPDATE segments SET source_text = 'Hello &amp; world' WHERE file_id = $1 AND segment_key = 'a'`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newDraftUploadFixture(t, "DraftPublishReparse")
			fileID := f.upload(t, "old.json", `{"a": "Hello world", "empty": "", "b": "Second line"}`)
			ctx := context.Background()
			if _, err := f.pool.Exec(ctx, c.stale, fileID); err != nil {
				t.Fatal(err)
			}

			rec := testutil.DoRequestWithRole(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", f.draftID),
				map[string]any{"analyze": false}, f.pm.ID, f.tenant.ID, "project_manager")
			testutil.AssertStatus(t, rec, http.StatusOK)

			segs, err := f.q.ListSegmentsByFile(ctx, fileID)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, s := range segs {
				got = append(got, fmt.Sprintf("%s=%s", ptr.Deref(s.SegmentKey), s.SourceText))
			}
			if fmt.Sprint(got) != "[a=Hello world b=Second line]" {
				t.Errorf("segments after publish = %v, want [a=Hello world b=Second line]", got)
			}
			if n := f.count(t, `SELECT segment_count FROM files WHERE id = $1`, fileID); n != 2 {
				t.Errorf("files.segment_count = %d, want 2", n)
			}
			if n := f.count(t, `SELECT count(*) FROM translations tr JOIN segments s ON s.id = tr.segment_id WHERE s.file_id = $1`, fileID); n != 4 {
				t.Errorf("translations = %d, want 2 segments × 2 languages", n)
			}
		})
	}
}
