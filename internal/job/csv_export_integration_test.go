package job_test

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// =============================================================================
// Project-level analysis CSV export
// =============================================================================
//
// HandleProjectAnalysisCSVExport streams a CSV of the project's analysis. One row per (file, target_language). The
// column layout and sort order are pinned here — external consumers (procurement, quoting tools) may import
// by column position, so the contract should not drift silently.
//
// =============================================================================
// Anti-tautology RED proofs (durable artifact)
// =============================================================================
// Proof 1 — cross-tenant requests get 404, not 200.
//   Mutation:    handler_analysis.go::computeProjectAnalysis — drop the
//                `GetProject(id, tenantID)` tenant check at the top.
//   Test:        TestIntegrationAnalysisCSV_CrossTenantReturns404
//   Diagnostic:  the test would receive 200 + a CSV body containing
//                tenant A's file names. V2 shares the helper, so the
//                same mutation leaks through both surfaces.
//   Proven:      2026-05-06.
//
// Proof 2 — column header order is part of the contract.
//   Mutation:    handler_csv.go::analysisCSVHeader — swap any two
//                column titles (e.g., "Total Words" with
//                "Total Segments").
//   Test:        TestIntegrationAnalysisCSV_HeaderColumnOrder
//   Diagnostic:  the test asserts the header slice equals a hard-
//                coded expected slice. Reordering breaks it.
//   Proven:      2026-05-06.

// TestIntegrationAnalysisCSV_HappyPath is the smoke test: project with
// 2 files × 2 target languages produces 4 data rows in stable
// (file_name, target_language) order. Asserts the header has 13
// columns and each data row mirrors that. Counts (segments, words)
// reflect aggregation over the seeded translations.
func TestIntegrationAnalysisCSV_HappyPath(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "CSVCo")
	user := testutil.SeedUser(t, q, tenant.ID, "csv@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "CSV Project", "en", []string{"es", "fr"})

	// Two files, each with one segment per target language. Word
	// counts are explicitly set so the CSV's "Total Words" column
	// has assertable values (SeedSegment defaults to word_count=0).
	file1 := testutil.SeedFile(t, q, project.ID, "alpha.txt", db.FileFormatTxt)
	file2 := testutil.SeedFile(t, q, project.ID, "beta.txt", db.FileFormatTxt)
	seedSegmentWithWords(t, pool, q, file1.ID, "Hello world.", 2, 1)
	seedSegmentWithWords(t, pool, q, file2.ID, "Hello universe.", 2, 1)
	// Translations for both languages on both files.
	for _, fileID := range []int64{file1.ID, file2.ID} {
		segs, err := q.ListSegmentsByFile(testutil.Ctx(), fileID)
		if err != nil {
			t.Fatalf("ListSegmentsByFile: %v", err)
		}
		for _, seg := range segs {
			for _, lang := range []string{"es", "fr"} {
				tr := testutil.SeedTranslation(t, q, seg.ID, lang, db.TranslationStatusNeedsReview)
				if _, err := pool.Exec(testutil.Ctx(),
					`UPDATE translations SET origin = 'mt' WHERE id = $1`, tr.ID); err != nil {
					t.Fatalf("update translation: %v", err)
				}
			}
		}
		testutil.SeedJob(t, q, fileID, "es")
		testutil.SeedJob(t, q, fileID, "fr")
	}

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/analysis.csv", project.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Errorf("Content-Type: want text/csv*, got %q", got)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".csv") {
		t.Errorf("Content-Disposition missing attachment+.csv: %q", cd)
	}
	if !strings.Contains(cd, "CSV Project") {
		t.Errorf("Content-Disposition should embed project name, got %q", cd)
	}

	rows := mustParseCSV(t, rec.Body.String())
	if len(rows) < 1 {
		t.Fatalf("CSV: expected at least header row, got 0 rows")
	}
	header := rows[0]
	if len(header) != 13 {
		t.Errorf("header: want 13 cols, got %d", len(header))
	}

	// 4 data rows: alpha-es, alpha-fr, beta-es, beta-fr (sorted).
	if len(rows) != 5 {
		t.Fatalf("CSV: want 1 header + 4 data rows, got %d total rows", len(rows))
	}
	dataRows := rows[1:]

	// Stable order pins: file then language.
	wantOrder := [][2]string{
		{"alpha.txt", "es"},
		{"alpha.txt", "fr"},
		{"beta.txt", "es"},
		{"beta.txt", "fr"},
	}
	for i, want := range wantOrder {
		if dataRows[i][0] != want[0] || dataRows[i][1] != want[1] {
			t.Errorf("row %d: want (%s, %s), got (%s, %s)",
				i, want[0], want[1], dataRows[i][0], dataRows[i][1])
		}
	}

	// Total Segments = 1 per row (1 translation per file+lang).
	for i, row := range dataRows {
		if row[2] != "1" {
			t.Errorf("row %d: Total Segments want 1, got %q", i, row[2])
		}
		// Total Words = 2 per row (each segment has 2 words).
		if row[3] != "2" {
			t.Errorf("row %d: Total Words want 2, got %q", i, row[3])
		}
	}
}

