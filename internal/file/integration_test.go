package file_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/xuri/excelize/v2"
	"openloc/internal/testutil"
)

// TestIntegrationUploadCreatesAllRecords verifies that file upload atomically creates
// file + segments + translations + jobs within a single transaction.
func TestIntegrationUploadCreatesAllRecords(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Seed tenant + user + project (2 target languages)
	tenant := testutil.SeedTenant(t, q, "UploadCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "uploader@test.com", "pass123")
	project := testutil.SeedProject(t, q, tenant.ID, "UploadProject", "en", []string{"fr", "de"})

	// Upload a simple JSON i18n file (2 segments × 2 target langs → 4 translations, 2 jobs)
	fileContent := `{"greeting": "Hello world", "farewell": "Goodbye"}`
	rec := testutil.DoFileUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"test.json", "application/json", fileContent, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	// Decode the response to get the file ID
	var resp struct {
		Data struct {
			ID           int64 `json:"id"`
			SegmentCount int32 `json:"segment_count"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	fileID := resp.Data.ID
	if fileID == 0 {
		t.Fatal("expected non-zero file ID")
	}
	if resp.Data.SegmentCount != 2 {
		t.Fatalf("expected 2 segments, got %d", resp.Data.SegmentCount)
	}

	// Verify segments were created
	var segCount int
	err := pool.QueryRow(ctx, "SELECT count(*) FROM segments WHERE file_id = $1", fileID).Scan(&segCount)
	if err != nil {
		t.Fatalf("count segments: %v", err)
	}
	if segCount != 2 {
		t.Fatalf("expected 2 segments in DB, got %d", segCount)
	}

	// Verify translations: 2 segments × 2 target langs = 4
	var txnCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM translations t JOIN segments s ON t.segment_id = s.id WHERE s.file_id = $1`,
		fileID).Scan(&txnCount)
	if err != nil {
		t.Fatalf("count translations: %v", err)
	}
	if txnCount != 4 {
		t.Fatalf("expected 4 translations (2 segments × 2 langs), got %d", txnCount)
	}

	// Verify jobs: 1 per target language
	var jobCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE file_id = $1", fileID).Scan(&jobCount)
	if err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if jobCount != 2 {
		t.Fatalf("expected 2 jobs, got %d", jobCount)
	}
}

// TestIntegrationUploadCrossTenantBlocked verifies that uploading to another tenant's project fails.
func TestIntegrationUploadCrossTenantBlocked(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	// Tenant A owns the project
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	testutil.SeedUser(t, q, tenantA.ID, "a@test.com", "pass")
	project := testutil.SeedProject(t, q, tenantA.ID, "ProjectA", "en", []string{"fr"})

	// Tenant B tries to upload
	tenantB := testutil.SeedTenant(t, q, "TenantB")
	userB := testutil.SeedUser(t, q, tenantB.ID, "b@test.com", "pass")

	rec := testutil.DoFileUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"evil.json", "application/json", `{"key": "value"}`, userB.ID, tenantB.ID)

	// Should get 404 (project not found for this tenant)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

// buildXLSXBilingual returns an in-memory XLSX where row 1 is a language-code
// header (en | fr | de) and subsequent rows are source + target translations.
// Mirrors the public XLSX bilingual format the file handler ingests via
// `parser.ExtractXLSXTargets`.
func buildXLSXBilingual(t *testing.T, header []string, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()

	for i, cell := range header {
		cellName, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue("Sheet1", cellName, cell)
	}
	for r, row := range rows {
		for c, cell := range row {
			cellName, _ := excelize.CoordinatesToCellName(c+1, r+2)
			_ = f.SetCellValue("Sheet1", cellName, cell)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("buildXLSXBilingual: %v", err)
	}
	return buf.Bytes()
}

// TestIntegrationUploadXLSXBilingual — XLSX with target-language columns must
// set active_text + origin='file_import' (status needs_review, deriving as
// pre_translated) for each (segment × language) pair via the sqlc query
// `ImportBilingualTargets`. Catches:
//   - SQL drift (column renames, default changes)
//   - Missing target-language updates
//   - origin getting dropped silently
func TestIntegrationUploadXLSXBilingual(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "XLSXCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "xlsx@test.com", "pass123")
	project := testutil.SeedProject(t, q, tenant.ID, "XLSXProject", "en", []string{"fr", "de"})

	xlsx := buildXLSXBilingual(t,
		[]string{"en", "fr", "de"},
		[][]string{
			{"Hello", "Bonjour", "Hallo"},
			{"Goodbye", "Au revoir", "Auf Wiedersehen"},
		},
	)

	rec := testutil.DoMultipartUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"bilingual.xlsx", xlsx, nil, user.ID, tenant.ID, "")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var resp struct {
		Data struct {
			ID           int64 `json:"id"`
			SegmentCount int32 `json:"segment_count"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	fileID := resp.Data.ID
	if fileID == 0 {
		t.Fatal("expected non-zero file ID")
	}
	if resp.Data.SegmentCount != 2 {
		t.Fatalf("expected 2 segments, got %d", resp.Data.SegmentCount)
	}

	// Each (segment, target lang) translation row should have:
	//   active_text = the localized cell, origin = 'file_import',
	//   status = 'needs_review' (derives as pre_translated). Origin
	//   carries the 'file_import' provenance.
	rows, err := pool.Query(ctx, `
		SELECT s.source_text, t.target_language, t.active_text,
		       t.origin::text, t.status::text
		FROM translations t
		JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1
		ORDER BY s.id, t.target_language`, fileID)
	if err != nil {
		t.Fatalf("query translations: %v", err)
	}
	defer rows.Close()

	type rowSnapshot struct {
		Source, Lang, ActiveText, Origin, Status string
	}
	var got []rowSnapshot
	for rows.Next() {
		var r rowSnapshot
		if err := rows.Scan(&r.Source, &r.Lang, &r.ActiveText, &r.Origin, &r.Status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	want := map[string]rowSnapshot{
		"Hello|de":   {Source: "Hello", Lang: "de", ActiveText: "Hallo", Origin: "file_import", Status: "needs_review"},
		"Hello|fr":   {Source: "Hello", Lang: "fr", ActiveText: "Bonjour", Origin: "file_import", Status: "needs_review"},
		"Goodbye|de": {Source: "Goodbye", Lang: "de", ActiveText: "Auf Wiedersehen", Origin: "file_import", Status: "needs_review"},
		"Goodbye|fr": {Source: "Goodbye", Lang: "fr", ActiveText: "Au revoir", Origin: "file_import", Status: "needs_review"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d translation rows, got %d (%v)", len(want), len(got), got)
	}
	for _, r := range got {
		key := r.Source + "|" + r.Lang
		w, ok := want[key]
		if !ok {
			t.Errorf("unexpected row %s", key)
			continue
		}
		if r != w {
			t.Errorf("row %s: got %+v, want %+v", key, r, w)
		}
	}
}
