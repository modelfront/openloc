package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"openloc/internal/api"
	"openloc/internal/productdocs"
	"openloc/internal/publicapi"
)

// writeStaticDir lays down a minimal built-SPA directory:
//
//	index.html
//	assets/app-abc123.js   (a content-hashed chunk that DOES exist)
func writeStaticDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>app</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app-abc123.js"), []byte("export const x = 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func doGet(t *testing.T, r chi.Router, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSPAHandler_ServesExistingAsset(t *testing.T) {
	r := chi.NewRouter()
	addSPAHandler(r, writeStaticDir(t), nil)

	rec := doGet(t, r, "/assets/app-abc123.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("existing asset: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "text/html; charset=utf-8" {
		t.Fatalf("existing asset served as HTML (content-type %q)", ct)
	}
}

func TestSPAHandler_FallsBackToIndexForClientRoutes(t *testing.T) {
	r := chi.NewRouter()
	addSPAHandler(r, writeStaticDir(t), nil)

	// A client-side route (no file extension) must still get index.html so the
	// SPA router can take over — this is the whole point of the fallback.
	rec := doGet(t, r, "/projects/42/jobs/7/edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("client route: got %d, want 200 (index.html)", rec.Code)
	}
	if body := rec.Body.String(); body == "" || body[0] != '<' {
		t.Fatalf("client route did not receive index.html, got: %q", body)
	}
}

// Pins that a missing hashed asset returns 404, not index.html. After a deploy
// a browser holding the old index.html requests a chunk whose hash no longer
// exists; HTML under a .js URL fails as "Failed to fetch dynamically imported
// module", while a 404 lets the client fail fast and reload for the new bundle.
func TestSPAHandler_MissingAssetReturns404NotIndex(t *testing.T) {
	r := chi.NewRouter()
	addSPAHandler(r, writeStaticDir(t), nil)

	rec := doGet(t, r, "/assets/CATEditor-STALEHASH.js")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing hashed asset: got %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "text/html; charset=utf-8" {
		t.Fatalf("missing .js served as HTML (content-type %q) — browser would MIME-reject the module", ct)
	}
}

func TestSPAHandler_MissingCSSReturns404(t *testing.T) {
	r := chi.NewRouter()
	addSPAHandler(r, writeStaticDir(t), nil)

	rec := doGet(t, r, "/assets/index-STALEHASH.css")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing hashed css: got %d, want 404", rec.Code)
	}
}

// A browser holding a pre-deploy index.html requests bundle hashes that no
// longer exist on the freshly-deployed pod (they 404 — see the tests above).
// The trigger is index.html being cacheable: with no Cache-Control the browser
// applies heuristic freshness and serves a stale shell without revalidating.
// index.html (direct at "/" and via the SPA fallback) must carry no-cache so
// every navigation revalidates and a deploy is visible immediately.
func TestSPAHandler_IndexHTMLNotCached(t *testing.T) {
	r := chi.NewRouter()
	addSPAHandler(r, writeStaticDir(t), nil)

	for _, path := range []string{"/", "/projects/42/jobs/7/edit"} {
		rec := doGet(t, r, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d, want 200", path, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
			t.Fatalf("%s: Cache-Control = %q, want it to contain no-cache", path, cc)
		}
	}
}

// Content-hashed bundles are safe to cache forever: a new deploy changes the
// hash (hence the URL), so a stale entry is simply never requested again.
func TestSPAHandler_HashedAssetImmutable(t *testing.T) {
	r := chi.NewRouter()
	addSPAHandler(r, writeStaticDir(t), nil)

	rec := doGet(t, r, "/assets/app-abc123.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("asset: got %d, want 200", rec.Code)
	}
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=31536000") {
		t.Fatalf("asset Cache-Control = %q, want immutable + max-age=31536000", cc)
	}
}

// docsHostRouter serves the built SPA with the documentation sites on
// docs.example.com and developer.example.com.
func docsHostRouter(t *testing.T) (chi.Router, string) {
	t.Helper()
	dir := writeStaticDir(t)
	index := "<!doctype html><html><head><title>OpenLoc</title></head><body><div id=\"root\"></div><script src=\"/assets/app-abc123.js\"></script></body></html>"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	docs, err := productdocs.Default()
	if err != nil {
		t.Fatal(err)
	}
	apiDocs, err := productdocs.API()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := publicapi.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	addSPAHandler(r, dir, api.DocPages("https://app.example.com", "docs.example.com", "developer.example.com", docs, apiDocs, spec))
	return r, index
}

func getOn(r chi.Router, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// A crawler that runs no JavaScript reads each documentation page's title,
// description, canonical address and text in the HTML itself.
func TestSPAHandler_DocsHostRendersPages(t *testing.T) {
	r, _ := docsHostRouter(t)
	cases := []struct{ host, path, title, canonical string }{
		{"docs.example.com", "/", "<title>Docs · OpenLoc</title>", "https://docs.example.com/"},
		{"docs.example.com", "/getting-started", "<title>Getting started · Docs · OpenLoc</title>", "https://docs.example.com/getting-started"},
		{"docs.example.com", "/getting-started/first-project", " · Docs · OpenLoc</title>", "https://docs.example.com/getting-started/first-project"},
		{"developer.example.com", "/", "<title>API · OpenLoc</title>", "https://developer.example.com/"},
		{"developer.example.com", "/endpoints", "<title>API documentation · OpenLoc</title>", "https://developer.example.com/endpoints"},
	}
	for _, c := range cases {
		rec := getOn(r, c.host, c.path)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Errorf("%s%s: status %d", c.host, c.path, rec.Code)
		}
		for _, want := range []string{c.title, `<link rel="canonical" href="` + c.canonical + `" />`, `<meta name="description" content="`, `<div id="root"><div data-prerendered><h1>`, "<style>[data-prerendered] { display: none; }</style>", `<script src="/assets/app-abc123.js">`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s%s lacks %q:\n%.600s", c.host, c.path, want, body)
			}
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s%s: Cache-Control %q", c.host, c.path, cc)
		}
	}
}

// A path with no public page is a real 404, so search engines drop it; the
// app still loads and shows its own "not found".
func TestSPAHandler_DocsHostUnknownPageIs404(t *testing.T) {
	r, index := docsHostRouter(t)
	for _, c := range []struct{ host, path string }{
		{"docs.example.com", "/guides/no-such-page"},
		{"docs.example.com", "/endpoints"},
		{"developer.example.com", "/endpoints/noSuchOperation"},
	} {
		rec := getOn(r, c.host, c.path)
		if rec.Code != http.StatusNotFound || rec.Body.String() != index {
			t.Errorf("%s%s: %d %.200q", c.host, c.path, rec.Code, rec.Body.String())
		}
	}
}

// The app's own host and static files are served exactly as before.
func TestSPAHandler_OtherHostsUntouched(t *testing.T) {
	r, index := docsHostRouter(t)
	for _, path := range []string{"/", "/docs/getting-started"} {
		rec := getOn(r, "app.example.com", path)
		if rec.Code != http.StatusOK || rec.Body.String() != index {
			t.Errorf("app %s: %d %.200q", path, rec.Code, rec.Body.String())
		}
	}
	if rec := getOn(r, "docs.example.com", "/assets/app-abc123.js"); rec.Body.String() != "export const x = 1" {
		t.Errorf("asset on docs host: %q", rec.Body.String())
	}
}
