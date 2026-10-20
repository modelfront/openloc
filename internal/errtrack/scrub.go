package errtrack

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/honeybadger-io/honeybadger-go"
)

// Redacted replaces a sensitive value. A marker rather than deletion so it's
// obvious the field existed and was removed on purpose.
const Redacted = "[REDACTED]"

// errRateLimited drops a notice. honeybadger treats any error returned from a
// BeforeNotify handler as "do not send".
var errRateLimited = errors.New("errtrack: rate limited")

// sensitiveKeyParts match case-insensitively as substrings, so "X-Api-Key",
// "ai_secrets_key" and "authorization" are all caught without enumerating
// every spelling.
var sensitiveKeyParts = []string{
	"authorization",
	"cookie",
	"token",
	"password",
	"secret",
	"api_key",
	"apikey",
	"private_key",
	"credential",
	"session",
}

// scrubNotice strips credentials and customer content from a notice before it
// leaves the process. Registered as a BeforeNotify handler by Init.
//
// This is not defensive over-engineering: honeybadger's Handler passes the
// *http.Request to Notify, and setHttpRequest copies every request header into
// CGIData, the full URL into URL, and every query parameter into Params. In
// this codebase that means:
//
//   - HTTP_COOKIE and HTTP_AUTHORIZATION carry a live session credential — auth
//     travels in an HttpOnly cookie and/or a Bearer header (web/src/api/request.ts).
//   - the query string carries customer segment text — the terminology lookup
//     (web/src/api/terminology.ts) puts source text in ?source_text=, so both Params
//     and URL leak translation content to a third party.
//   - the path can carry a live token — /api/invites/lookup/{token} itself, and
//     the Referer of any API call made from /invite/{token} or
//     /reset-password/{token}.
//
// Returns nil always: scrubbing must never itself drop a notice.
func scrubNotice(n *honeybadger.Notice) error {
	if n == nil {
		return nil
	}

	// Params is query/form data wholesale. There is no allowlist worth
	// maintaining here — source_text proves the query string is untrusted, and
	// a new leaky parameter would be added by someone who never reads this file.
	n.Params = nil

	// Keep the path for grouping and debugging, minus its token segments; drop
	// the query entirely. A panic message can quote the same URL.
	n.URL = ScrubURL(n.URL)
	n.ErrorMessage = ScrubURLsInText(n.ErrorMessage)

	for k, v := range n.CGIData {
		if isSensitiveKey(k) {
			n.CGIData[k] = Redacted
			continue
		}
		// HTTP_REFERER is a full URL: SPA queries carry free text (?q= is the
		// file-name search) and SPA paths carry tokens (/invite/{token}).
		if s, ok := v.(string); ok {
			n.CGIData[k] = ScrubHeaderValue(s)
		}
	}
	for k := range n.Context {
		if isSensitiveKey(k) {
			n.Context[k] = Redacted
		}
	}
	return nil
}

// urlWithQueryRE matches an http(s) URL that carries a query or fragment, for
// scrubbing URLs embedded in free text (error messages, breadcrumbs). Group 1
// is the query-less prefix.
var urlWithQueryRE = regexp.MustCompile(`(https?://[^\s?#"']+)[?#][^\s"']*`)

// tokenPathPrefixes are the path prefixes whose next segment is a live
// credential: the invite, password-reset and chart-share links, and the public
// invite lookup API. Twin: TOKEN_PATH_PREFIXES in web/src/lib/errtrack.ts.
// Guard tests keep both lists in step with the token routes in App.tsx and the
// API router, and both scrubbers run testdata/redaction_cases.json.
var tokenPathPrefixes = []string{"invite", "invites/lookup", "reset-password", "shared/charts"}

// tokenPathRE matches a prefix and its token segment; group 1 is the prefix.
// Case-insensitive, because React Router matches routes that way, and every
// prefix character may be percent-encoded: the router decodes /%69nvite/ to
// /invite/, while a recorded URL keeps the encoding.
var tokenPathRE = regexp.MustCompile(`(?i)(/(?:` + prefixPatterns(tokenPathPrefixes) + `)/)[^/?#\s"']+`)

