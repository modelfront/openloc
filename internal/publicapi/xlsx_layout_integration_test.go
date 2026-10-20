package publicapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"openloc/internal/publicapi"
	"openloc/internal/testutil"
)

// productsWorkbook has an ID column, English text in B under a header row,
// and nothing in the columns the translations go to.
func productsWorkbook(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", "Products"); err != nil {
		t.Fatal(err)
	}
	for cell, v := range map[string]string{"A1": "ID", "B1": "English", "A2": "P-1", "B2": "Blue chair", "A3": "P-2", "B3": "Red table"} {
		if err := f.SetCellValue("Products", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The customer API uploads a workbook with a column layout like the app does:
// only the chosen column is segmented, and the file carries its layout.
func TestIntegrationV1_UploadXLSXWithLayout(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "XLSXLayoutV1")
	key := f.key(t, "owner")

	rec := f.client.do(http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Catalog", "source_language": "en", "target_languages": []string{"fr", "de"},
	}, key)
	wantStatus(t, rec, http.StatusCreated)
	p := testutil.DecodeData[publicapi.Project](t, rec)
	path := fmt.Sprintf("/api/v1/projects/%d/files", p.ID)

	layout := `{"sheets":[{"name":"Products","header_row":true,"columns":["b"]}]}`
	rec = f.client.uploadWith(path, "catalog.xlsx", productsWorkbook(t), map[string]string{"xlsx_layout": layout}, key)
	wantStatus(t, rec, http.StatusCreated)
	got := testutil.DecodeData[publicapi.File](t, rec)
	if got.SegmentCount != 2 {
		t.Errorf("segment_count = %d, want 2 (B2 and B3; the header and the ID column are not text to translate)", got.SegmentCount)
	}
	var stored struct {
		Sheets []struct {
			Columns   []string `json:"columns"`
			HeaderRow bool     `json:"header_row"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal(got.XLSXLayout, &stored); err != nil || len(stored.Sheets) != 1 {
		t.Fatalf("xlsx_layout = %s (%v), want the layout as JSON", got.XLSXLayout, err)
	}
	if s := stored.Sheets[0]; strings.Join(s.Columns, ",") != "B" || !s.HeaderRow {
		t.Errorf("stored layout = %+v, want column B under a header row", s)
	}

	// A layout naming a sheet the workbook does not have is refused.
	bad := `{"sheets":[{"name":"Prices","columns":["B"]}]}`
	rec = f.client.uploadWith(path, "bad.xlsx", productsWorkbook(t), map[string]string{"xlsx_layout": bad}, key)
	wantStatus(t, rec, http.StatusBadRequest)
}
