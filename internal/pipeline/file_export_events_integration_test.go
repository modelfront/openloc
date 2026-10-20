package pipeline_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// getFileExport calls GET /files/{fileID}/export — the link the file page and
// openloc_export_file hand out.
func getFileExport(t *testing.T, q db.Querier, pool *pgxpool.Pool, storage pipeline.FileStorage, fileID, tenantID, userID int64, query string) *httptest.ResponseRecorder {
	t.Helper()
	h := pipeline.NewHandler(q, pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), storage))
	r := chi.NewRouter()
	r.Get("/files/{fileID}/export", h.HandleExportFile)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/files/%d/export?%s", fileID, query), nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: tenantID, UserID: userID, Role: "owner"}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Exporting a file through its export link lands in the project history as
// job.exported, exactly like the job export and the batch download: the
// format, the file, the job and the person who downloaded it.
func TestIntegration_FileExport_EmitsHistoryEvent(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupDownloadFixture(t, q, "fileexport", "guide.txt")
	storage := stubStorage{byPath: fx.Contents}
	file, job := fx.Files[0], fx.Jobs[0]

	for _, format := range []string{"source", "xliff"} {
		if w := getFileExport(t, q, pool, storage, file.ID, fx.TenantID, fx.UserID, "lang=es&format="+format); w.Code != http.StatusOK {
			t.Fatalf("%s export: %d %s", format, w.Code, w.Body.String())
		}
	}

	events := testutil.ProjectEvents(t, q, fx.ProjectID, fx.TenantID, "job.exported")
	if len(events) != 2 {
		t.Fatalf("job.exported events = %d, want one per export (2)", len(events))
	}
	formats := map[string]bool{}
	for _, ev := range events {
		formats[fmt.Sprint(ev.Data["format"])] = true
		if ev.Data["file_name"] != "guide.txt" || ev.Data["target_language"] != "es" {
			t.Errorf("details = %v, want file_name guide.txt and target_language es", ev.Data)
		}
		if ev.JobID == nil || *ev.JobID != job.ID || ev.FileID == nil || *ev.FileID != file.ID {
			t.Errorf("event job/file = %v/%v, want %d/%d", ev.JobID, ev.FileID, job.ID, file.ID)
		}
		if ev.ActorID == nil || *ev.ActorID != fx.UserID {
			t.Errorf("event actor = %v, want the downloading user %d", ev.ActorID, fx.UserID)
		}
	}
	if !formats["source"] || !formats["xliff"] {
		t.Errorf("formats = %v, want source and xliff", formats)
	}
}

// Only an export that was served is history: another tenant's request is a 404,
// a failed export of either format is a 500, and none of them leaves an event.
func TestIntegration_FileExport_NoEventWithoutDelivery(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupDownloadFixture(t, q, "noexport", "guide.txt")
	other := setupDownloadFixture(t, q, "othertenant", "theirs.txt")
	file := fx.Files[0]

	if w := getFileExport(t, q, pool, stubStorage{byPath: fx.Contents}, file.ID, other.TenantID, other.UserID, "lang=es"); w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant export: %d, want 404", w.Code)
	}
	if w := getFileExport(t, q, pool, stubStorage{err: errors.New("storage down")}, file.ID, fx.TenantID, fx.UserID, "lang=es"); w.Code != http.StatusInternalServerError {
		t.Errorf("failed export: %d, want 500", w.Code)
	}
	// The bilingual XLIFF refuses a segment with no key: it could not be
	// matched back on re-import.
	if _, err := q.CreateSegment(context.Background(), db.CreateSegmentParams{FileID: file.ID, SourceText: "No key", Position: 2}); err != nil {
		t.Fatalf("seed a segment with no key: %v", err)
	}
	if w := getFileExport(t, q, pool, stubStorage{byPath: fx.Contents}, file.ID, fx.TenantID, fx.UserID, "lang=es&format=xliff"); w.Code != http.StatusInternalServerError {
		t.Errorf("failed XLIFF export: %d, want 500", w.Code)
	}
	if events := testutil.ProjectEvents(t, q, fx.ProjectID, fx.TenantID, "job.exported"); len(events) != 0 {
		t.Errorf("job.exported events = %d after no delivered export, want 0", len(events))
	}
	if events := testutil.ProjectEvents(t, q, other.ProjectID, other.TenantID, "job.exported"); len(events) != 0 {
		t.Errorf("the other tenant got %d job.exported events, want 0", len(events))
	}
}

// A file exported in a target language it has no job for is still history:
// the event names the file and the language, and carries no job.
func TestIntegration_FileExport_WithoutAJobStillRecorded(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupDownloadFixture(t, q, "nojob", "guide.txt")
	file := fx.Files[0]
	if _, err := pool.Exec(context.Background(), `UPDATE projects SET target_languages = $1 WHERE id = $2`, []string{"es", "fr"}, fx.ProjectID); err != nil {
		t.Fatalf("add fr to the project's targets: %v", err)
	}
	w := getFileExport(t, q, pool, stubStorage{byPath: fx.Contents}, file.ID, fx.TenantID, fx.UserID, "lang=fr&format=xliff")
	if w.Code != http.StatusOK {
		t.Fatalf("export without a job: %d %s", w.Code, w.Body.String())
	}
	events := testutil.ProjectEvents(t, q, fx.ProjectID, fx.TenantID, "job.exported")
	if len(events) != 1 {
		t.Fatalf("job.exported events = %d, want 1", len(events))
	}
	if events[0].JobID != nil || events[0].FileID == nil || *events[0].FileID != file.ID || events[0].Data["target_language"] != "fr" {
		t.Errorf("event job %v file %v details %v, want no job, file %d, target_language fr", events[0].JobID, events[0].FileID, events[0].Data, file.ID)
	}
}

// The export link serves only the project's target languages. Any other
// language is refused before anything is exported, so its text never reaches
// the History.
func TestIntegration_FileExport_RefusesALanguageTheProjectDoesNotTarget(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupDownloadFixture(t, q, "notarget", "guide.txt")
	file := fx.Files[0]
	for _, query := range []string{
		"lang=fr",
		"lang=fr&format=xliff",
		"lang=" + strings.Repeat("x", 500),
	} {
		if w := getFileExport(t, q, pool, stubStorage{byPath: fx.Contents}, file.ID, fx.TenantID, fx.UserID, query); w.Code != http.StatusBadRequest {
			t.Errorf("export %.40s: %d, want 400", query, w.Code)
		}
	}
	if events := testutil.ProjectEvents(t, q, fx.ProjectID, fx.TenantID, "job.exported"); len(events) != 0 {
		t.Errorf("job.exported events = %d after refused exports, want 0", len(events))
	}
}
