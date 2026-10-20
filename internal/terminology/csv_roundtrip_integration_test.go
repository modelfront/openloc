package terminology_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"openloc/internal/testutil"
)

func createCSVBase(t *testing.T, router chi.Router, name string, userID, tenantID int64) int64 {
	t.Helper()
	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": name, "source_language": "en", "target_language": "es",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return resp.Data.ID
}

func importCSV(t *testing.T, router chi.Router, baseID int64, content string, userID, tenantID int64) (imported, skipped int) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "terms.csv")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte(content))
	w.Close()
	rec := testutil.DoRequestRaw(t, router, "POST", fmt.Sprintf("/api/terminology/%d/import?target=es", baseID),
		&buf, w.FormDataContentType(), userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return resp.Imported, resp.Skipped
}

func exportCSV(t *testing.T, router chi.Router, baseID, userID, tenantID int64) string {
	t.Helper()
	rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/export?target=es", baseID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	return rec.Body.String()
}

// The app's own CSV export imports back into another term base unchanged; a
// header in any column order, and Excel's UTF-8 byte order mark, are read as
// a header, never as a term.
func TestIntegrationCSVExportImportRoundTrip(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "CSVRound")
	user := testutil.SeedUser(t, q, tenant.ID, "csv-round@test.com", "pass")

	first := createCSVBase(t, router, "First", user.ID, tenant.ID)
	if n, s := importCSV(t, router, first, "source_term,target_term,status,notes,domain\n"+
		"hello,hola,approved,greeting,general\n"+
		"bad,malo,forbidden,,\n", user.ID, tenant.ID); n != 2 || s != 0 {
		t.Fatalf("seed import: %d imported, %d skipped", n, s)
	}
	exported := exportCSV(t, router, first, user.ID, tenant.ID)

	second := createCSVBase(t, router, "Second", user.ID, tenant.ID)
	if n, s := importCSV(t, router, second, exported, user.ID, tenant.ID); n != 2 || s != 0 {
		t.Fatalf("re-import of the export: %d imported, %d skipped\n%s", n, s, exported)
	}
	if again := exportCSV(t, router, second, user.ID, tenant.ID); again != exported {
		t.Errorf("round trip changed the terms:\nfirst:\n%s\nsecond:\n%s", exported, again)
	}

	third := createCSVBase(t, router, "Third", user.ID, tenant.ID)
	if n, s := importCSV(t, router, third, "\ufefftarget_term,notes,source_term,status,domain\n"+
		"hola,greeting,hello,approved,general\n"+
		"malo,,bad,forbidden,\n", user.ID, tenant.ID); n != 2 || s != 0 {
		t.Fatalf("reordered header with BOM: %d imported, %d skipped", n, s)
	}
	if got := exportCSV(t, router, third, user.ID, tenant.ID); got != exported {
		t.Errorf("reordered header imported differently:\nwant:\n%s\ngot:\n%s", exported, got)
	}
}
