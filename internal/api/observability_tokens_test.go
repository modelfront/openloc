package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"openloc/internal/errtrack"
	"openloc/internal/errtrack/errtracktest"
	"openloc/internal/logging"
)

// TestSPATokenPathNeverRecorded — the SPA catch-all serves the invite and
// password-reset pages, and every page load writes an access-log line. The
// token sits in chi's "*" param, which no param-name rule can recognize. A
// HEAD request (email link scanners send them) gets a 405 from the GET-only
// catch-all, so no route pattern matches and the route field falls back to
// the path.
func TestSPATokenPathNeverRecorded(t *testing.T) {
	const token = "live-token-do-not-leak"

	for _, tc := range []struct {
		name, method, path string
		field, want        string
	}{
		{"invite page load", "GET", "/invite/" + token, logging.KeyURLPath, "/invite/" + errtrack.Redacted},
		{"password reset page load", "GET", "/reset-password/" + token, logging.KeyURLPath, "/reset-password/" + errtrack.Redacted},
		{"link scanner HEAD", "HEAD", "/invite/" + token, logging.KeyRoute, "/invite/" + errtrack.Redacted},
		{"chart share page load", "GET", "/shared/charts/" + token, logging.KeyURLPath, "/shared/charts/" + errtrack.Redacted},
		{"chart share API", "GET", "/api/shared/charts/" + token + "/data", logging.KeyURLPath, "/api/shared/charts/" + errtrack.Redacted + "/data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errtracktest.Install(t)
			logs := captureLogs(t)

			r := testRouter(t, func(r chi.Router) {
				r.Get("/*", func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				})
			})
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))

			lines := decodeLines(t, logs)
			if len(lines) != 1 {
				t.Fatalf("got %d log lines, want 1:\n%s", len(lines), logs.String())
			}
			if got := lines[0][tc.field]; got != tc.want {
				t.Errorf("%s = %v, want %q", tc.field, got, tc.want)
			}
			if strings.Contains(logs.String(), token) {
				t.Errorf("access log leaks the token:\n%s", logs.String())
			}
		})
	}
}
