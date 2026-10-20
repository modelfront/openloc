package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/honeybadger-io/honeybadger-go"
	"openloc/internal/auth"
	"openloc/internal/errtrack"
	"openloc/internal/errtrack/errtracktest"
	"openloc/internal/logging"
)

// These tests run against a synthetic router carrying the same middleware stack
// as NewRouter, rather than through the real one.
//
// The unit under test is the middleware, and a synthetic router can force a 500,
// a panic, a 404 and a 409 directly. Going through NewRouter would need a
// testcontainers Postgres and then some contrived way to make a real handler
// fail, which would test the handler's failure mode rather than the middleware's
// reaction to it.
//
// The stack below must mirror NewRouter's ordering — see api.go. No test
// compares the two, so a change to either must be copied to the other by hand.
func testRouter(t *testing.T, register func(r chi.Router)) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(requestObservability)
	r.Use(honeybadger.Handler)
	register(r)
	return r
}

// captureLogs installs an ECS logger writing to a buffer, so request logging can
// be asserted without polluting test output.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := logging.L()
	logging.SetLogger(logging.New(logging.Options{ECS: true, Writer: &buf, Level: slog.LevelDebug, Version: "testrev"}))
	t.Cleanup(func() { logging.SetLogger(prev) })
	return &buf
}

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, line)
		}
		out = append(out, m)
	}
	return out
}

func TestServerErrorReported(t *testing.T) {
	rec := errtracktest.Install(t)
	captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Get("/api/projects/{projectID}", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
	})

	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest("GET", "/api/projects/42", nil))

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.Code)
	}
	n := rec.One(t)

	// The route PATTERN, not the path: /api/projects/42 would fingerprint every
	// project ID as a separate issue.
	if got := n.Context[logging.KeyRoute]; got != "/api/projects/{projectID}" {
		t.Errorf("route = %v, want the chi pattern (not the concrete path)", got)
	}
	if got := n.Context[logging.KeyHTTPStatus]; got != int64(500) && got != float64(500) && got != 500 {
		t.Errorf("http status = %#v, want 500", got)
	}
	if n.ErrorClass != "HTTPServerError" {
		t.Errorf("ErrorClass = %q, want HTTPServerError", n.ErrorClass)
	}
}

// TestClientErrorsNotReported — a missing resource or an optimistic-locking
// conflict is normal control flow. Reporting them would bury real incidents.
func TestClientErrorsNotReported(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"404 not found", http.StatusNotFound},
		{"409 conflict", http.StatusConflict},
		{"400 bad request", http.StatusBadRequest},
		{"403 forbidden", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := errtracktest.Install(t)
			captureLogs(t)

			r := testRouter(t, func(r chi.Router) {
				r.Get("/api/thing", func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
				})
			})
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/thing", nil))

			if rec.Len() != 0 {
				t.Errorf("status %d produced %d notices, want 0", tc.status, rec.Len())
			}
		})
	}
}

// TestClientDisconnect500FilesNoNotice — a browser refresh mid-request cancels
// r.Context(); whatever the handler was doing then fails and typically surfaces
// as a 500. That 500 is an artifact of the disconnect, not a server fault, and
// reporting it pages someone for every refresh during a long synchronous run
// (for example a refresh during Apply MT). The ECS log line must remain — it is
// the record that the request died — only the notice is suppressed.
func TestClientDisconnect500FilesNoNotice(t *testing.T) {
	rec := errtracktest.Install(t)
	buf := captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Post("/api/jobs/{jobID}/mt-apply", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
	})

	// net/http cancels the request context when the client goes away; a
	// pre-canceled context is the test stand-in for that.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("POST", "/api/jobs/42/mt-apply", nil).WithContext(ctx)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if rec.Len() != 0 {
		t.Errorf("a 500 on a disconnected client produced %d notices, want 0", rec.Len())
	}
	lines := decodeLines(t, buf)
	if len(lines) == 0 {
		t.Fatal("the request must still be logged")
	}
	last := lines[len(lines)-1]
	if got := last[logging.KeyHTTPStatus]; got != float64(500) {
		t.Errorf("logged status = %#v, want 500 — the log line is the record of the death", got)
	}
	if got := last[logging.KeyLogLevel]; got != "error" {
		t.Errorf("log.level = %v, want error", got)
	}
}

