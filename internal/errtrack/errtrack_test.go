package errtrack_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/honeybadger-io/honeybadger-go"
	"openloc/internal/errtrack"
	"openloc/internal/errtrack/errtracktest"
	"openloc/internal/logging"
)

const (
	secretSegment = "CONFIDENTIAL-PATENT-TEXT"
	sessionToken  = "eyJhbGciOiJIUzI1NiJ9.SESSIONVALUE"
)

// leakyRequest mimics a real authenticated request to the terminology lookup
// endpoint: a session cookie, a Bearer header, and customer segment text in the
// query string (the terminology lookup in web/src/api/terminology.ts puts source text there).
func leakyRequest() *http.Request {
	r := httptest.NewRequest("GET",
		"/api/terminology/lookup?template_id=1&source_text="+secretSegment, nil)
	r.Header.Set("Cookie", "token="+sessionToken)
	r.Header.Set("Authorization", "Bearer "+sessionToken)
	r.Header.Set("X-Api-Key", "some-api-key")
	// Referer is a full URL whose query carries free text in this SPA
	// (?q= is the project list's file-name search).
	r.Header.Set("Referer", "https://app.example.com/projects?q="+secretSegment)
	return r
}

// TestSDKLeaksRequestDataWithoutScrubbing is the anti-tautology half of the
// scrubbing tests. It asserts the leak is REAL by driving the SDK's own path
// with no BeforeNotify handlers registered — an isolated client, not the
// package-level one. Without this, TestCaptureScrubsRequestData could pass
// simply because the SDK never populated those fields, and we'd have a
// reassuring green test protecting nothing.
//
// If a future SDK version stops copying cookies into CGIData, this test fails
// and tells us the scrubber's justification changed.
func TestSDKLeaksRequestDataWithoutScrubbing(t *testing.T) {
	rec := &errtracktest.Recorder{}
	client := honeybadger.New(honeybadger.Configuration{
		APIKey:  "test-key",
		Sync:    true,
		Backend: rec,
	})
	if _, err := client.Notify(errors.New("boom"), leakyRequest()); err != nil {
		t.Fatalf("notify: %v", err)
	}

	n := rec.One(t)
	if n.CGIData["HTTP_COOKIE"] != "token="+sessionToken {
		t.Errorf("expected the raw cookie in CGIData, got %v — the leak this "+
			"scrubber defends against may no longer exist", n.CGIData["HTTP_COOKIE"])
	}
	if n.CGIData["HTTP_AUTHORIZATION"] != "Bearer "+sessionToken {
		t.Errorf("expected the raw Authorization header, got %v", n.CGIData["HTTP_AUTHORIZATION"])
	}
	if !strings.Contains(n.URL, secretSegment) {
		t.Errorf("expected segment text in URL, got %q", n.URL)
	}
	if got := fmt.Sprint(n.Params); !strings.Contains(got, secretSegment) {
		t.Errorf("expected segment text in Params, got %v", got)
	}
}

// TestCaptureScrubsRequestData is the other half: the same leaky request through
// the package-level client, which Init has fitted with the scrubber.
func TestCaptureScrubsRequestData(t *testing.T) {
	rec := errtracktest.Install(t)

	// Route through the SDK's Handler path, which is what passes the request to
	// Notify and thus produces the leak in the first place.
	h := honeybadger.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	}))
	func() {
		defer func() { _ = recover() }() // Handler re-panics by design
		h.ServeHTTP(httptest.NewRecorder(), leakyRequest())
	}()

	n := rec.One(t)

	if got := n.CGIData["HTTP_COOKIE"]; got != "[REDACTED]" {
		t.Errorf("HTTP_COOKIE = %v, want [REDACTED] — a live session credential would leave the cluster", got)
	}
	if got := n.CGIData["HTTP_AUTHORIZATION"]; got != "[REDACTED]" {
		t.Errorf("HTTP_AUTHORIZATION = %v, want [REDACTED]", got)
	}
	if got := n.CGIData["HTTP_X_API_KEY"]; got != "[REDACTED]" {
		t.Errorf("HTTP_X_API_KEY = %v, want [REDACTED]", got)
	}
	if n.Params != nil {
		t.Errorf("Params = %v, want nil", n.Params)
	}
	if strings.Contains(n.URL, secretSegment) {
		t.Errorf("URL still contains customer segment text: %q", n.URL)
	}
	if !strings.Contains(n.URL, "/api/terminology/lookup") {
		t.Errorf("URL = %q, want the path preserved for debugging", n.URL)
	}
	if got := n.CGIData["HTTP_REFERER"]; got != "https://app.example.com/projects" {
		t.Errorf("HTTP_REFERER = %v, want query stripped — the referer URL carries free-text search", got)
	}

	// Belt and braces: the whole serialized notice must not mention either secret.
	blob := fmt.Sprintf("%+v", *n)
	for _, forbidden := range []string{secretSegment, sessionToken} {
		if strings.Contains(blob, forbidden) {
			t.Errorf("notice still contains %q somewhere:\n%s", forbidden, blob)
		}
	}
}

