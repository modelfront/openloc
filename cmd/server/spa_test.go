package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
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
	addSPAHandler(r, writeStaticDir(t))

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
	addSPAHandler(r, writeStaticDir(t))

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
	addSPAHandler(r, writeStaticDir(t))

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
	addSPAHandler(r, writeStaticDir(t))

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
	addSPAHandler(r, writeStaticDir(t))

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
	addSPAHandler(r, writeStaticDir(t))

	rec := doGet(t, r, "/assets/app-abc123.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("asset: got %d, want 200", rec.Code)
	}
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=31536000") {
		t.Fatalf("asset Cache-Control = %q, want immutable + max-age=31536000", cc)
	}
}