// TestPanicReportedExactlyOnce is the ordering test.
//
// honeybadger.Handler reports the panic and re-panics; Recoverer then turns it
// into a 500. If requestObservability were registered OUTSIDE Recoverer it would
// observe that synthesised 500 as an ordinary response and report a second time.
// This test fails if that ordering regresses.
func TestPanicReportedExactlyOnce(t *testing.T) {
	rec := errtracktest.Install(t)
	captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Get("/api/boom", func(http.ResponseWriter, *http.Request) {
			panic("kaboom")
		})
	})

	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest("GET", "/api/boom", nil))

	// Recoverer must still own the response.
	if res.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 — Recoverer should still produce the response", res.Code)
	}
	if rec.Len() != 1 {
		t.Fatalf("got %d notices for one panic, want exactly 1", rec.Len())
	}
	if msg := rec.One(t).ErrorMessage; !strings.Contains(msg, "kaboom") {
		t.Errorf("ErrorMessage = %q, want the panic value", msg)
	}
}

func TestPanicLoggedAs500(t *testing.T) {
	errtracktest.Install(t)
	buf := captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Get("/api/boom", func(http.ResponseWriter, *http.Request) { panic("kaboom") })
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/boom", nil))

	lines := decodeLines(t, buf)
	if len(lines) == 0 {
		t.Fatal("no log output for a panicking request")
	}
	last := lines[len(lines)-1]
	// Recoverer writes the 500 after our defer unwinds, so ww.Status() is still
	// 0 at log time; the middleware substitutes what the client will receive.
	if got := last[logging.KeyHTTPStatus]; got != float64(500) {
		t.Errorf("logged status = %#v, want 500", got)
	}
	if got := last[logging.KeyLogLevel]; got != "error" {
		t.Errorf("log.level = %v, want error", got)
	}
}

func TestIdentityAttachedToNotice(t *testing.T) {
	rec := errtracktest.Install(t)
	captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Group(func(r chi.Router) {
			// Stand in for auth.Middleware / auth.RequireIdentity, which both
			// install claims the same way.
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					claims := &auth.Claims{TenantID: 7, UserID: 9, Role: "admin"}
					ctx := auth.WithUserEmail(auth.WithClaims(req.Context(), claims), "jane@example.com")
					next.ServeHTTP(w, req.WithContext(ctx))
				})
			})
			r.Use(attachIdentity(Deps{}))
			r.Get("/api/secure", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/secure", nil))

	n := rec.One(t)
	if n.Context[logging.KeyTenantID] != "7" {
		t.Errorf("tenant = %v, want 7", n.Context[logging.KeyTenantID])
	}
	if n.Context[logging.KeyUserID] != "9" {
		t.Errorf("user = %v, want 9", n.Context[logging.KeyUserID])
	}
	if n.Context[logging.KeyUserRole] != "admin" {
		t.Errorf("role = %v, want admin", n.Context[logging.KeyUserRole])
	}
	// Honeybadger renders user_email natively in its fault UI —
	// notices only, never logs (see below).
	if n.Context[errtrack.ContextUserEmail] != "jane@example.com" {
		t.Errorf("user_email = %v, want the caller's email", n.Context[errtrack.ContextUserEmail])
	}
}

