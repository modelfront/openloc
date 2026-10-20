package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"openloc/internal/auth"
	"openloc/internal/extension"
)

type failingExtensions struct{ err error }

func (f failingExtensions) List(context.Context, int64) ([]extension.Installed, error) {
	return nil, f.err
}
func (f failingExtensions) GetByProvider(context.Context, int64, string) (extension.Installed, error) {
	return extension.Installed{}, f.err
}
func (f failingExtensions) CheckKey(context.Context, int64, string, string, string) error {
	return f.err
}

func postConnection(h *Handler, provider string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Post("/{provider}/connections", h.HandleCreateConnection)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/"+provider+"/connections", strings.NewReader(`{}`))
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: 1, UserID: 1, Role: "owner"}))
	r.ServeHTTP(rec, req)
	return rec
}

// A failed lookup is a server error, not an unknown provider: a database
// hiccup must not tell an admin their custom provider does not exist.
func TestValidProvider_LookupFailureIsAServerError(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil).WithExtensions(failingExtensions{err: errors.New("db down")})
	if rec := postConnection(h, "ext:acme"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%s), want 500", rec.Code, rec.Body.String())
	}
}

// A provider that is not installed is still a 400.
func TestValidProvider_NotInstalledIsABadRequest(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil).WithExtensions(failingExtensions{err: extension.ErrNotInstalled})
	if rec := postConnection(h, "ext:acme"); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (%s), want 400", rec.Code, rec.Body.String())
	}
}
