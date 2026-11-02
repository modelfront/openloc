package file_test

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// scaleUnits are n units. Every third has no target, and unit 1's target
// carries an inline tag.
func scaleUnits(n int) []xliffSpec {
	units := make([]xliffSpec, n)
	for i := range units {
		units[i] = xliffSpec{Key: fmt.Sprintf("u%d", i), Source: fmt.Sprintf("Source %d", i)}
		switch {
		case i%3 == 0:
		case i == 1:
			units[i].Target = `Ziel <ph id="1">{0}</ph> 1`
		default:
			units[i].Target = fmt.Sprintf("Ziel %d", i)
		}
	}
	return units
}

// An XLIFF upload stamps its targets in a few statements: the statement count
// must not grow with the number of units.
func TestIntegrationUploadXLIFF_TargetImportDoesNotGrowWithUnits(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	countPool, log := testutil.CountingPool(t)
	router := testutil.NewTestRouter(countPool)

	tenant := testutil.SeedTenant(t, q, "XLIFFScaleCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "xliff-scale@test.com", "pass123")

	upload := func(units int) (statements int, fileID int64) {
		t.Helper()
		project := testutil.SeedProject(t, q, tenant.ID, fmt.Sprintf("XLIFFScale%d", units), "en", []string{"de"})
		log.Reset()
		rec := testutil.DoMultipartUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
			"scale.xliff", []byte(buildXLIFF(t, "en", "de", scaleUnits(units))), nil, user.ID, tenant.ID, "")
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
		t.Errorf("300 units sent %d statements, 10 units %d: %d more for 290 units, want at most 10", large, small, extra)
	}

	// 200 of the 300 units have a target; the other 100 stay untranslated.
	imported := testutil.CountRows(t, pool, `
		SELECT count(*) FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.target_language = 'de' AND t.origin = 'file_import'
		  AND t.status = 'needs_review' AND t.active_text = 'Ziel ' || s.position::text`, fileID)
	empty := testutil.CountRows(t, pool, `
		SELECT count(*) FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.active_text IS NULL AND t.origin IS NULL AND t.active_rich_text IS NULL`, fileID)
	tagged := testutil.CountRows(t, pool, `
		SELECT count(*) FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND s.position = 1 AND t.origin = 'file_import' AND t.active_rich_text IS NOT NULL`, fileID)
	if imported != 199 || empty != 100 || tagged != 1 {
		t.Errorf("imported %d plain targets, %d untranslated, %d tagged target with rich text; want 199, 100, 1", imported, empty, tagged)
	}
}