// TestImpersonatedNoticeNamesBothParties — on a "log in as org" session
// a fault has to answer two questions: who was driving, and whose
// account they were in. user_email is the operator, because claims.UserID stays
// theirs; impersonating_user_email is the org owner they are viewing.
func TestImpersonatedNoticeNamesBothParties(t *testing.T) {
	rec := errtracktest.Install(t)
	captureLogs(t)

	deps := Deps{Queries: testIdentityQuerier()}
	r := testRouter(t, func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					// TenantID must be the org the stub knows an owner for —
					// the lookup is by id, so a wrong tenant yields no email.
					claims := &auth.Claims{TenantID: testOrgTenantID, UserID: 9, Role: "owner", Impersonated: true}
					ctx := auth.WithUserEmail(auth.WithClaims(req.Context(), claims), testOperatorEmail)
					next.ServeHTTP(w, req.WithContext(ctx))
				})
			})
			r.Use(attachIdentity(deps))
			r.Get("/api/secure", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/secure", nil))

	n := rec.One(t)
	if n.Context[errtrack.ContextUserEmail] != testOperatorEmail {
		t.Errorf("user_email = %v, want the operator driving", n.Context[errtrack.ContextUserEmail])
	}
	if n.Context[errtrack.ContextImpersonatingUserEmail] != testOrgOwnerEmail {
		t.Errorf("impersonating_user_email = %v, want the impersonated org's owner", n.Context[errtrack.ContextImpersonatingUserEmail])
	}
	if n.Context[errtrack.ContextImpersonating] != true {
		t.Errorf("impersonating = %v, want true", n.Context[errtrack.ContextImpersonating])
	}
}

// TestOrdinarySessionHasNoImpersonationEmail — the field is the one sanctioned
// place a third party's address enters a notice, so it must appear only on
// impersonated sessions.
func TestOrdinarySessionHasNoImpersonationEmail(t *testing.T) {
	rec := errtracktest.Install(t)
	captureLogs(t)

	deps := Deps{Queries: testIdentityQuerier()}
	r := testRouter(t, func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					// testOrgTenantID deliberately: the stub HAS an owner for
					// it, so the field is absent because the session is not
					// impersonated — not because the lookup found nothing.
					claims := &auth.Claims{TenantID: testOrgTenantID, UserID: 9, Role: "admin"}
					ctx := auth.WithUserEmail(auth.WithClaims(req.Context(), claims), "jane@example.com")
					next.ServeHTTP(w, req.WithContext(ctx))
				})
			})
			r.Use(attachIdentity(deps))
			r.Get("/api/secure", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/secure", nil))

	n := rec.One(t)
	if v, present := n.Context[errtrack.ContextImpersonatingUserEmail]; present {
		t.Errorf("impersonating_user_email = %v on a non-impersonated session, want absent", v)
	}
	if v, present := n.Context[errtrack.ContextImpersonating]; present {
		t.Errorf("impersonating = %v on a non-impersonated session, want absent", v)
	}
}

// TestImpersonatedOwnerlessOrgStillReports — an org with no owner row (owner
// invite never accepted, owner removed) costs the notice that one field and
// nothing else: the request still reports, still under the operator.
//
// One mutation this cannot catch: dropping the `err == nil` guard in
// attachIdentity leaves owner.Email == "", which errtrack.notify then filters
// out, so the key is absent either way. The proxy's twin,
// TestProxyOwnerlessOrgKeepsTheOperator, does catch its own version of that —
// it writes straight into the notice context with no empty-value filter behind
// it. Removing notify's filter is caught here: the key arrives empty, not
// absent.
func TestImpersonatedOwnerlessOrgStillReports(t *testing.T) {
	rec := errtracktest.Install(t)
	captureLogs(t)

	deps := Deps{Queries: testIdentityQuerier()}
	r := testRouter(t, func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					claims := &auth.Claims{TenantID: testOwnerlessTenantID, UserID: 9, Role: "owner", Impersonated: true}
					ctx := auth.WithUserEmail(auth.WithClaims(req.Context(), claims), testOperatorEmail)
					next.ServeHTTP(w, req.WithContext(ctx))
				})
			})
			r.Use(attachIdentity(deps))
			r.Get("/api/secure", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/secure", nil))

	n := rec.One(t)
	if v, present := n.Context[errtrack.ContextImpersonatingUserEmail]; present {
		t.Errorf("impersonating_user_email = %v, want absent when the org has no owner", v)
	}
	if n.Context[errtrack.ContextUserEmail] != testOperatorEmail {
		t.Errorf("user_email = %v, want the operator regardless of the owner lookup", n.Context[errtrack.ContextUserEmail])
	}
	// The flag is why it is a flag and not an inference from the email: this is
	// the one session shape where the email cannot be resolved, and the notice
	// still has to say the operator was not acting as themselves.
	if n.Context[errtrack.ContextImpersonating] != true {
		t.Errorf("impersonating = %v, want true even when the owner lookup finds nothing", n.Context[errtrack.ContextImpersonating])
	}
}

