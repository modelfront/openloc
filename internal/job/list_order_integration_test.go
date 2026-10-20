package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationListJobsByProject_OrderedByFileCreatedAtDesc pins that the
// project files list is ordered newest-upload-first and each row carries
// `file_created_at` so the UI can show "x min ago" and disambiguate same-name
// uploads.
//
// Anti-tautology RED: revert `ORDER BY f.created_at DESC, j.target_language`
// in queries.sql back to `f.name, j.target_language` and the assertion that
// the newer file appears first fails (alphabetical puts a_old first).
func TestIntegrationListJobsByProject_OrderedByFileCreatedAtDesc(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "OrderCo")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@order.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Order Test", "en", []string{"es", "fr"})

	// Names are chosen so alphabetical and chronological orderings diverge:
	// older file has alphabetically-earlier name (a_old < b_new), so alpha-sort
	// puts older first while chrono-DESC puts newer first.
	older := testutil.SeedFile(t, q, project.ID, "a_old.txt", db.FileFormatTxt)
	newer := testutil.SeedFile(t, q, project.ID, "b_new.txt", db.FileFormatTxt)
	// Backdate the older file by a full day so the second-precision RFC3339
	// timestamps in the response are unambiguously distinct (no time.Sleep flake).
	if _, err := pool.Exec(context.Background(), "UPDATE files SET created_at = created_at - INTERVAL '1 day' WHERE id = $1", older.ID); err != nil {
		t.Fatalf("backdate older file: %v", err)
	}
	testutil.SeedJob(t, q, older.ID, "es")
	testutil.SeedJob(t, q, older.ID, "fr")
	testutil.SeedJob(t, q, newer.ID, "es")
	testutil.SeedJob(t, q, newer.ID, "fr")

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/jobs", project.ID),
		nil, user.ID, tenant.ID,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data []struct {
			FileID        int64  `json:"file_id"`
			FileName      string `json:"file_name"`
			FileCreatedAt string `json:"file_created_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 4 {
		t.Fatalf("want 4 jobs, got %d", len(resp.Data))
	}

	gotNames := []string{resp.Data[0].FileName, resp.Data[1].FileName, resp.Data[2].FileName, resp.Data[3].FileName}
	wantNames := []string{"b_new.txt", "b_new.txt", "a_old.txt", "a_old.txt"}
	for i := range wantNames {
		if gotNames[i] != wantNames[i] {
			t.Errorf("row %d: file_name = %q, want %q (full order: %v)", i, gotNames[i], wantNames[i], gotNames)
		}
	}

	if resp.Data[0].FileCreatedAt == "" {
		t.Fatalf("file_created_at missing from response (row 0)")
	}
	newerTS, err := time.Parse(time.RFC3339, resp.Data[0].FileCreatedAt)
	if err != nil {
		t.Fatalf("parse newer file_created_at %q: %v", resp.Data[0].FileCreatedAt, err)
	}
	olderTS, err := time.Parse(time.RFC3339, resp.Data[2].FileCreatedAt)
	if err != nil {
		t.Fatalf("parse older file_created_at %q: %v", resp.Data[2].FileCreatedAt, err)
	}
	if !newerTS.After(olderTS) {
		t.Errorf("newer file_created_at (%s) should be after older (%s)", newerTS, olderTS)
	}
}
