package translationmemory_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
	"openloc/internal/translationmemory"
)

// An import whose process died stops updating its row; the sweep marks one
// idle for 30 minutes failed with a message, and leaves a running import and a
// finished one alone.
func TestIntegrationTMImportSweepFailsStaleImports(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "ImportSweep")
	tm := testutil.SeedTM(t, q, tenant.ID, "Sweep TM")

	newImport := func(status, idle string) int64 {
		t.Helper()
		imp, err := q.CreateTMImport(ctx, db.CreateTMImportParams{TmID: tm.ID, TenantID: tenant.ID, FileName: status + ".tmx"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE tm_imports SET status = $2, updated_at = now() - $3::interval WHERE id = $1`, imp.ID, status, idle); err != nil {
			t.Fatal(err)
		}
		return imp.ID
	}
	stuck := newImport("processing", "2 hours")
	stuckPending := newImport("pending", "31 minutes")
	running := newImport("processing", "1 minute")
	done := newImport("completed", "2 hours")

	if err := translationmemory.NewImportSweeper(q).HandleSweep(ctx, nil); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	imports, err := q.ListTMImports(ctx, tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	status := map[int64]db.TmImport{}
	for _, imp := range imports {
		status[imp.ID] = imp
	}
	for id, want := range map[int64]string{stuck: "failed", stuckPending: "failed", running: "processing", done: "completed"} {
		if got := status[id].Status; got != want {
			t.Errorf("import %d status = %q, want %q", id, got, want)
		}
	}
	if msg := status[stuck].ErrorMessage; msg == nil || *msg == "" {
		t.Errorf("a swept import carries no message")
	}
}

// The sweep removes a spooled upload no import has touched for 30 minutes,
// left by a process that died mid-import, and keeps a running import's file.
func TestIntegrationTMImportSweepRemovesStaleSpoolFiles(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	dir := translationmemory.ImportSpoolDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale, fresh := filepath.Join(dir, "upload-stale.tmx"), filepath.Join(dir, "upload-fresh.tmx")
	for _, f := range []string{stale, fresh} {
		if err := os.WriteFile(f, []byte("<tmx/>"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	if err := translationmemory.NewImportSweeper(q).HandleSweep(context.Background(), nil); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the stale spool file is still there (%v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("the running import's spool file was removed: %v", err)
	}
}