// TestUserEmailAbsentFromLogs — the email travels notice-only. Logs stay
// in-cluster for 90 days and the ECS index template is dynamic:false; the
// Scope keeps the email out of Fields() structurally, and this test pins it.
func TestUserEmailAbsentFromLogs(t *testing.T) {
	const email = "jane@example.com"
	// The impersonated org's owner rides a second notice-only slot, held to the
	// same rule.
	const ownerEmail = testOrgOwnerEmail
	errtracktest.Install(t)
	buf := captureLogs(t)

	deps := Deps{Queries: testIdentityQuerier()}
	r := testRouter(t, func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					claims := &auth.Claims{TenantID: testOrgTenantID, UserID: 9, Role: "admin", Impersonated: true}
					ctx := auth.WithUserEmail(auth.WithClaims(req.Context(), claims), email)
					next.ServeHTTP(w, req.WithContext(ctx))
				})
			})
			r.Use(attachIdentity(deps))
			r.Get("/api/secure", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})
			r.Get("/api/fine", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/secure", nil))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/fine", nil))

	for _, line := range decodeLines(t, buf) {
		for k, v := range line {
			s, ok := v.(string)
			if !ok {
				continue
			}
			if strings.Contains(s, email) {
				t.Errorf("log field %q carries the user's email: %q", k, s)
			}
			if strings.Contains(s, ownerEmail) {
				t.Errorf("log field %q carries the impersonated org owner's email: %q", k, s)
			}
		}
	}
}

// TestAttachIdentityWithoutClaimsDoesNotPanic — auth.TenantIDFromContext and
// friends PANIC when claims are absent, so attachIdentity must read claims via
// ClaimsFromContext. Unauthenticated routes run this middleware in tests and
// could run it in future wiring.
func TestAttachIdentityWithoutClaimsDoesNotPanic(t *testing.T) {
	errtracktest.Install(t)
	captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Use(attachIdentity(Deps{})) // no claim-setter ahead of it
		r.Get("/api/open", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest("GET", "/api/open", nil))
	if res.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — attachIdentity must be harmless without claims", res.Code)
	}
}

// TestRequestLoggedOnce guards against the double-logging that reintroducing
// chi's middleware.Logger alongside this one would cause.
func TestRequestLoggedOnce(t *testing.T) {
	errtracktest.Install(t)
	buf := captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Get("/api/thing", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/thing", nil))

	lines := decodeLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines for one request, want 1:\n%s", len(lines), buf.String())
	}
	line := lines[0]
	if line[logging.KeyEventAction] != "http-request" {
		t.Errorf("event.action = %v", line[logging.KeyEventAction])
	}
	if line[logging.KeyRoute] != "/api/thing" {
		t.Errorf("route = %v", line[logging.KeyRoute])
	}
	if line[logging.KeyHTTPMethod] != "GET" {
		t.Errorf("method = %v", line[logging.KeyHTTPMethod])
	}
	if _, ok := line[logging.KeyTraceID]; !ok {
		t.Error("trace.id missing — RequestID should flow into the scope")
	}
	if _, ok := line[logging.KeyDurationMS]; !ok {
		t.Error("duration missing")
	}
}

// TestProbePathsNotLogged — /version takes liveness every 10s plus readiness
// every 5s, roughly 52k lines/day across two replicas, all of it noise.
func TestProbePathsNotLogged(t *testing.T) {
	errtracktest.Install(t)
	buf := captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		for _, p := range []string{"/version", "/health", "/health/scheduler", "/health/complete"} {
			r.Get(p, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		}
	})
	for _, p := range []string{"/version", "/health", "/health/scheduler", "/health/complete"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
	}

	if got := strings.TrimSpace(buf.String()); got != "" {
		t.Errorf("probe paths produced log output:\n%s", got)
	}
}

