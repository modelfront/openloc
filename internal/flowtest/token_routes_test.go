package flowtest

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"openloc/internal/errtrack"
	"openloc/internal/errtrack/errtracktest"
	"openloc/internal/logging"
	"openloc/internal/testutil"
)

// TestIntegrationTokenRoutesNeverRecordTheirToken walks the real router. Every
// route with a credential-like parameter ({token}) must sit under a prefix
// errtrack.RedactTokenPaths knows, and a request to it must leave the token in
// no log line and no error notice. A new token route — under /api or not,
// inside r.Route or not — fails here until its prefix joins the redaction
// lists. The SPA's /x/:token pages are checked against App.tsx in
// internal/errtrack.
func TestIntegrationTokenRoutesNeverRecordTheirToken(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	router := testutil.NewTestRouter(pool)
	notices := errtracktest.Install(t)
	var logs bytes.Buffer
	prev := logging.L()
	logging.SetLogger(logging.New(logging.Options{ECS: true, Writer: &logs, Level: slog.LevelDebug, Version: "testrev"}))
	t.Cleanup(func() { logging.SetLogger(prev) })

	const token = "live-token-do-not-leak"
	param := regexp.MustCompile(`\{(\w+)(?::[^}]*)?\}`)
	found := 0
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		carriesToken := false
		path := param.ReplaceAllStringFunc(route, func(m string) string {
			if isCredentialParam(param.FindStringSubmatch(m)[1]) {
				carriesToken = true
				return token
			}
			return "1"
		})
		if !carriesToken {
			return nil
		}
		found++
		if got := errtrack.RedactTokenPaths(path); strings.Contains(got, token) {
			t.Errorf("%s %s keeps its token after RedactTokenPaths: %s", method, route, got)
		}
		logs.Reset()
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
		if logs.Len() == 0 {
			t.Errorf("%s %s wrote no access log line; the leak check saw nothing", method, route)
		}
		if strings.Contains(logs.String(), token) {
			t.Errorf("%s %s writes its token to the log:\n%s", method, route, logs.String())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The invite lookup and the two chart-share routes.
	if found < 3 {
		t.Fatalf("found %d token routes, want at least 3; the walk did not see the router", found)
	}
	for _, n := range notices.Notices() {
		raw, err := json.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), token) {
			t.Errorf("an error notice carries the token: %s", raw)
		}
	}
}

// isCredentialParam reports whether a route parameter holds a credential
// ({token}, {resetCode}) rather than an id ({tokenID}).
func isCredentialParam(name string) bool {
	n := strings.ToLower(name)
	if strings.HasSuffix(n, "id") {
		return false
	}
	for _, part := range []string{"token", "code", "secret", "key", "signature"} {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}
