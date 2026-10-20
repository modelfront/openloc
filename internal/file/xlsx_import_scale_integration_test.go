package file_test

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// A bilingual XLSX upload stamps every target cell in a few statements per
// language: the statement count must not grow with the number of rows.
func TestIntegrationUploadXLSX_TargetImportDoesNotGrowWithRows(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	countPool, log := testutil.CountingPool(t)
	router := testutil.NewTestRouter(countPool)

	tenant := testutil.SeedTenant(t, q, "XLSXScaleCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "xlsx-scale@test.com", "pass123")
	langs := []string{"fr", "de", "es"}

	upload := func(rows int) (statements int, fileID int64) {
		t.Helper()
		project := testutil.SeedProject(t, q, tenant.ID, fmt.Sprintf("XLSXScale%d", rows), "en", langs)
		cells := make([][]string, rows)
		for i := range cells {
			cells[i] = []string{fmt.Sprintf("Source %d", i), fmt.Sprintf("Cible %d", i), fmt.Sprintf("Ziel %d", i), fmt.Sprintf("Destino %d", i)}
		}
		xlsx := buildXLSXBilingual(t, append([]string{"en"}, langs...), cells)
		log.Reset()
		rec := testutil.DoMultipartUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
			"scale.xlsx", xlsx, nil, user.ID, tenant.ID, "")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var resp struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return log.Total(), resp.Data.ID
	}

	small, _ := upload(10)
	large, fileID := upload(300)
	if extra := large - small; extra > 10 {
		t.Errorf("300 rows sent %d statements, 10 rows %d: %d more for 290 rows × 3 languages, want at most 10", large, small, extra)
	}

	imported := testutil.CountRows(t, pool, `
		SELECT count(*) FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.origin = 'file_import' AND t.active_text LIKE '% %'`, fileID)
	history := testutil.CountRows(t, pool, `
		SELECT count(*) FROM translation_history th
		JOIN translations t ON t.id = th.translation_id JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND th.kind = 'file_import' AND th.created_by = $2`, fileID, user.ID)
	if imported != 900 || history != 900 {
		t.Errorf("imported %d cells with %d file_import history rows, want 900 and 900", imported, history)
	}
}
