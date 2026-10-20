package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"openloc/internal/auth"
	"openloc/internal/errtrack"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// This file owns HTTP observability: structured request logging and 5xx
// reporting.
//
// It lives in internal/api rather than internal/logging on purpose: the
// middleware needs chi (WrapResponseWriter, RouteContext), auth (claims) and
// errtrack (reporting), and internal/logging stays stdlib-only.

// probePaths are high-frequency, low-signal paths: kubelet probes plus the
// SPA's bootstrap config. /version takes a liveness probe every 10s plus a
// readiness probe every 5s — 18 requests/minute per pod, and with 2 replicas
// that is roughly 52k log lines a day of pure noise against 90 day retention;
// /api/config is fetched once per browser page load. Skipped for logging; a
// 5xx from one is still reported, because a failing health check is a real
// signal.
var probePaths = map[string]bool{
	"/version":          true,
	"/health":           true,
	"/health/scheduler": true,
	"/health/complete":  true,
	"/api/config":       true,
}

// requestObservability creates the per-request logging.Scope, logs the request in
// ECS form, and reports 5xx responses to Honeybadger.
//
// It replaces chi's middleware.Logger — running both would emit two lines per
// request, one unstructured and one ECS.
//
// # Placement
//
// Must be registered INSIDE middleware.Recoverer (i.e. after it) and OUTSIDE
// honeybadger.Handler. That ordering is what prevents a panic being reported
// twice: honeybadger.Handler reports the panic and re-panics, the panic passes
// through here, and only then does Recoverer convert it to a 500. If this
// middleware sat outside Recoverer instead, it would observe Recoverer's
// synthesised 500 as an ordinary response and report it a second time.
func requestObservability(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, scope := logging.WithScope(r.Context())
		r = r.WithContext(ctx)

		if reqID := middleware.GetReqID(ctx); reqID != "" {
			scope.Set(logging.TraceID(reqID))
		}
		scope.Set(logging.HTTPMethod(r.Method))

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		rec := &httputil.ErrorRecordingWriter{ResponseWriter: ww}
		start := time.Now()

		// panicked avoids calling recover() here. Recovering would swallow the
		// panic and force us to re-panic, losing Recoverer's ownership of the
		// response; a flag set after ServeHTTP returns tells us the same thing.
		panicked := true
		defer func() {
			status := ww.Status()
			if panicked {
				// Recoverer writes the 500 after this defer unwinds, so nothing
				// has been written yet and ww.Status() is still 0. Record what
				// the client will actually receive.
				status = http.StatusInternalServerError
			}

			// The route pattern is only populated once chi has routed, so it
			// must be read after the handler ran. RouteContext hands back a
			// pointer installed before the middleware chain, so this observes
			// the filled-in value rather than a copy.
			route := routePattern(r)
			path := safePath(r)
			scope.Set(logging.Route(route), logging.HTTPStatus(status), logging.URLPath(path))
			// Concrete resource IDs, so a notice says which job/project/file
			// failed rather than only "{jobID}". Read after the handler ran —
			// chi fills URLParams during routing.
			attachRouteParams(r, scope)

			logRequest(r, route, status, time.Since(start), panicked, rec.Err())

			// Panics are already reported by honeybadger.Handler, with the
			// panic's own stack — reporting again here would double-count.
			//
			// The errtrack notice proxy is exempt: its 5xx means "Honeybadger
			// upstream is unreachable", and reporting a Honeybadger outage to
			// Honeybadger goes nowhere (the log line above still records it).
			//
			// A request whose context is already canceled is exempt too: the
			// client disconnected (browser refresh, closed tab), whatever the
			// handler was doing failed on the dead context, and the resulting
			// 500 is an artifact of the disconnect, not a server fault.
			// The error log line above remains the record.
			if !panicked && status >= http.StatusInternalServerError && path != errtrackNoticePath && r.Context().Err() == nil {
				errtrack.Capture(r.Context(), nil, errtrack.CaptureOptions{
					Class: errtrack.ClassHTTPServerError,
					// The CONCRETE path, deliberately not the pattern.
					// Honeybadger's fault list, its emails and its Slack
					// notifications show only the error class and this message,
					// so a message built from the pattern reads "HTTP 500 on
					// POST /api/jobs/{jobID}/mt-apply" — the shape of the
					// failure with no subject. Grouping is unaffected: Event
					// below keeps the pattern, and the fingerprint uses Event.
					Message:   fmt.Sprintf("HTTP %d on %s %s", status, r.Method, path),
					Subsystem: "http",
					Event:     r.Method + " " + route,
				})
			}
		}()

		next.ServeHTTP(rec, r)
		panicked = false
	})
}