func TestCaptureRedactsSensitiveContextKeys(t *testing.T) {
	rec := errtracktest.Install(t)

	errtrack.Capture(context.Background(), errors.New("boom"), errtrack.CaptureOptions{
		Class:     errtrack.ClassHTTPServerError,
		Subsystem: "http",
		Event:     "test",
		Extra: map[string]any{
			"api_token":      "abc",
			"user_password":  "hunter2",
			"AI_SECRETS_KEY": "xyz",
			"job_id":         "42", // benign, must survive
		},
	})

	n := rec.One(t)
	for _, k := range []string{"api_token", "user_password", "AI_SECRETS_KEY"} {
		if n.Context[k] != "[REDACTED]" {
			t.Errorf("context[%s] = %v, want [REDACTED]", k, n.Context[k])
		}
	}
	if n.Context["job_id"] != "42" {
		t.Errorf("context[job_id] = %v, want it left alone", n.Context["job_id"])
	}
}

func TestCaptureAttachesRevisionAndScopeFields(t *testing.T) {
	rec := errtracktest.Install(t)

	ctx, scope := logging.WithScope(context.Background())
	scope.Set(logging.TenantID(7), logging.UserID(9), logging.Route("/api/projects/{projectID}"))

	errtrack.Capture(ctx, errors.New("boom"), errtrack.CaptureOptions{
		Class: errtrack.ClassHTTPServerError, Subsystem: "http", Event: "project-list",
	})

	n := rec.One(t)
	if n.Context[errtrack.ContextRevision] != "testrev" {
		t.Errorf("revision = %v, want testrev — deploy correlation depends on it", n.Context[errtrack.ContextRevision])
	}
	if n.Context[logging.KeyTenantID] != "7" {
		t.Errorf("tenant = %v, want 7 (logging.Scope should flow into notice context)", n.Context[logging.KeyTenantID])
	}
	if n.Context[logging.KeyRoute] != "/api/projects/{projectID}" {
		t.Errorf("route = %v", n.Context[logging.KeyRoute])
	}
	// Honeybadger aggregates on user_id specifically, so logging's user.id must be
	// mirrored into the key its UI understands.
	if n.Context[errtrack.ContextUserID] != "9" {
		t.Errorf("user_id = %v, want 9", n.Context[errtrack.ContextUserID])
	}
}

// TestUserEmailInNoticeContext — the Scope's dedicated email slot must reach
// the notice as Honeybadger's user_email convention,
// context-only: never the message, and the scrubber must leave it
// alone (it is sent deliberately).
func TestUserEmailInNoticeContext(t *testing.T) {
	rec := errtracktest.Install(t)

	ctx, scope := logging.WithScope(context.Background())
	scope.Set(logging.TenantID(7), logging.UserID(9))
	scope.SetUserEmail("jane@example.com")

	errtrack.Capture(ctx, errors.New("boom"), errtrack.CaptureOptions{
		Class: errtrack.ClassHTTPServerError, Subsystem: "http", Event: "test",
	})

	n := rec.One(t)
	if n.Context[errtrack.ContextUserEmail] != "jane@example.com" {
		t.Errorf("user_email = %v, want the scope's email (scrubber must not eat it)", n.Context[errtrack.ContextUserEmail])
	}
	if strings.Contains(n.ErrorMessage, "jane@example.com") {
		t.Errorf("ErrorMessage = %q — the email belongs in the context, never the message", n.ErrorMessage)
	}
}

func TestCaptureSetsExplicitErrorClass(t *testing.T) {
	rec := errtracktest.Install(t)
	errtrack.Capture(context.Background(), fmt.Errorf("wrapped: %w", errors.New("inner")),
		errtrack.CaptureOptions{Class: errtrack.ClassWorkerTaskFail, Subsystem: "worker", Event: "translate"})

	n := rec.One(t)
	// Without an explicit class this would be *fmt.wrapError or *errors.errorString
	// for nearly every error in the codebase, collapsing the whole app into one issue.
	if n.ErrorClass != errtrack.ClassWorkerTaskFail {
		t.Errorf("ErrorClass = %q, want %q", n.ErrorClass, errtrack.ClassWorkerTaskFail)
	}
	if strings.Contains(n.ErrorClass, "errorString") || strings.Contains(n.ErrorClass, "wrapError") {
		t.Errorf("ErrorClass fell back to the Go type: %q", n.ErrorClass)
	}
}

