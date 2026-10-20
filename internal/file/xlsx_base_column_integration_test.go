package file_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/parser"
	"openloc/internal/testutil"
)

// A workbook headed with bare base codes (fr, de) feeds a project whose target
// languages are regional (fr-FR, de-DE): import reads the same column the
// export writes for that job.
func TestIntegrationUploadXLSX_BaseCodeColumnFeedsRegionalTarget(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "XLSXBaseCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "xlsx-base@test.com", "pass123")
	project := testutil.SeedProject(t, q, tenant.ID, "XLSXBase", "en", []string{"fr-fr", "de-de"})

	xlsx := buildXLSXBilingual(t,
		[]string{"en", "fr", "de", "ja"},
		[][]string{{"Hello", "Bonjour", "Hallo", "こんにちは"}},
	)
	rec := testutil.DoMultipartUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"base.xlsx", xlsx, nil, user.ID, tenant.ID, "")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	rows, err := pool.Query(ctx, `
		SELECT t.target_language, COALESCE(t.active_text, ''), COALESCE(t.origin::text, '')
		FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1`, resp.Data.ID)
	if err != nil {
		t.Fatalf("query translations: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var lang, text, origin string
		if err := rows.Scan(&lang, &text, &origin); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[lang] = text + " / " + origin
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	want := map[string]string{"fr-fr": "Bonjour / file_import", "de-de": "Hallo / file_import"}
	if len(got) != len(want) {
		t.Fatalf("translations = %v, want %v", got, want)
	}
	for lang, w := range want {
		if got[lang] != w {
			t.Errorf("%s = %q, want %q", lang, got[lang], w)
		}
	}
}

// importedTargets uploads an XLSX to a new project and returns, per target
// language, "active_text | active_rich_text | origin" of the first segment.
func importedTargets(t *testing.T, header []string, row []string, targets []string) map[string]string {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "XLSXImportCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "xlsx-import@test.com", "pass123")
	project := testutil.SeedProject(t, q, tenant.ID, "XLSXImport", "en", targets)

	rec := testutil.DoMultipartUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"import.xlsx", buildXLSXBilingual(t, header, [][]string{row}), nil, user.ID, tenant.ID, "")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	rows, err := pool.Query(context.Background(), `
		SELECT t.target_language, COALESCE(t.active_text, ''), COALESCE(t.active_rich_text, ''), COALESCE(t.origin::text, '')
		FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1`, resp.Data.ID)
	if err != nil {
		t.Fatalf("query translations: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var lang, text, rich, origin string
		if err := rows.Scan(&lang, &text, &rich, &origin); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[lang] = text + " | " + rich + " | " + origin
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return got
}

// A workbook with one target column fills that column's language only; the
// project's other target languages get nothing from it.
func TestIntegrationUploadXLSX_OneColumnFillsOnlyItsLanguage(t *testing.T) {
	got := importedTargets(t, []string{"en", "fr"}, []string{"Hello", "Bonjour"}, []string{"fr-fr", "de-de"})
	if got["fr-fr"] != "Bonjour |  | file_import" {
		t.Errorf("fr-fr = %q, want the fr column", got["fr-fr"])
	}
	if got["de-de"] != " |  | " {
		t.Errorf("de-de = %q, want nothing imported", got["de-de"])
	}
}

// A target cell with inline HTML is imported as the editor's tags, like the
// source cell, not as literal markup.
func TestIntegrationUploadXLSX_TaggedTargetCellImportsAsTags(t *testing.T) {
	cell := "Bonjour <b>monde</b>"
	got := importedTargets(t, []string{"en", "fr", "de"},
		[]string{"Hello <b>world</b>", cell, "Hallo"}, []string{"fr", "de"})
	plain, rich := parser.DetectInlineTags(cell)
	if want := plain + " | " + rich + " | file_import"; rich == "" || got["fr"] != want {
		t.Errorf("fr = %q, want %q", got["fr"], want)
	}
	if got["de"] != "Hallo |  | file_import" {
		t.Errorf("de = %q", got["de"])
	}
}