// logRequest emits one ECS line per request. Errors at error level so Kibana's
// log.level filter is useful; everything else at info. handlerErr is what the
// handler recorded through httputil, nil when it recorded nothing; it stays in
// the log and never reaches the Honeybadger notice, since a database error can
// quote customer data.
func logRequest(r *http.Request, route string, status int, took time.Duration, panicked bool, handlerErr error) {
	if probePaths[r.URL.Path] && !panicked && status < http.StatusInternalServerError {
		return
	}

	// Method, route and status are already on the scope, so the handler merges
	// them into every line — only the duration needs passing explicitly.
	ctx := r.Context()
	switch {
	case panicked:
		logging.Error(ctx, nil, "request panicked", "http-request-panic", logging.Duration(took))
	case status >= http.StatusInternalServerError:
		logging.Error(ctx, handlerErr, "request failed", "http-request-error", logging.Duration(took))
	default:
		logging.Info(ctx, "request", "http-request", logging.Duration(took))
	}
}

// urlParamFields maps chi URL parameter names onto the domain fields they
// represent, so a notice for "/api/jobs/{jobID}/mt-apply" also records WHICH
// job — the pattern alone tells you the shape of the failure, not the subject.
//
// `userID` and `tenantID` are deliberately ABSENT. They appear as path params on
// routes like /api/auth/orgs/{tenantID}/select and /api/users/{userID}, where
// they name the *target* of the operation, not the caller. Mapping them onto
// user.id / openloc.tenant.id would overwrite the identity attachIdentity put
// there from the auth claims, silently mis-attributing who did what. They fall
// through to the label tier below instead, which keeps both facts visible.
var urlParamFields = map[string]string{
	"projectID":     logging.KeyProjectID,
	"jobID":         logging.KeyJobID,
	"fileID":        logging.KeyFileID,
	"translationID": logging.KeyTranslationID,
	"tmID":          logging.KeyTMID,
	"baseID":        logging.KeyTermbaseID,
	"analysisID":    logging.KeyAnalysisID,
	"profileID":     logging.KeyQAProfileID,
}

// attachRouteParams records the concrete resource IDs from the matched route.
// Must run AFTER the handler, since chi only fills URLParams during routing.
func attachRouteParams(r *http.Request, scope *logging.Scope) {
	rc := chi.RouteContext(r.Context())
	if rc == nil {
		return
	}
	for i, key := range rc.URLParams.Keys {
		if i >= len(rc.URLParams.Values) {
			break
		}
		val := rc.URLParams.Values[i]
		if val == "" || key == "*" { // chi uses "*" for wildcard matches
			continue
		}
		// A credential-bearing param never gets recorded, not even redacted:
		// /api/invites/lookup/{token} carries a live invite token. scrubNotice
		// would catch the resulting "…param_token" context key by name, but
		// relying on that would leave the value in the ECS log line, which the
		// scrubber never sees.
		if errtrack.IsSensitiveName(key) {
			continue
		}
		if field, ok := urlParamFields[key]; ok {
			scope.Set(logging.IDFromString(field, val))
			continue
		}
		// Everything else — templateID, engineID, entryID, provider, and the
		// deliberately-unmapped userID/tenantID — rides the searchable label
		// tier rather than growing the strict-mapped field set per route.
		scope.Set(logging.Label("param_"+key, val))
	}
}

