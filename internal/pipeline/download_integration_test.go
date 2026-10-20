package pipeline_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parser/pptxtest"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// =============================================================================
// Batch download (POST /api/projects/{projectID}/download)
// =============================================================================
//
// The UI only ever calls this endpoint for multi-item selections (one item goes
// through the per-job / per-file GETs so linguists can reach it), so the tests
// below focus on what only this handler does: packing several artifacts into a
// ZIP, refusing ids that don't belong to the named project, and emitting one
// audit row per delivered artifact.
//
// =============================================================================
// Anti-tautology RED proofs (durable artifact)
// =============================================================================
// Proof 1 — a job from another project must not be downloadable through a
//           project the caller CAN see.
//   Mutation:   handler_download.go::fileInProject — drop the
//               `file.ProjectID != project.ID` half of the condition.
//   Test:       TestIntegration_ProjectDownload_RejectsForeignProjectJob
//   Diagnostic: the handler happily exports the other project's file and
//               returns 200 with its content; the test asserts 404.
//   Proven:     2026-08-28.
//
// Proof 2 — same-named files must not collide inside the archive.
//   Mutation:   handler_download.go::zipEntries — drop the uniqueEntryName
//               call and use e.name directly.
//   Test:       TestIntegration_ProjectDownload_DeduplicatesEntryNames
//   Diagnostic: the ZIP carries two entries called "report.txt"; extraction
//               keeps one, silently losing a deliverable. The test asserts on
//               the distinct names.
//   Proven:     2026-08-28.

// stubStorage serves canned bytes for any storage path. ExportOriginal and
// ExportFile both read through FileStorage, so the download tests need one;
// the rest of the pipeline integration suite passes nil.
type stubStorage struct {
	// byPath is consulted first; missing paths fall back to `data`.
	byPath map[string][]byte
	data   []byte
	err    error
}

func (s stubStorage) Download(_ context.Context, path string) (io.ReadCloser, error) {
	if s.err != nil {
		return nil, s.err
	}
	if b, ok := s.byPath[path]; ok {
		return io.NopCloser(bytes.NewReader(b)), nil
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

// downloadFixture is a tenant + project with two TXT files, each with one
// segment and an approved translation, plus a job per file.
type downloadFixture struct {
	TenantID  int64
	UserID    int64
	ProjectID int64
	Files     []db.File
	Jobs      []db.Job
	// Contents keyed by storage path, wired into stubStorage.
	Contents map[string][]byte
}

func setupDownloadFixture(t *testing.T, q db.Querier, prefix string, fileNames ...string) downloadFixture {
	t.Helper()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, prefix+" Corp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, prefix+"@test.com", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, prefix+" Project", "en", []string{"es"})

	fx := downloadFixture{
		TenantID:  tenant.ID,
		UserID:    user.ID,
		ProjectID: project.ID,
		Contents:  map[string][]byte{},
	}

	for i, name := range fileNames {
		file := testutil.SeedFile(t, q, project.ID, name, db.FileFormatTxt)
		// The TXT parser reconstructs line-by-line, so the stored original has
		// to line up with the segment text for the source-format export.
		source := fmt.Sprintf("Source line %d", i+1)
		fx.Contents[file.StoragePath] = []byte(source + "\n")

		key := "line:1"
		seg, err := q.CreateSegment(ctx, db.CreateSegmentParams{
			FileID: file.ID, SourceText: source, SegmentKey: &key, Position: 1,
		})
		if err != nil {
			t.Fatalf("CreateSegment(%s): %v", name, err)
		}
		tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusApproved)
		target := fmt.Sprintf("Línea traducida %d", i+1)
		if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
			ID: tr.ID, HumanText: &target,
		}); err != nil {
			t.Fatalf("UpdateTranslationHuman(%s): %v", name, err)
		}

		fx.Files = append(fx.Files, file)
		fx.Jobs = append(fx.Jobs, testutil.SeedJob(t, q, file.ID, "es"))
	}
	return fx
}

func (fx downloadFixture) jobIDs() []int64 {
	ids := make([]int64, len(fx.Jobs))
	for i, j := range fx.Jobs {
		ids[i] = j.ID
	}
	return ids
}

func (fx downloadFixture) fileIDs() []int64 {
	ids := make([]int64, len(fx.Files))
	for i, f := range fx.Files {
		ids[i] = f.ID
	}
	return ids
}

