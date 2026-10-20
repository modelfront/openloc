package productdocs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"openloc/internal/auth"
)

func newTestRouter(t *testing.T) *chi.Mux {
	t.Helper()
	h := NewHandler(mustIndex(t))
	r := chi.NewRouter()
	// Mirrors the registration in internal/api/api.go.
	r.Route("/api/docs", func(r chi.Router) {
		r.Get("/search", h.HandleSearch)
		r.Get("/assets/*", h.HandleAsset)
		r.Get("/", h.HandleTree)
		r.Get("/*", h.HandlePage)
	})
	return r
}

func asRole(req *http.Request, role string) *http.Request {
	return req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: 1, UserID: 1, Role: role}))
}

func do(t *testing.T, r http.Handler, role, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := asRole(httptest.NewRequest(method, target, nil), role)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var env struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return env.Data
}

func TestHandleTree_RoleFiltered(t *testing.T) {
	r := newTestRouter(t)

	w := do(t, r, "linguist", http.MethodGet, "/api/docs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	tree := decodeData[[]DocSection](t, w)
	for _, s := range tree {
		for _, p := range s.Pages {
			if p.Slug == "guides/vendors" {
				t.Fatal("linguist tree leaks the admin-only vendors page")
			}
		}
	}

	w = do(t, r, "owner", http.MethodGet, "/api/docs", nil)
	tree = decodeData[[]DocSection](t, w)
	if len(tree) != 2 || len(tree[0].Pages) != 2 {
		t.Fatalf("owner tree: %+v", tree)
	}
}

func TestHandlePage_404ForUnknownAndForbidden(t *testing.T) {
	r := newTestRouter(t)

	w := do(t, r, "owner", http.MethodGet, "/api/docs/guides/cat-editor", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("owner page: %d %s", w.Code, w.Body.String())
	}
	page := decodeData[DocPage](t, w)
	if page.Title != "The CAT editor" || page.Body == "" || page.SectionTitle != "Guides" {
		t.Fatalf("page payload: %+v", page)
	}

	// Same status and body shape for "does not exist" and "not for your role".
	unknown := do(t, r, "linguist", http.MethodGet, "/api/docs/guides/nope", nil)
	forbidden := do(t, r, "linguist", http.MethodGet, "/api/docs/guides/vendors", nil)
	if unknown.Code != http.StatusNotFound || forbidden.Code != http.StatusNotFound {
		t.Fatalf("want 404/404, got %d/%d", unknown.Code, forbidden.Code)
	}
	if unknown.Body.String() != forbidden.Body.String() {
		t.Fatalf("existence leaks through the body: %q vs %q", unknown.Body.String(), forbidden.Body.String())
	}

	// Slugs must be exactly section/stem; anything else is not a page.
	for _, bad := range []string{"/api/docs/guides", "/api/docs/guides/cat-editor/extra", "/api/docs/../etc/passwd", "/api/docs/Guides/Cat"} {
		if w := do(t, r, "owner", http.MethodGet, bad, nil); w.Code != http.StatusNotFound {
			t.Fatalf("%s: want 404, got %d", bad, w.Code)
		}
	}
}

func TestHandleSearch(t *testing.T) {
	r := newTestRouter(t)

	if w := do(t, r, "owner", http.MethodGet, "/api/docs/search", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("missing q: want 400, got %d", w.Code)
	}
	long := make([]byte, maxQueryLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if w := do(t, r, "owner", http.MethodGet, "/api/docs/search?q="+string(long), nil); w.Code != http.StatusBadRequest {
		t.Fatalf("overlong q: want 400, got %d", w.Code)
	}

	w := do(t, r, "linguist", http.MethodGet, "/api/docs/search?q=vendor", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d", w.Code)
	}
	if hits := decodeData[[]DocSearchHit](t, w); len(hits) != 0 {
		t.Fatalf("linguist search leaks admin-only page: %+v", hits)
	}
	w = do(t, r, "admin", http.MethodGet, "/api/docs/search?q=vendor", nil)
	if hits := decodeData[[]DocSearchHit](t, w); len(hits) != 1 || hits[0].SectionTitle != "Guides" {
		t.Fatalf("admin search: %+v", hits)
	}
}

func TestHandleAsset(t *testing.T) {
	r := newTestRouter(t)

	w := do(t, r, "linguist", http.MethodGet, "/api/docs/assets/cat-editor.png", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("asset: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type: %q", ct)
	}
	etag := w.Header().Get("ETag")
	if etag == "" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing caching/safety headers: %v", w.Header())
	}
	if w.Body.String() != "\x89PNG fake" {
		t.Fatalf("body: %q", w.Body.String())
	}

	// Revalidation: matching ETag → 304 with no body.
	w = do(t, r, "linguist", http.MethodGet, "/api/docs/assets/cat-editor.png", map[string]string{"If-None-Match": etag})
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("304: got %d with %d bytes", w.Code, w.Body.Len())
	}

	for _, bad := range []string{
		"/api/docs/assets/nope.png",
		"/api/docs/assets/../guides/cat-editor.md",
		"/api/docs/assets/sub/cat-editor.png",
		"/api/docs/assets/cat-editor.md",
		"/api/docs/assets/Cat-Editor.png",
	} {
		if w := do(t, r, "owner", http.MethodGet, bad, nil); w.Code != http.StatusNotFound {
			t.Fatalf("%s: want 404, got %d", bad, w.Code)
		}
	}
}

func TestHandleTree_AnonymousGetsPublicPages(t *testing.T) {
	r := newTestRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	tree := decodeData[[]DocSection](t, w)
	if len(tree) != 2 || len(tree[0].Pages) != 1 || tree[0].Pages[0].Slug != "guides/cat-editor" {
		t.Fatalf("anonymous tree: %+v", tree)
	}
}