// safePath returns the concrete request path with any credential-bearing path
// segment replaced by a redaction marker.
//
// GET /api/invites/lookup/{token} is a public route whose path carries a LIVE
// invite token — one that grants org membership when redeemed — and its handler
// has a reachable 500 (it loads the tenant after resolving the invite). The
// SPA catch-all serves /invite/{token} and /reset-password/{token} the same
// way. The value this feeds is used twice: as ECS url.path, retained in-cluster
// for 90 days, and in the Honeybadger notice message, which leaves our
// infrastructure entirely. The raw path is unacceptable in either.
//
// Two rules apply. A URL param with a credential-shaped name is substituted by
// value rather than by rebuilding from the route pattern, because the pattern
// is unavailable on a 404 and because percent-encoding in the surrounding
// segments should survive untouched. errtrack.RedactTokenPaths then catches
// the token paths whatever the route: the SPA's token sits in chi's "*" param,
// which no name rule can recognize.
func safePath(r *http.Request) string {
	path := r.URL.Path
	if rc := chi.RouteContext(r.Context()); rc != nil {
		for i, key := range rc.URLParams.Keys {
			if i >= len(rc.URLParams.Values) {
				break
			}
			val := rc.URLParams.Values[i]
			if val == "" || !errtrack.IsSensitiveName(key) {
				continue
			}
			path = strings.Replace(path, val, errtrack.Redacted, 1)
		}
	}
	return errtrack.RedactTokenPaths(path)
}

// routePattern returns the matched chi route pattern ("/api/projects/{projectID}"),
// falling back to the path when nothing matched (404s, and the 405 a HEAD gets
// from a GET-only route). The pattern rather than the path keeps IDs out of
// fingerprints and aggregations. The fallback redacts token segments, since a
// link scanner's HEAD on an invite link lands here.
func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return errtrack.RedactTokenPaths(r.URL.Path)
}

// attachIdentity copies auth claims into the request's logging.Scope so every
// subsequent log line and any error notice carries tenant, user and role.
//
// It exists because a top-level middleware cannot read claims: auth.Middleware
// installs them by replacing the request further down the chain, so the outer
// middleware still holds the pre-auth context. The scope is a pointer, so
// filling it in here is visible to the middleware that created it.
//
// Register immediately after each claim-setter. There are two — auth.RequireIdentity
// and auth.Middleware. Both also install the user's email (auth.WithUserEmail,
// from the UserExists lookup) which this middleware moves onto the scope.
func attachIdentity(deps Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return attachIdentityHandler(deps, next)
	}
}

func attachIdentityHandler(deps Deps, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// ClaimsFromContext, not TenantIDFromContext: the typed helpers PANIC
		// when claims are absent, and this middleware must be harmless on any
		// route where authentication was optional or skipped.
		if claims, ok := auth.ClaimsFromContext(ctx); ok && claims != nil {
			scope := logging.ScopeFromContext(ctx)
			if claims.TenantID != 0 {
				scope.Set(logging.TenantID(claims.TenantID))
			}
			if claims.UserID != 0 {
				scope.Set(logging.UserID(claims.UserID))
			}
			if claims.Role != "" {
				scope.Set(logging.UserRole(claims.Role))
			}
			// The email goes to the Scope's notice-only slot, never Set() —
			// notices carry user_email, ECS
			// logs must not. PAT requests carry the token owner's email.
			if email := auth.UserEmailFromContext(ctx); email != "" {
				scope.SetUserEmail(email)
			}
			// Claims carry no email, hence the lookup — impersonated sessions
			// are a handful of operators, so the per-request cost is bounded.
			// Best-effort: a failed lookup drops the address, never the flag.
			if claims.Impersonated {
				scope.SetImpersonating()
				if deps.Queries != nil && claims.TenantID != 0 {
					if owner, err := deps.Queries.GetTenantOwner(ctx, claims.TenantID); err == nil {
						scope.SetImpersonatingUserEmail(owner.Email)
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
