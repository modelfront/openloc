package file_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/testutil"
)

// Publish segments a draft's file before it locks the file row, then creates
// its jobs under the lock.

// publishFile runs publish's step for one file row, as the publishing
// manager's request does.
func (f draftUploadFixture) publishFile(t *testing.T, row db.File) error {
	t.Helper()
	ctx := context.Background()
	project, err := f.q.GetProject(ctx, db.GetProjectParams{ID: f.draftID, TenantID: f.tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := f.q.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	h := file.NewHandler(f.q, f.pool, f.store, f.enqueuer)
	pubCtx := auth.WithClaims(ctx, &auth.Claims{TenantID: f.tenant.ID, UserID: f.pm.ID, Role: "project_manager"})
	return file.MaterializeStoredFile(h, pubCtx, project, profile, row, false)
}

func (f draftUploadFixture) fileRow(t *testing.T, fileID int64) db.File {
	t.Helper()
	row, err := f.q.GetFileTenant(context.Background(), db.GetFileTenantParams{ID: fileID, TenantID: f.tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// Publish parses a file before it locks the row. When the layout changed in
// between, the parse is stale: the file is refused, not given jobs. A publish
// that finds the file already published (a second click, the assistant and
// the page at once) skips it.
func TestIntegrationXLSXPublishLocksTheFileRow(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXLayoutStale")
	fileID := f.uploadLayout(t, testLayoutJSON)
	stale := f.fileRow(t, fileID)
	testutil.AssertStatus(t, f.setLayout(t, fileID, `{"sheets":[{"name":"Products","header_row":true,"columns":["A"]}]}`), http.StatusOK)

	if err := f.publishFile(t, stale); !errors.Is(err, file.ErrFileChanged) {
		t.Fatalf("publish of a row read before the change = %v, want ErrFileChanged", err)
	}
	if n := f.count(t, `SELECT count(*) FROM jobs WHERE file_id = $1`, fileID); n != 0 {
		t.Errorf("the refused file has %d jobs", n)
	}
	if got, want := f.segmentKeys(t, fileID), "Products!A2=P-1|Products!A3=P-2"; got != want {
		t.Errorf("segments = %q, want the new layout's %q", got, want)
	}

	// Read again, the row publishes: a job per target language.
	fresh := f.fileRow(t, fileID)
	if err := f.publishFile(t, fresh); err != nil {
		t.Fatalf("publish of the current row: %v", err)
	}
	if n := f.count(t, `SELECT count(*) FROM jobs WHERE file_id = $1`, fileID); n != 2 {
		t.Fatalf("jobs after publish = %d, want 2 (fr, de)", n)
	}

	// A second publish holding the same row finds the jobs and skips it.
	if err := f.publishFile(t, fresh); err != nil {
		t.Errorf("second publish = %v, want the file skipped as already published", err)
	}
	if n := f.count(t, `SELECT count(*) FROM jobs WHERE file_id = $1`, fileID); n != 2 {
		t.Errorf("jobs after the second publish = %d, want 2", n)
	}
}

// A rename between publish's read and its lock changes nothing the parse
// read: the file publishes, under its new name.
func TestIntegrationXLSXPublishSurvivesARename(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXPublishRename")
	fileID := f.uploadLayout(t, testLayoutJSON)
	read := f.fileRow(t, fileID)
	testutil.AssertStatus(t, testutil.DoRequestWithRole(t, f.router, "PATCH", fmt.Sprintf("/api/projects/%d/files/%d", f.draftID, fileID),
		map[string]any{"name": "renamed.xlsx"}, f.pm.ID, f.tenant.ID, "project_manager"), http.StatusOK)

	if err := f.publishFile(t, read); err != nil {
		t.Fatalf("publish of a row read before a rename = %v, want the file published", err)
	}
	if n := f.count(t, `SELECT count(*) FROM jobs WHERE file_id = $1`, fileID); n != 2 {
		t.Errorf("jobs = %d, want 2 (fr, de)", n)
	}
	if got := f.fileRow(t, fileID).Name; got != "renamed.xlsx" {
		t.Errorf("file name = %q, want the rename kept", got)
	}
}

// Two publishes of one draft at once (a second click, the assistant and the
// page) both succeed: publish holds the file row while it creates the jobs,
// and the one that waited finds them and has nothing left to do. The file
// needs segmenting again, so the first publish changes its row.
func TestIntegrationXLSXTwoPublishesAtOnce(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXTwoPublishes")
	ctx := context.Background()
	fileID := f.uploadLayout(t, testLayoutJSON)
	if _, err := f.pool.Exec(ctx, `UPDATE segments SET source_text = 'stale' WHERE file_id = $1 AND position = 0`, fileID); err != nil {
		t.Fatal(err)
	}

	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if _, err := tx.Exec(ctx, `SELECT id FROM files WHERE id = $1 FOR UPDATE`, fileID); err != nil {
		t.Fatal(err)
	}
	publish := func() <-chan *httptest.ResponseRecorder {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- testutil.DoRequestWithRole(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", f.draftID),
				map[string]any{"analyze": false}, f.pm.ID, f.tenant.ID, "project_manager")
		}()
		return done
	}
	first, second := publish(), publish()
	// Both wait on the file row: publish takes its lock.
	for deadline := time.Now().Add(10 * time.Second); testutil.LockWaiters(t, f.pool) < 2; time.Sleep(10 * time.Millisecond) {
		select {
		case rec := <-first:
			t.Fatalf("a publish finished without waiting on the file row (status %d: %s)", rec.Code, rec.Body.String())
		case rec := <-second:
			t.Fatalf("a publish finished without waiting on the file row (status %d: %s)", rec.Code, rec.Body.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for both publishes to wait on the file row")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	for _, rec := range []*httptest.ResponseRecorder{<-first, <-second} {
		testutil.AssertStatus(t, rec, http.StatusOK)
	}
	if n := f.count(t, `SELECT count(*) FROM jobs WHERE file_id = $1`, fileID); n != 2 {
		t.Errorf("jobs = %d, want 2 (fr, de)", n)
	}
	if n := f.count(t, `SELECT count(*) FROM segments WHERE file_id = $1 AND source_text = 'stale'`, fileID); n != 0 {
		t.Errorf("%d stale segments left; publish should have segmented the file again", n)
	}
	if n := f.count(t, `SELECT count(*) FROM projects WHERE id = $1 AND status = 'in_progress'`, f.draftID); n != 1 {
		t.Errorf("the project is not in progress after the publishes")
	}
}
