package errtrack

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestTokenPathPrefixesCoverTokenRoutes keeps the redaction lists in step with
// the SPA pages that carry a token: a new /x/:token page in App.tsx, or a
// prefix added to only one of the Go and TS lists, fails here, before the
// token reaches a log line or a notice. The API routes are walked on the real
// router in internal/flowtest.
func TestTokenPathPrefixesCoverTokenRoutes(t *testing.T) {
	read := func(parts ...string) string {
		t.Helper()
		src, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
		if err != nil {
			t.Fatalf("read %s: %v", filepath.Join(parts...), err)
		}
		return string(src)
	}

	const token = "live-token"
	param := regexp.MustCompile(`:(\w+)`)
	routes := regexp.MustCompile(`path=\{?["'`+"`"+`]([^"'`+"`"+`]*:\w+[^"'`+"`"+`]*)["'`+"`"+`]`).
		FindAllStringSubmatch(read("web", "src", "App.tsx"), -1)
	checked := 0
	for _, m := range routes {
		carriesToken := false
		path := param.ReplaceAllStringFunc(m[1], func(p string) string {
			if isCredentialParam(p[1:]) {
				carriesToken = true
				return token
			}
			return "1"
		})
		if !carriesToken {
			continue
		}
		checked++
		if got := RedactTokenPaths(path); strings.Contains(got, token) {
			t.Errorf("App.tsx route %s keeps its token after RedactTokenPaths: %s", m[1], got)
		}
	}
	// The invite, password-reset and chart-share pages.
	if checked < 3 {
		t.Fatalf("found %d token routes in App.tsx, want at least 3; the parse is broken", checked)
	}

	m := regexp.MustCompile(`TOKEN_PATH_PREFIXES = \[([^\]]*)\]`).FindStringSubmatch(read("web", "src", "lib", "errtrack.ts"))
	if m == nil {
		t.Fatal("TOKEN_PATH_PREFIXES not found in web/src/lib/errtrack.ts")
	}
	var ts []string
	for _, q := range regexp.MustCompile(`['"]([^'"]*)['"]`).FindAllStringSubmatch(m[1], -1) {
		ts = append(ts, q[1])
	}
	goList := slices.Clone(tokenPathPrefixes)
	slices.Sort(ts)
	slices.Sort(goList)
	if !slices.Equal(ts, goList) {
		t.Errorf("TOKEN_PATH_PREFIXES %v differ from tokenPathPrefixes %v", ts, goList)
	}
}

// isCredentialParam reports whether a route parameter holds a credential
// (:token, :resetCode) rather than an id (:tokenId).
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
