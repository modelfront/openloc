package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func teapot() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
}

func TestDocSitesOnly(t *testing.T) {
	h := docSitesOnly("https://app.example.com/", docSites("docs.example.com", "developer.example.com"))(teapot())

	cases := []struct {
		host, path   string
		wantCode     int
		wantLocation string
	}{
		// On the docs host the help sits at the root: every page path is the SPA.
		{"docs.example.com", "/", http.StatusTeapot, ""},
		{"docs.example.com", "/guides/cat-editor", http.StatusTeapot, ""},
		{"docs.example.com", "/api/docs", http.StatusTeapot, ""},
		{"docs.example.com", "/api/docs/assets/a.png", http.StatusTeapot, ""},
		{"docs.example.com", "/api/config", http.StatusTeapot, ""},
		{"docs.example.com", "/robots.txt", http.StatusTeapot, ""},
		{"docs.example.com", "/assets/index-abc.js", http.StatusTeapot, ""},
		// On its own host a prefixed path is just a page path (the SPA's "not found").
		{"docs.example.com", "/docs", http.StatusTeapot, ""},
		{"DOCS.example.com:443", "/docs/guides/cat-editor", http.StatusTeapot, ""},
		// The rest of the API is not on the docs host, the API docs' API included.
		{"docs.example.com", "/api/projects", http.StatusNotFound, ""},
		{"docs.example.com", "/api/api-docs", http.StatusNotFound, ""},
		{"docs.example.com", "/api/auth/me", http.StatusNotFound, ""},
		{"docs.example.com", "/api/foo.json", http.StatusNotFound, ""},
		{"docs.example.com", "/mcp", http.StatusNotFound, ""},
		{"docs.example.com", "/oauth/token", http.StatusNotFound, ""},
		// The API docs live at the developer host's root, with their own API.
		{"developer.example.com", "/", http.StatusTeapot, ""},
		{"developer.example.com", "/endpoints/getJob", http.StatusTeapot, ""},
		{"developer.example.com", "/api/api-docs", http.StatusTeapot, ""},
		{"developer.example.com", "/api/api-docs/openapi.json", http.StatusTeapot, ""},
		{"developer.example.com", "/api/config", http.StatusTeapot, ""},
		{"developer.example.com", "/api-docs/endpoints/getJob", http.StatusTeapot, ""},
		{"developer.example.com", "/api/docs", http.StatusNotFound, ""},
		{"developer.example.com", "/api/v1/projects", http.StatusNotFound, ""},
		// Each site's pages move to their host from anywhere.
		{"developer.example.com", "/docs/guides/mcp", http.StatusMovedPermanently, "https://docs.example.com/guides/mcp"},
		{"docs.example.com", "/api-docs", http.StatusMovedPermanently, "https://developer.example.com/"},
		{"app.example.com", "/docs", http.StatusMovedPermanently, "https://docs.example.com/"},
		{"app.example.com:443", "/docs/guides/cat-editor?q=1", http.StatusMovedPermanently, "https://docs.example.com/guides/cat-editor?q=1"},
		{"app.example.com", "/api-docs/endpoints/getJob", http.StatusMovedPermanently, "https://developer.example.com/endpoints/getJob"},
		// The app host is otherwise untouched.
		{"app.example.com", "/", http.StatusTeapot, ""},
		{"app.example.com", "/api/projects", http.StatusTeapot, ""},
		{"app.example.com", "/api/docs", http.StatusTeapot, ""},
		{"app.example.com", "/api/api-docs", http.StatusTeapot, ""},
		{"app.example.com", "/docsearch", http.StatusTeapot, ""},
		{"app.example.com", "/api-docsx", http.StatusTeapot, ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		req.Host = c.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.wantCode {
			t.Errorf("%s%s: status want %d, got %d", c.host, c.path, c.wantCode, w.Code)
		}
		if got := w.Header().Get("Location"); got != c.wantLocation {
			t.Errorf("%s%s: Location want %q, got %q", c.host, c.path, c.wantLocation, got)
		}
	}
}

// One site configured: only it moves; the other stays at its prefix.
func TestDocSitesOnly_OneSite(t *testing.T) {
	h := docSitesOnly("https://app.example.com", docSites("", "developer.example.com"))(teapot())
	for _, c := range []struct {
		host, path string
		wantCode   int
	}{
		{"app.example.com", "/docs/guides/x", http.StatusTeapot},
		{"app.example.com", "/api-docs", http.StatusMovedPermanently},
		{"developer.example.com", "/api/docs", http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		req.Host = c.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.wantCode {
			t.Errorf("%s%s: status want %d, got %d", c.host, c.path, c.wantCode, w.Code)
		}
	}
}

func TestDocSitesOnly_OffWhenUnset(t *testing.T) {
	h := docSitesOnly("https://app.example.com", docSites("", ""))(teapot())
	for _, path := range []string{"/api/projects", "/docs", "/api-docs"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "docs.example.com"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusTeapot {
			t.Errorf("%s with no sites must pass through, got %d", path, w.Code)
		}
	}
}

func TestHostOrigin(t *testing.T) {
	for _, c := range []struct{ base, host, want string }{
		{"https://app.example.com/", "docs.example.com", "https://docs.example.com"},
		{"http://localhost:9000", "Docs.Localhost", "http://docs.localhost:9000"},
		{"https://app.example.com", "", ""},
		{"not a url", "docs.example.com", ""},
	} {
		if got := hostOrigin(c.base, c.host); got != c.want {
			t.Errorf("hostOrigin(%q, %q) = %q, want %q", c.base, c.host, got, c.want)
		}
	}
}