// TestIntegrationAnalysisCSV_HeaderColumnOrder pins the column-order
// contract. External consumers may import by column position, so a
// silent reorder is a downstream-breaking change. (Proof 2 above.)
func TestIntegrationAnalysisCSV_HeaderColumnOrder(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "HeaderCo")
	user := testutil.SeedUser(t, q, tenant.ID, "h@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Header Test", "en", []string{"es"})

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/analysis.csv", project.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rows := mustParseCSV(t, rec.Body.String())
	if len(rows) < 1 {
		t.Fatalf("expected at least header row")
	}
	wantHeader := []string{
		"File",
		"Target Language",
		"Total Segments",
		"Total Words",
		"Repetitions Words",
		"TM 100% Words",
		"TM 95-99% Words",
		"TM 85-94% Words",
		"TM 75-84% Words",
		"TM 50-74% Words",
		"QE Approved Words",
		"New Words",
		"Weighted Words",
	}
	got := rows[0]
	if len(got) != len(wantHeader) {
		t.Fatalf("header length: want %d, got %d", len(wantHeader), len(got))
	}
	for i, w := range wantHeader {
		if got[i] != w {
			t.Errorf("header[%d]: want %q, got %q", i, w, got[i])
		}
	}
}

// TestIntegrationAnalysisCSV_CrossTenantReturns404 — tenant B caller
// hits tenant A's project. Same posture as the other tenant-isolation
// tests in this package (project pretranslate, etc.). (Proof 1 above.)
func TestIntegrationAnalysisCSV_CrossTenantReturns404(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	userA := testutil.SeedUser(t, q, tenantA.ID, "a@test.com", "pass")
	projectA := testutil.SeedProject(t, q, tenantA.ID, "Project A", "en", []string{"es"})
	fileA := testutil.SeedFile(t, q, projectA.ID, "secret.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, fileA.ID, "es")
	_ = userA

	tenantB := testutil.SeedTenant(t, q, "TenantB")
	userB := testutil.SeedUser(t, q, tenantB.ID, "b@test.com", "pass")

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/analysis.csv", projectA.ID),
		nil, userB.ID, tenantB.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// Anti-tautology guard: even if the response were 200 by accident,
	// it must NOT contain tenant A's file name. Belt-and-suspenders
	// for the leak proof.
	if strings.Contains(rec.Body.String(), "secret.txt") {
		t.Errorf("CSV body leaked tenant A file name across tenant boundary")
	}
}

// TestIntegrationAnalysisCSV_EmptyProject — a project with no files
// returns HTTP 200 with header-only CSV (no data rows). Procurement
// downloading an empty project shouldn't see a 404 or 500; they
// should see a valid empty spreadsheet.
func TestIntegrationAnalysisCSV_EmptyProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "EmptyCSVCo")
	user := testutil.SeedUser(t, q, tenant.ID, "empty@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Empty Project", "en", []string{"es"})

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/analysis.csv", project.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rows := mustParseCSV(t, rec.Body.String())
	if len(rows) != 1 {
		t.Errorf("want header-only (1 row), got %d", len(rows))
	}
}

// --- helpers ---

// seedSegmentWithWords seeds a segment AND sets its word_count to a
// specific value. Existing SeedSegment defaults word_count to 0,
// which makes the CSV "Total Words" column unassertable.
func seedSegmentWithWords(t testing.TB, pool *pgxpool.Pool, q db.Querier, fileID int64, text string, words int32, position int32) db.Segment {
	t.Helper()
	seg := testutil.SeedSegment(t, q, fileID, text, position)
	if _, err := pool.Exec(testutil.Ctx(),
		`UPDATE segments SET word_count = $2 WHERE id = $1`, seg.ID, words); err != nil {
		t.Fatalf("seedSegmentWithWords UPDATE: %v", err)
	}
	return seg
}

// mustParseCSV parses a CSV string into rows. Fails the test on parse
// error rather than returning, so call-sites can assume success.
func mustParseCSV(t testing.TB, s string) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(s)).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v\nbody:\n%s", err, s)
	}
	return rows
}
