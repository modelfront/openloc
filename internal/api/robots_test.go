package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Search engines and AI crawlers may index the documentation hosts; every
// other host, the app and a self-hosted /docs included, stays out of reach.
func TestCrawlerPolicy(t *testing.T) {
	hosts := []string{"docs.example.com", "developer.example.com"}
	h := blockCrawlers(hosts)(robotsTxt(hosts))

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
	}
}

// With no documentation hosts configured, nothing is indexable.
func TestCrawlerPolicy_NoDocHosts(t *testing.T) {
	h := blockCrawlers([]string{"", ""})(robotsTxt([]string{"", ""}))
	req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	req.Host = ""
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Robots-Tag") == "" || !strings.Contains(rec.Body.String(), "Disallow: /\n") {
		t.Errorf("empty host got header %q, body %q", rec.Header().Get("X-Robots-Tag"), rec.Body.String())
	}
}