// TestFailingProbeIsStillReported — suppressing probe *logs* must not suppress a
// failing health check, which is a real signal.
func TestFailingProbeIsStillReported(t *testing.T) {
	rec := errtracktest.Install(t)
	buf := captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Get("/health/complete", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/health/complete", nil))

	if rec.Len() != 1 {
		t.Errorf("failing probe produced %d notices, want 1", rec.Len())
	}
	if strings.TrimSpace(buf.String()) == "" {
		t.Error("a failing probe should still be logged")
	}
}

// TestUnmatchedRouteFallsBackToPath — a 404 has no chi pattern, so grouping
// falls back to the raw path rather than an empty string.
func TestUnmatchedRouteFallsBackToPath(t *testing.T) {
	errtracktest.Install(t)
	buf := captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Get("/api/known", func(w http.ResponseWriter, _ *http.Request) {})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/nope", nil))

	lines := decodeLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	if got := lines[0][logging.KeyRoute]; got != "/api/nope" {
		t.Errorf("route = %v, want the raw path for an unmatched request", got)
	}
}

// TestRouteParamsRecorded — the route PATTERN tells you the shape of a failure
// ("/api/jobs/{jobID}/mt-apply"); it does not tell you which job. Pins that the
// concrete IDs are recorded next to the pattern.
func TestRouteParamsRecorded(t *testing.T) {
	rec := errtracktest.Install(t)
	captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Post("/api/projects/{projectID}/jobs/{jobID}/mt-apply", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/projects/7/jobs/42/mt-apply", nil))

	n := rec.One(t)
	if got := n.Context[logging.KeyJobID]; got != "42" {
		t.Errorf("job id = %v, want 42 — the notice must name the failing job", got)
	}
	if got := n.Context[logging.KeyProjectID]; got != "7" {
		t.Errorf("project id = %v, want 7", got)
	}
	// Both the pattern (for grouping) and the concrete path (for debugging).
	if got := n.Context[logging.KeyRoute]; got != "/api/projects/{projectID}/jobs/{jobID}/mt-apply" {
		t.Errorf("route = %v, want the pattern", got)
	}
	if got := n.Context[logging.KeyURLPath]; got != "/api/projects/7/jobs/42/mt-apply" {
		t.Errorf("url.path = %v, want the concrete path", got)
	}
}

// TestNoticeMessageIsSelfDescribing guards the field that is ALWAYS visible.
//
// Honeybadger's fault list, its emails and its Slack notifications render only
// the error class and the message — context fields require opening the fault
// page, so a message like "HTTP 500 on POST /api/jobs/{jobID}/mt-apply" is
// boilerplate.
//
// Pins that the message carries the concrete path and no caller identity.
// Identity lives in the notice context (tenant.id, user.id, user_email —
// Honeybadger renders user_email natively); an email in the message would
// bypass every context-level control.
func TestNoticeMessageIsSelfDescribing(t *testing.T) {
	rec := errtracktest.Install(t)
	captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					claims := &auth.Claims{TenantID: 7, UserID: 9, Role: "admin"}
					next.ServeHTTP(w, req.WithContext(auth.WithClaims(req.Context(), claims)))
				})
			})
			r.Use(attachIdentity(Deps{}))
			r.Post("/api/jobs/{jobID}/mt-apply", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/jobs/42/mt-apply", nil))

	msg := rec.One(t).ErrorMessage
	// The concrete path, so the message names the failing resource.
	if !strings.Contains(msg, "/api/jobs/42/mt-apply") {
		t.Errorf("message = %q, want the concrete path", msg)
	}
	// The chi placeholder must be gone, not merely accompanied by the real path.
	if strings.Contains(msg, "{jobID}") {
		t.Errorf("message = %q, still carries the chi placeholder", msg)
	}
	// Identity lives in the context, not the message.
	if strings.Contains(msg, "tenant 7") || strings.Contains(msg, "user 9") {
		t.Errorf("message = %q, identity must not be in the message", msg)
	}
}