// prefixPatterns joins the prefixes into one alternation in which each
// character matches literally or percent-encoded, in either letter case.
func prefixPatterns(prefixes []string) string {
	alts := make([]string, len(prefixes))
	for i, p := range prefixes {
		var b strings.Builder
		for _, c := range p {
			lower, upper := fmt.Sprintf("%%%02X", unicode.ToLower(c)), fmt.Sprintf("%%%02X", unicode.ToUpper(c))
			b.WriteString("(?:" + regexp.QuoteMeta(string(c)) + "|" + lower)
			if upper != lower {
				b.WriteString("|" + upper)
			}
			b.WriteString(")")
		}
		alts[i] = b.String()
	}
	return strings.Join(alts, "|")
}

// RedactTokenPaths replaces the token segment of every token-bearing path in s
// with Redacted and keeps everything else, query included. s may be a full
// URL, a relative path, or free text with several of either.
func RedactTokenPaths(s string) string {
	return tokenPathRE.ReplaceAllString(s, "${1}"+Redacted)
}

// ScrubURLsInText applies the URL rules to free text: every http(s) URL loses
// its query and fragment, and every token-bearing path, absolute or relative,
// loses its token segment. Error messages routinely embed request URLs
// ("GET https://…/api/tm/lookup?source_text=… failed"), and the query is
// customer content — the same boundary ScrubURL enforces on structured fields.
func ScrubURLsInText(s string) string {
	return RedactTokenPaths(urlWithQueryRE.ReplaceAllString(s, "$1"))
}

