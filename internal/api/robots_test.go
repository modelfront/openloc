package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/productdocs"
	"openloc/internal/publicapi"
)

// Search engines and AI crawlers may index the documentation hosts; every
// other host, the app and a self-hosted /docs included, stays out of reach.
func TestCrawlerPolicy(t *testing.T) {
	hosts := []string{"docs.example.com", "developer.example.com"}
	h := blockCrawlers(hosts)(robotsTxt("https://app.example.com", hosts))

	cases := []struct {
		host      string
		indexable bool
	}{
		{"docs.example.com", true},
		{"DEVELOPER.example.com:443", true},
		{"app.example.com", false},
		{"localhost:8080", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
		req.Host = c.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		body := rec.Body.String()

		if got := rec.Header().Get("X-Robots-Tag"); (got == "") != c.indexable {
			t.Errorf("%s: X-Robots-Tag = %q, indexable = %v", c.host, got, c.indexable)
		}
		if blocked := strings.Contains(body, "Disallow: /\n"); blocked == c.indexable {
			t.Errorf("%s: robots.txt = %q, indexable = %v", c.host, body, c.indexable)
		}
		if c.indexable && !strings.Contains(body, "User-agent: *\nAllow: /\n") {
			t.Errorf("%s: robots.txt = %q, want everyone allowed", c.host, body)
		}
		if hasSitemap := strings.Contains(body, "\nSitemap: https://"); hasSitemap != c.indexable {
			t.Errorf("%s: robots.txt = %q, want a sitemap line only when indexable", c.host, body)
		}
	}
}

// With no documentation hosts configured, nothing is indexable.
func TestCrawlerPolicy_NoDocHosts(t *testing.T) {
	h := blockCrawlers([]string{"", ""})(robotsTxt("https://app.example.com", []string{"", ""}))
	req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	req.Host = ""
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Robots-Tag") == "" || !strings.Contains(rec.Body.String(), "Disallow: /\n") {
		t.Errorf("empty host got header %q, body %q", rec.Header().Get("X-Robots-Tag"), rec.Body.String())
	}
}

// Each documentation host names its own sitemap, which lists its public
// pages; no other host has one.
func TestSitemap(t *testing.T) {
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
	base := "https://app.example.com"
	hosts := []string{"docs.example.com", "developer.example.com"}
	pages := DocPages(base, hosts[0], hosts[1], docs, apiDocs, spec)
	robots, sitemap := robotsTxt(base, hosts), sitemapXML(pages)

	get := func(h http.Handler, host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, c := range []struct{ host, page string }{
		{"docs.example.com", "https://docs.example.com/getting-started/"},
		{"developer.example.com", "https://developer.example.com/endpoints/"},
	} {
		if body := get(robots, c.host, "/robots.txt").Body.String(); !strings.HasSuffix(body, "\nSitemap: https://"+c.host+"/sitemap.xml\n") {
			t.Errorf("%s robots.txt = %q", c.host, body)
		}
		rec := get(sitemap, c.host, "/sitemap.xml")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/xml; charset=utf-8" ||
			!strings.Contains(rec.Body.String(), "<loc>"+c.page) {
			t.Errorf("%s sitemap: %d %q\n%s", c.host, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
	}
	if rec := get(sitemap, "app.example.com", "/sitemap.xml"); rec.Code != http.StatusNotFound {
		t.Errorf("app host sitemap: %d", rec.Code)
	}
	if rec := get(sitemapXML(nil), "docs.example.com", "/sitemap.xml"); rec.Code != http.StatusNotFound {
		t.Errorf("no pages: %d", rec.Code)
	}
}