func TestCaptureMessageIncludesCauseAndSentence(t *testing.T) {
	rec := errtracktest.Install(t)
	errtrack.Capture(context.Background(), errors.New("connection refused"),
		errtrack.CaptureOptions{Class: errtrack.ClassStartupFailure, Message: "database unreachable",
			Subsystem: "startup", Event: "db-connect"})

	n := rec.One(t)
	if !strings.Contains(n.ErrorMessage, "database unreachable") || !strings.Contains(n.ErrorMessage, "connection refused") {
		t.Errorf("ErrorMessage = %q, want both the human sentence and the cause", n.ErrorMessage)
	}
}

// TestCaptureNilError — some call sites guard on `!ok` with no error value, so
// nil must be survivable.
func TestCaptureNilError(t *testing.T) {
	rec := errtracktest.Install(t)
	errtrack.Capture(context.Background(), nil, errtrack.CaptureOptions{
		Class: errtrack.ClassHTTPServerError, Message: "invariant violated",
		Subsystem: "http", Event: "bad-state",
	})

	n := rec.One(t)
	if !strings.Contains(n.ErrorMessage, "invariant violated") {
		t.Errorf("ErrorMessage = %q, want the message for a nil error", n.ErrorMessage)
	}
}

// TestBacktracePointsAtCaller — without the stacked-error trick every notice's
// top frame would be inside internal/errtrack, making backtraces useless.
//
// Deliberately uses StartupFailure, not HTTPServerError: the latter is in
// classesWithoutMeaningfulStack (see scrub.go), because it is raised after the
// failing frame has unwound. Picking a suppressed class here would test the
// suppression instead of the mechanism.
func TestBacktracePointsAtCaller(t *testing.T) {
	rec := errtracktest.Install(t)
	errtrack.Capture(context.Background(), errors.New("boom"), errtrack.CaptureOptions{
		Class: errtrack.ClassStartupFailure, Subsystem: "startup", Event: "e",
	})

	n := rec.One(t)
	if len(n.Backtrace) == 0 {
		t.Fatal("empty backtrace")
	}
	top := n.Backtrace[0]
	if !strings.Contains(top.Method, "TestBacktracePointsAtCaller") {
		t.Errorf("top frame = %s (%s:%s), want this test function — backtraces "+
			"must point at the real call site, not inside errtrack",
			top.Method, top.File, top.Number)
	}
}

func TestFingerprintGroupsByTenantAndEvent(t *testing.T) {
	a := errtrack.Fingerprint("http", "HTTPServerError", "1", "", "/api/projects")
	again := errtrack.Fingerprint("http", "HTTPServerError", "1", "", "/api/projects")
	otherTenant := errtrack.Fingerprint("http", "HTTPServerError", "2", "", "/api/projects")
	otherRoute := errtrack.Fingerprint("http", "HTTPServerError", "1", "", "/api/jobs")

	if a != again {
		t.Errorf("fingerprint not stable: %q vs %q", a, again)
	}
	if a == otherTenant {
		t.Errorf("different tenants share a fingerprint (%q) — one tenant's "+
			"failures would mask another's", a)
	}
	if a == otherRoute {
		t.Errorf("different routes share a fingerprint: %q", a)
	}
}

// TestFingerprintEmptyPartsDoNotCollapse — if blanks were joined bare, an
// unknown tenant would produce "http--..." and two structurally different
// groups could merge into one.
func TestFingerprintEmptyPartsDoNotCollapse(t *testing.T) {
	got := errtrack.Fingerprint("", "", "", "", "")
	if strings.Contains(got, "--") {
		t.Errorf("fingerprint %q has empty parts collapsed into adjacent hyphens", got)
	}
	if !strings.Contains(got, "unknown") {
		t.Errorf("fingerprint %q should mark missing parts explicitly", got)
	}
}

// TestDisabledByDefault — a binary that merely links errtrack must not report.
// The SDK reads HONEYBADGER_API_KEY from the environment with the real HTTP
// backend at package init, so without this gate any test binary on a machine
// with that variable set would send live notices to the shared org account.
func TestDisabledByDefault(t *testing.T) {
	rec := errtracktest.Install(t)
	errtrack.Disable()

	errtrack.Capture(context.Background(), errors.New("boom"), errtrack.CaptureOptions{
		Class: errtrack.ClassHTTPServerError, Subsystem: "http", Event: "e",
	})

	if rec.Len() != 0 {
		t.Errorf("captured %d notices while disabled, want 0", rec.Len())
	}
	if errtrack.Enabled() {
		t.Error("Enabled() true after Disable()")
	}
}

