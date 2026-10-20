package pipeline

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/xuri/excelize/v2"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
)

// withTestClaims adds auth claims to a request (simulates authenticated middleware).
func withTestClaims(r *http.Request, tenantID int64) *http.Request {
	ctx := auth.WithClaims(r.Context(), &auth.Claims{
		TenantID: tenantID,
		UserID:   1,
		Role:     "admin",
	})
	return r.WithContext(ctx)
}

func newTestRouter(h *Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/files/{fileID}/export", h.HandleExportFile)
	return r
}

func TestHandleExportFile_CrossTenantBlocked(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProjectTenant(1, 10, 1, nil, "en", []string{"de"})
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	h := NewHandler(mq, svc)

	r := newTestRouter(h)
	req := withTestClaims(httptest.NewRequest(http.MethodGet, "/files/1/export?lang=de", nil), 2)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant export, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleExportFile_MissingLang(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	h := NewHandler(mq, svc)

	r := newTestRouter(h)
	req := withTestClaims(httptest.NewRequest(http.MethodGet, "/files/1/export", nil), 1) // no ?lang=
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// =============================================================================
// Handler dispatch tests for ?format=
// =============================================================================

func TestHandleExportFile_BilingualXLIFFContentType(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	h := NewHandler(mq, svc)

	r := newTestRouter(h)
	req := withTestClaims(httptest.NewRequest(http.MethodGet, "/files/1/export?lang=de&format=xliff", nil), 1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/x-xliff+xml" {
		t.Errorf("Content-Type: got %q, want application/x-xliff+xml", got)
	}
	cd := w.Header().Get("Content-Disposition")
	if !contains(cd, ".xlf") {
		t.Errorf("Content-Disposition should include .xlf filename, got %q", cd)
	}
	// The served export is history: one job.exported event naming the format.
	if len(mq.projectEvents) != 1 || mq.projectEvents[0].EventType != "job.exported" ||
		!contains(string(mq.projectEvents[0].Details), `"format":"xliff"`) {
		t.Errorf("want one job.exported event with format xliff, got %+v", mq.projectEvents)
	}
}

func TestHandleExportFile_InvalidFormatReturns400(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	h := NewHandler(mq, svc)

	r := newTestRouter(h)
	req := withTestClaims(httptest.NewRequest(http.MethodGet, "/files/1/export?lang=de&format=pdf", nil), 1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown format, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !contains(body, "pdf") || !contains(body, "xliff") {
		t.Errorf("error body should name the bad format and the allowed values, got: %s", body)
	}
}

func TestHandleExportFile_BilingualCrossTenantBlocked(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProjectTenant(1, 10, 1, nil, "en", []string{"de"})
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	h := NewHandler(mq, svc)

	r := newTestRouter(h)
	req := withTestClaims(httptest.NewRequest(http.MethodGet, "/files/1/export?lang=de&format=xliff", nil), 2)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant bilingual export, got %d: %s", w.Code, w.Body.String())
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// A workbook with several target columns and none for the job's language is a
// problem the user can fix, so the export answers 400 with the column list,
// not a generic failure.
func TestHandleExportFile_XLSXNoColumnForLanguageReturns400(t *testing.T) {
	wb := excelize.NewFile()
	for cell, v := range map[string]string{"A1": "en", "B1": "fr", "C1": "de", "A2": "Hello", "B2": "Bonjour", "C2": "Hallo"} {
		if err := wb.SetCellValue("Sheet1", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	var upload bytes.Buffer
	if err := wb.Write(&upload); err != nil {
		t.Fatal(err)
	}
	wb.Close()

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"fr", "de", "es"})
	file := mq.files[1]
	file.Format = db.FileFormatXlsx
	file.Name = "strings.xlsx"
	mq.files[1] = file
	svc := NewService(mq, &passthroughTxRunner{q: mq}, memStorage(upload.Bytes()))
	h := NewHandler(mq, svc)

	r := newTestRouter(h)
	req := withTestClaims(httptest.NewRequest(http.MethodGet, "/files/1/export?lang=es", nil), 1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"es", "de, fr"} {
		if !strings.Contains(body, want) {
			t.Errorf("400 body must name the language and the columns, got %s", body)
		}
	}
}