// TestCredentialPathSegmentNeverRecorded is a security test, not a formatting one.
//
// GET /api/invites/lookup/{token} is public and carries a LIVE invite token in
// its path — redeeming it grants org membership — and its handler can 500 while
// loading the tenant. Recording the concrete path (rather than only the chi
// pattern) would put that token on two paths at once: the ECS log line, kept
// in-cluster for 90 days, and the Honeybadger notice, which leaves our
// infrastructure entirely.
//
// The token must appear nowhere: not in the message, not in url.path, and not in
// any context value.
func TestCredentialPathSegmentNeverRecorded(t *testing.T) {
	const token = "live-invite-token-do-not-leak"

	rec := errtracktest.Install(t)
	logs := captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Get("/api/invites/lookup/{token}", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/invites/lookup/"+token, nil))

	n := rec.One(t)
	if strings.Contains(n.ErrorMessage, token) {
		t.Errorf("notice message leaks the invite token: %q", n.ErrorMessage)
	}
	if got := n.Context[logging.KeyURLPath]; got != "/api/invites/lookup/"+errtrack.Redacted {
		t.Errorf("url.path = %v, want the token segment redacted", got)
	}
	for k, v := range n.Context {
		if s, ok := v.(string); ok && strings.Contains(s, token) {
			t.Errorf("notice context %q leaks the invite token: %q", k, s)
		}
	}

	// The log line is the half scrubNotice can never protect — it runs on
	// notices only — so assert on the emitted JSON too.
	for _, line := range decodeLines(t, logs) {
		for k, v := range line {
			if s, ok := v.(string); ok && strings.Contains(s, token) {
				t.Errorf("log field %q leaks the invite token: %q", k, s)
			}
		}
	}
}

// TestNoticeMessageOmitsUnknownIdentity — an unauthenticated route has no
// tenant or user, and the message must not advertise that with an empty
// bracket or a "tenant 0".
func TestNoticeMessageOmitsUnknownIdentity(t *testing.T) {
	rec := errtracktest.Install(t)
	captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Get("/api/public/thing", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/public/thing", nil))

	n := rec.One(t)
	if strings.ContainsAny(n.ErrorMessage, "[]") || strings.Contains(n.ErrorMessage, "tenant") {
		t.Errorf("message = %q, want no identity clause when identity is unknown", n.ErrorMessage)
	}
	// No identity → no user_email key at all: absent email must mean absent key,
	// not an empty string. Every authenticated path installs one (session JWT,
	// PAT and OAuth all call auth.WithUserEmail), so this is the unauthenticated
	// case — the only one that legitimately has no address to report.
	if v, ok := n.Context[errtrack.ContextUserEmail]; ok {
		t.Errorf("user_email = %v, want the key absent when no email is known", v)
	}
}

// TestRouteParamsNeverOverwriteIdentity is the important one.
//
// `userID` and `tenantID` appear as path params on routes where they name the
// TARGET of an operation, not the caller — /api/auth/orgs/{tenantID}/select,
// /api/users/{userID}. Mapping them onto user.id / openloc.tenant.id would
// overwrite what attachIdentity read from the auth claims, silently
// mis-attributing who performed the action. Here the caller is user 9 / tenant 7
// operating on user 999 / tenant 888; the identity fields must still say 9 and 7.
func TestRouteParamsNeverOverwriteIdentity(t *testing.T) {
	rec := errtracktest.Install(t)
	captureLogs(t)

	r := testRouter(t, func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					claims := &auth.Claims{TenantID: 7, UserID: 9, Role: "owner"}
					next.ServeHTTP(w, req.WithContext(auth.WithClaims(req.Context(), claims)))
				})
			})
			r.Use(attachIdentity(Deps{}))
			r.Post("/api/platform/orgs/{tenantID}/users/{userID}", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/platform/orgs/888/users/999", nil))

	n := rec.One(t)
	if got := n.Context[logging.KeyTenantID]; got != "7" {
		t.Errorf("tenant = %v, want 7 (the CALLER) — a path param overwrote identity", got)
	}
	if got := n.Context[logging.KeyUserID]; got != "9" {
		t.Errorf("user = %v, want 9 (the CALLER) — a path param overwrote identity", got)
	}
	// The targets are still recorded, just on the label tier where they can't collide.
	if got := n.Context["openloc.labels.param_tenantID"]; got != "888" {
		t.Errorf("target tenant label = %v, want 888", got)
	}
	if got := n.Context["openloc.labels.param_userID"]; got != "999" {
		t.Errorf("target user label = %v, want 999", got)
	}
}