// TestEmptyAPIKeyDisables — "no key configured" must mean off, which cannot be
// expressed through the SDK's config: Configuration.update only overwrites
// non-zero fields, so an empty APIKey leaves any environment-derived key intact.
func TestEmptyAPIKeyDisables(t *testing.T) {
	t.Cleanup(errtrack.Disable)
	errtrack.Init(errtrack.Options{APIKey: "", Environment: "test"})

	if errtrack.Enabled() {
		t.Error("Enabled() true with an empty API key")
	}
}

func TestRateLimiterCapsRepeatedFingerprint(t *testing.T) {
	rec := errtracktest.Install(t)

	const attempts = 50
	for range attempts {
		errtrack.Capture(context.Background(), errors.New("boom"), errtrack.CaptureOptions{
			Class: errtrack.ClassWorkerTaskFail, Subsystem: "worker", Event: "same-task",
		})
	}

	// An asynq retry storm across a segment batch can produce thousands of
	// identical failures; unthrottled they'd burn the shared org quota, after
	// which Honeybadger throttles the whole project to 1/min for the month.
	if rec.Len() >= attempts {
		t.Errorf("sent %d of %d notices — rate limiter not applied", rec.Len(), attempts)
	}
	if rec.Len() == 0 {
		t.Error("rate limiter dropped everything; the first occurrences must get through")
	}
}

func TestRateLimiterIsPerFingerprint(t *testing.T) {
	rec := errtracktest.Install(t)

	// Distinct tenants ⇒ distinct fingerprints ⇒ one noisy tenant must not
	// silence another's errors.
	for i := range 20 {
		ctx, scope := logging.WithScope(context.Background())
		scope.Set(logging.TenantID(int64(i)))
		errtrack.Capture(ctx, errors.New("boom"), errtrack.CaptureOptions{
			Class: errtrack.ClassHTTPServerError, Subsystem: "http", Event: "same-route",
		})
	}
	if rec.Len() != 20 {
		t.Errorf("sent %d/20 notices across distinct tenants — the limiter is "+
			"grouping tenants together", rec.Len())
	}
}

func TestScrubHandlesEmptyNotice(t *testing.T) {
	rec := errtracktest.Install(t)
	// No request, no context, no message — must not panic anywhere.
	errtrack.Capture(context.Background(), nil, errtrack.CaptureOptions{})
	if rec.Len() != 1 {
		t.Errorf("expected 1 notice, got %d", rec.Len())
	}
}

func TestEntityID(t *testing.T) {
	if got := errtrack.EntityID(42); got != "42" {
		t.Errorf("EntityID(42) = %q, want %q", got, "42")
	}
}

// TestMisleadingBacktraceSuppressed — an HTTPServerError notice is raised from
// requestObservability's deferred func, after the failing handler has already
// returned. Any captured stack therefore points at our own middleware, which
// misleads whoever is debugging.
func TestMisleadingBacktraceSuppressed(t *testing.T) {
	rec := errtracktest.Install(t)
	errtrack.Capture(context.Background(), nil, errtrack.CaptureOptions{
		Class: errtrack.ClassHTTPServerError, Message: "HTTP 500 on POST /api/jobs/{jobID}/mt-apply",
		Subsystem: "http", Event: "POST /api/jobs/{jobID}/mt-apply",
	})

	n := rec.One(t)
	for _, f := range n.Backtrace {
		if strings.Contains(f.File, "observability.go") || strings.Contains(f.Method, "requestObservability") {
			t.Errorf("backtrace still points at the middleware: %s (%s:%s)", f.Method, f.File, f.Number)
		}
	}
	if len(n.Backtrace) != 1 {
		t.Fatalf("want exactly 1 explanatory frame, got %d", len(n.Backtrace))
	}
	if !strings.Contains(n.Backtrace[0].File, "no backtrace") {
		t.Errorf("frame should explain the absence, got %q", n.Backtrace[0].File)
	}
}

// TestWorkerBacktraceSuppressed — same reasoning: asynq invokes ErrorHandler
// after the task handler returned, so the stack is asynq's processor internals.
func TestWorkerBacktraceSuppressed(t *testing.T) {
	rec := errtracktest.Install(t)
	errtrack.Capture(context.Background(), errors.New("boom"), errtrack.CaptureOptions{
		Class: errtrack.ClassWorkerTaskFail, Subsystem: "worker", Event: "pipeline:translate",
	})
	if got := len(rec.One(t).Backtrace); got != 1 {
		t.Errorf("want the single explanatory frame, got %d", got)
	}
}
