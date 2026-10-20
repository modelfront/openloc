package translationmemory_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
	"openloc/internal/translationmemory"
)

func tmxUnits(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><tmx version="1.4"><header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="t" creationtoolversion="1" o-tmf="t"/><body>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<tu tuid="%d"><tuv xml:lang="en"><seg>Source %d</seg></tuv><tuv xml:lang="es"><seg>Fuente %d</seg></tuv></tu>`, i, i, i)
	}
	return b.String()
}

func uploadTMX(t *testing.T, router chi.Router, tmID int64, content string, userID, tenantID int64) (*http.Response, db.TmImport) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "memory.tmx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	rec := testutil.DoRequestRaw(t, router, "POST", fmt.Sprintf("/api/tms/%d/import", tmID), &buf, w.FormDataContentType(), userID, tenantID)
	var env struct {
		Data db.TmImport `json:"data"`
	}
	if rec.Code == http.StatusAccepted {
		testutil.DecodeJSON(t, rec, &env)
	}
	return rec.Result(), env.Data
}

// waitImportDone polls until the import has ended: its status is final, a
// failed one carries its message, and the spooled upload is gone (the import
// removes it last).
func waitImportDone(t *testing.T, q db.Querier, id int64) db.TmImport {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		imp, err := q.GetTMImport(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		ended := imp.Status == "completed" || imp.Status == "failed" && imp.ErrorMessage != nil
		if ended && len(spoolFiles(t)) == 0 {
			return imp
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the import never finished")
	return db.TmImport{}
}

// spoolFiles lists the uploads waiting in the spool directory. Tests point
// TMPDIR at their own directory first, so other tests' uploads never show.
func spoolFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(translationmemory.ImportSpoolDir(), "*"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// A TMX file that breaks after 1200 units is accepted at once, then fails on
// its row with a message before anything is written, so a fixed re-upload
// does not duplicate entries; the spooled copy of the upload is deleted.
func TestIntegrationTMXImport_BrokenFileFailsOnTheRowWithNothingImported(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TMXStream")
	user := testutil.SeedUser(t, q, tenant.ID, "tmx-stream@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Stream TM")

	resp, imp := uploadTMX(t, router, tm.ID, tmxUnits(1200)+`<tu><tuv xml:lang="en"><seg>cut <<`, user.ID, tenant.ID)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload status %d, want 202 with the import row", resp.StatusCode)
	}
	done := waitImportDone(t, q, imp.ID)
	if done.Status != "failed" || done.ErrorMessage == nil || *done.ErrorMessage == "" {
		t.Errorf("import = %s %v, want failed with a message", done.Status, done.ErrorMessage)
	}
	if done.Imported != 0 {
		t.Errorf("import counted %d imported, want 0", done.Imported)
	}
	n, err := q.CountTMEntries(context.Background(), db.CountTMEntriesParams{TmID: tm.ID})
	if err != nil || n != 0 {
		t.Errorf("TM holds %d entries (%v), want 0", n, err)
	}
	if left := spoolFiles(t); len(left) != 0 {
		t.Errorf("spooled uploads left behind: %v", left)
	}
}

// A whole file imports completely and leaves no spooled copy; a file that is
// not TMX at all is still refused at upload.
func TestIntegrationTMXImport_CompletesAndRefusesNonTMX(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TMXWhole")
	user := testutil.SeedUser(t, q, tenant.ID, "tmx-whole@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Whole TM")

	resp, imp := uploadTMX(t, router, tm.ID, tmxUnits(1234)+`</body></tmx>`, user.ID, tenant.ID)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload status %d, want 202", resp.StatusCode)
	}
	done := waitImportDone(t, q, imp.ID)
	if done.Status != "completed" || done.Imported != 1234 || done.TotalUnits != 1234 || done.Skipped != 0 {
		t.Errorf("import = %+v, want completed with 1234 of 1234", done)
	}

	for _, body := range []string{"not xml at all <", tmxUnits(0) + `</body></tmx>`} {
		resp, _ := uploadTMX(t, router, tm.ID, body, user.ID, tenant.ID)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("upload of %.30q: status %d, want 400", body, resp.StatusCode)
		}
	}
	if left := spoolFiles(t); len(left) != 0 {
		t.Errorf("spooled uploads left behind: %v", left)
	}
}

// total_units is the number of entries in the whole file from the first batch
// on, so the progress bar moves from 0 to 100% as the import runs.
func TestIntegrationTMXImport_TotalUnitsIsTheFileFromTheStart(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TMXTotal")
	user := testutil.SeedUser(t, q, tenant.ID, "tmx-total@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Total TM")

	first := make(chan db.TmImport, 1)
	defer translationmemory.SetImportBatchHook(func(id int64) {
		imp, err := q.GetTMImport(context.Background(), id)
		if err != nil {
			t.Error(err)
			return
		}
		select {
		case first <- imp:
		default:
		}
	})()

	resp, imp := uploadTMX(t, router, tm.ID, tmxUnits(1234)+`</body></tmx>`, user.ID, tenant.ID)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload status %d, want 202", resp.StatusCode)
	}
	waitImportDone(t, q, imp.ID)
	// The import has finished: the first batch's progress is buffered or never coming.
	var got db.TmImport
	select {
	case got = <-first:
	default:
		t.Fatal("the batch hook never ran")
	}
	if got.TotalUnits != 1234 || got.Imported != 500 {
		t.Errorf("after the first batch: %d imported of %d, want 500 of 1234", got.Imported, got.TotalUnits)
	}
}