// postDownload drives the handler through a chi router so {projectID} resolves
// exactly as it does in production.
func postDownload(t *testing.T, q db.Querier, pool *pgxpool.Pool, storage pipeline.FileStorage, fx downloadFixture, tenantID int64, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), storage)
	h := pipeline.NewHandler(q, svc)

	r := chi.NewRouter()
	r.Post("/projects/{projectID}/download", h.HandleProjectDownload)

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/projects/%d/download", fx.ProjectID), bytes.NewReader(payload))
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenantID, UserID: fx.UserID, Role: "owner",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// zipNames lists the entry names in a ZIP response body.
func zipNames(t *testing.T, body []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("open zip (%d bytes): %v", len(body), err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

func zipEntry(t *testing.T, body []byte, name string) []byte {
	t.Helper()
	return []byte(pptxtest.ReadPart(t, body, name))
}

// TestIntegration_ProjectDownload_MultipleJobsZipped is the endpoint's reason
// to exist: two selected jobs come back as ONE archive, each entry named the
// way the single-job export names its file ("<lang>_<name>").
func TestIntegration_ProjectDownload_MultipleJobsZipped(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupDownloadFixture(t, q, "zip", "alpha.txt", "beta.txt")
	storage := stubStorage{byPath: fx.Contents}

	w := postDownload(t, q, pool, storage, fx, fx.TenantID, map[string]any{
		"format": "source", "job_ids": fx.jobIDs(),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("expected application/zip, got %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, ".zip") {
		t.Errorf("expected a .zip filename, got %q", cd)
	}

	names := zipNames(t, w.Body.Bytes())
	if len(names) != 2 {
		t.Fatalf("expected 2 zip entries, got %d: %v", len(names), names)
	}
	for _, want := range []string{"es_alpha.txt", "es_beta.txt"} {
		found := false
		for _, got := range names {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing entry %q in %v", want, names)
		}
	}
	// The entry must carry the TRANSLATED text, not the stored original —
	// otherwise the zip path silently ships source files as deliverables.
	if body := string(zipEntry(t, w.Body.Bytes(), "es_alpha.txt")); !strings.Contains(body, "Línea traducida 1") {
		t.Errorf("expected translated text in the deliverable, got %q", body)
	}
}

// TestIntegration_ProjectDownload_OriginalReturnsUploadedBytes covers the new
// source-file option: the upload comes back byte-for-byte, under its own name.
func TestIntegration_ProjectDownload_OriginalReturnsUploadedBytes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupDownloadFixture(t, q, "orig", "one.txt", "two.txt")
	storage := stubStorage{byPath: fx.Contents}

	w := postDownload(t, q, pool, storage, fx, fx.TenantID, map[string]any{
		"format": "original", "file_ids": fx.fileIDs(),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	got := zipEntry(t, w.Body.Bytes(), "one.txt")
	want := fx.Contents[fx.Files[0].StoragePath]
	if !bytes.Equal(got, want) {
		t.Errorf("source download must be byte-identical to the upload:\n got %q\nwant %q", got, want)
	}
}

// TestIntegration_ProjectDownload_SingleItemNotZipped — a lone item comes back
// as the file itself. The UI routes single downloads elsewhere, but a direct
// API caller shouldn't be handed a one-entry archive.
func TestIntegration_ProjectDownload_SingleItemNotZipped(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupDownloadFixture(t, q, "single", "solo.txt")
	storage := stubStorage{byPath: fx.Contents}

	w := postDownload(t, q, pool, storage, fx, fx.TenantID, map[string]any{
		"format": "original", "file_ids": fx.fileIDs(),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct == "application/zip" {
		t.Error("a single item must not be wrapped in a zip")
	}
	if !bytes.Equal(w.Body.Bytes(), fx.Contents[fx.Files[0].StoragePath]) {
		t.Errorf("expected the raw upload, got %q", w.Body.String())
	}
}

// TestIntegration_ProjectDownload_CrossTenantBlocked — the project id resolves
// only inside the caller's tenant.
func TestIntegration_ProjectDownload_CrossTenantBlocked(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupDownloadFixture(t, q, "xtenant", "secret.txt")
	other := testutil.SeedTenant(t, q, "Intruder Corp")

	w := postDownload(t, q, pool, stubStorage{byPath: fx.Contents}, fx, other.ID, map[string]any{
		"format": "original", "file_ids": fx.fileIDs(),
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant download, got %d: %s", w.Code, w.Body.String())
	}
}

// TestIntegration_ProjectDownload_RejectsForeignProjectJob — a job id from a
// DIFFERENT project in the SAME tenant must not ride along on a project the
// caller legitimately owns. See RED proof 1.
func TestIntegration_ProjectDownload_RejectsForeignProjectJob(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupDownloadFixture(t, q, "own", "mine.txt")

	// A second project inside the same tenant.
	otherProject := testutil.SeedProject(t, q, fx.TenantID, "Other Project", "en", []string{"es"})
	otherFile := testutil.SeedFile(t, q, otherProject.ID, "theirs.txt", db.FileFormatTxt)
	otherJob := testutil.SeedJob(t, q, otherFile.ID, "es")

	w := postDownload(t, q, pool, stubStorage{data: []byte("x")}, fx, fx.TenantID, map[string]any{
		"format": "source", "job_ids": []int64{fx.Jobs[0].ID, otherJob.ID},
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when a job belongs to another project, got %d: %s", w.Code, w.Body.String())
	}
}

// TestIntegration_ProjectDownload_DeduplicatesEntryNames — two files can share
// a name inside one project; the archive must keep both. See RED proof 2.
func TestIntegration_ProjectDownload_DeduplicatesEntryNames(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupDownloadFixture(t, q, "dupe", "report.txt", "report.txt")

	w := postDownload(t, q, pool, stubStorage{byPath: fx.Contents}, fx, fx.TenantID, map[string]any{
		"format": "original", "file_ids": fx.fileIDs(),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	names := zipNames(t, w.Body.Bytes())
	if len(names) != 2 {
		t.Fatalf("expected 2 entries, got %v", names)
	}
	if names[0] == names[1] {
		t.Errorf("duplicate entry names would drop a file on extraction: %v", names)
	}
	if names[1] != "report (2).txt" {
		t.Errorf("expected the second entry to be disambiguated, got %q", names[1])
	}
}

// TestIntegration_ProjectDownload_Validation covers the input guards in one
// table: bad format, missing ids, and the batch cap.
func TestIntegration_ProjectDownload_Validation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupDownloadFixture(t, q, "validate", "a.txt")

	tooMany := make([]int64, 101)
	for i := range tooMany {
		tooMany[i] = fx.Jobs[0].ID
	}

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"unknown format", map[string]any{"format": "pdf", "job_ids": fx.jobIDs()}, http.StatusBadRequest},
		{"no job ids", map[string]any{"format": "source", "job_ids": []int64{}}, http.StatusBadRequest},
		{"no file ids", map[string]any{"format": "original", "file_ids": []int64{}}, http.StatusBadRequest},
		{"over the cap", map[string]any{"format": "source", "job_ids": tooMany}, http.StatusBadRequest},
		{"unknown job", map[string]any{"format": "source", "job_ids": []int64{999999}}, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := postDownload(t, q, pool, stubStorage{data: []byte("x")}, fx, fx.TenantID, c.body)
			if w.Code != c.want {
				t.Errorf("expected %d, got %d: %s", c.want, w.Code, w.Body.String())
			}
		})
	}
}

// TestIntegration_ProjectDownload_EmitsAuditEvents — every delivered artifact
// lands in the project history, with the format that was actually served.
func TestIntegration_ProjectDownload_EmitsAuditEvents(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	fx := setupDownloadFixture(t, q, "audit", "a.txt", "b.txt")
	storage := stubStorage{byPath: fx.Contents}

	if w := postDownload(t, q, pool, storage, fx, fx.TenantID, map[string]any{
		"format": "xliff", "job_ids": fx.jobIDs(),
	}); w.Code != http.StatusOK {
		t.Fatalf("bilingual download failed: %d %s", w.Code, w.Body.String())
	}
	if w := postDownload(t, q, pool, storage, fx, fx.TenantID, map[string]any{
		"format": "original", "file_ids": fx.fileIDs(),
	}); w.Code != http.StatusOK {
		t.Fatalf("original download failed: %d %s", w.Code, w.Body.String())
	}

	events, err := q.ListProjectEvents(ctx, db.ListProjectEventsParams{
		ProjectID: fx.ProjectID, TenantID: fx.TenantID, Limit: 100,
	})
	if err != nil {
		t.Fatalf("ListProjectEvents: %v", err)
	}
	exported, downloaded := 0, 0
	for _, ev := range events {
		switch ev.EventType {
		case "job.exported":
			exported++
			// Parse rather than substring-match: pg renders jsonb with spaces
			// after the colon, so a literal check passes/fails for the wrong reason.
			var details map[string]any
			if err := json.Unmarshal(ev.Details, &details); err != nil {
				t.Fatalf("unmarshal event details %s: %v", ev.Details, err)
			}
			if details["format"] != "xliff" {
				t.Errorf("job.exported must record the served format, got %v", details["format"])
			}
		case "file.downloaded":
			downloaded++
		}
	}
	if exported != 2 {
		t.Errorf("expected 2 job.exported events (one per job), got %d", exported)
	}
	if downloaded != 2 {
		t.Errorf("expected 2 file.downloaded events (one per file), got %d", downloaded)
	}
}