// Token shapes that end up in browser error messages: JWTs (the auth token)
// and long hex runs (API keys, digests). Twins of JWT_RE and LONG_HEX_RE in
// web/src/lib/errtrack.ts.
var (
	jwtRE     = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*`)
	longHexRE = regexp.MustCompile(`\b[A-Fa-f0-9]{32,}\b`)
)

// ScrubMessage is the browser's message rule, the twin of scrubMessage in
// web/src/lib/errtrack.ts: ScrubURLsInText, then every JWT and long hex run
// becomes Redacted. The browser-notice proxy applies it to every error message,
// error.causes[] included, which the browser SDK builds after its own scrub.
func ScrubMessage(s string) string {
	s = jwtRE.ReplaceAllString(ScrubURLsInText(s), Redacted)
	return longHexRE.ReplaceAllString(s, Redacted)
}

// ScrubHeaderValue applies the URL rules to a header value: a URL, or a bare
// path such as X-Original-Uri, loses its query, fragment, userinfo and token
// segments (ScrubURL); any other value loses its token segments.
func ScrubHeaderValue(s string) string {
	if strings.HasPrefix(s, "http") || strings.HasPrefix(s, "/") {
		return ScrubURL(s)
	}
	return RedactTokenPaths(s)
}

// ScrubURL returns the URL without its query string, fragment or userinfo, and
// with its token segment redacted (RedactTokenPaths). Exported for callers that
// scrub URLs outside this package's notice pipeline — the browser-notice proxy
// (internal/api) applies the same rule to client-built payloads, so a frontend
// regression can't leak ?source_text= or an invite token through the server.
func ScrubURL(raw string) string { return RedactTokenPaths(stripQuery(raw)) }

// stripQuery returns the URL without its query string or fragment. Falls back to
// a plain cut when the URL doesn't parse, so a malformed URL can't leak a query
// through the error path.
func stripQuery(raw string) string {
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil {
		u.RawQuery = ""
		u.Fragment = ""
		u.User = nil // credentials in userinfo, e.g. https://user:pass@host
		return u.String()
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i]
	}
	return raw
}

// IsSensitiveName reports whether a field, header or URL-parameter name looks
// like it holds a credential.
//
// Exported so that callers which BUILD fields can redact at the source instead
// of relying on scrubNotice to catch it downstream. That distinction matters:
// scrubNotice only runs on notices, while the HTTP middleware's fields also feed
// the ECS log line, which is retained in-cluster for 90 days. A credential
// belongs in neither.
func IsSensitiveName(name string) bool { return isSensitiveKey(name) }

func isSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, part := range sensitiveKeyParts {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

// --- backtraces -------------------------------------------------------------

// classesWithoutMeaningfulStack are the notices raised AFTER the failing frame
// has already unwound, so no captured stack can point at the code that broke.
//
//   - HTTPServerError comes from requestObservability's deferred func, which
//     runs once the handler has returned. The stack is our own middleware.
//   - WorkerTaskFailed comes from asynq's ErrorHandler, invoked after the task
//     handler returned. The stack is asynq's processor internals.
//
// Both would show a plausible-looking backtrace pointing at our own middleware,
// which misleads whoever is debugging. No backtrace is better than a wrong one.
//
// RequestPanic and StartupFailure are NOT in this set: those capture at the
// failure site, so their stacks are real and useful.
var classesWithoutMeaningfulStack = map[string]bool{
	ClassHTTPServerError: true,
	ClassWorkerTaskFail:  true,
}

// dropMisleadingBacktrace is a BeforeNotify handler. Returns nil always: it
// must never drop a notice.
func dropMisleadingBacktrace(n *honeybadger.Notice) error {
	if n == nil || !classesWithoutMeaningfulStack[n.ErrorClass] {
		return nil
	}
	// A single synthetic frame, rather than an empty slice: it reads as a
	// deliberate statement in the UI instead of an unexplained blank, and it
	// says where to look instead.
	n.Backtrace = []*honeybadger.Frame{{
		File:   "(no backtrace — raised after the failing frame unwound)",
		Number: "0",
		Method: "see error.message and the openloc.route context field",
	}}
	return nil
}

// --- rate limiting ----------------------------------------------------------

// limiterWindow and limiterBurst cap how many notices one fingerprint may send
// per window.
//
// Honeybadger counts every occurrence with no server-side dedup, and once an
// account passes 100% of quota it throttles to one notice per minute per project
// for the remainder of the calendar month. OpenLoc shares the org account, so
// an unthrottled retry storm — a pipeline worker failing across a segment batch
// on asynq's 1m/2m/4m backoff — would degrade every other service's visibility,
// not just ours. This is the in-process half of that defence; the project-level
// Throttle setting is the other.
const (
	limiterWindow = time.Minute
	limiterBurst  = 5
)

// maxTrackedFingerprints bounds memory. A pathological Entity value (a unique
// ID per occurrence) would otherwise grow this map without limit; when the cap
// is hit the map resets rather than being evicted one key at a time, which
// briefly allows a burst but can never leak.
const maxTrackedFingerprints = 4096

type rateLimiter struct {
	mu     sync.Mutex
	counts map[string]*window
	now    func() time.Time // injectable for tests
}

type window struct {
	start time.Time
	count int
}

var defaultLimiter = newRateLimiter(time.Now)

func newRateLimiter(now func() time.Time) *rateLimiter {
	return &rateLimiter{counts: make(map[string]*window), now: now}
}

// reset clears all accounting. Called from Init so reconfiguring starts with a
// clean slate rather than inheriting the previous configuration's counts.
func (l *rateLimiter) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.counts = make(map[string]*window)
}

// allow is a BeforeNotify handler: returning an error drops the notice.
func (l *rateLimiter) allow(n *honeybadger.Notice) error {
	if n == nil {
		return nil
	}
	// Group by fingerprint when we set one (we always do via Capture), else by
	// class, so a notice from the SDK's own Handler path is still bounded.
	key := n.Fingerprint
	if key == "" {
		key = n.ErrorClass
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if len(l.counts) >= maxTrackedFingerprints {
		l.counts = make(map[string]*window)
	}

	w, ok := l.counts[key]
	if !ok || now.Sub(w.start) >= limiterWindow {
		l.counts[key] = &window{start: now, count: 1}
		return nil
	}
	w.count++
	if w.count > limiterBurst {
		return errRateLimited
	}
	return nil
}
