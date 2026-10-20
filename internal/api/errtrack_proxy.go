package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// handleAppConfig serves GET /api/config — the SPA's bootstrap config.
// error_reporting is a boolean precisely so the key never appears in any
// response; the frontend only needs to know whether to turn its SDK on.
func handleAppConfig(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httputil.JSON(w, http.StatusOK, map[string]any{
			"version":         deps.Revision,
			"environment":     deps.Environment,
			"error_reporting": deps.HoneybadgerAPIKey != "",
			// The app's own address, for links from the documentation hosts.
			"app_url": deps.BaseURL,
			// Each documentation site's one address (empty when it stays at
			// /docs or /api-docs), and whether this request came in on it.
			// BASE_URL need not match the address the app is served on, so
			// the frontend never compares addresses itself.
			"docs_url":       hostOrigin(deps.BaseURL, deps.DocsHost),
			"docs_host":      onHost(r, deps.DocsHost),
			"developer_url":  hostOrigin(deps.BaseURL, deps.DeveloperHost),
			"developer_host": onHost(r, deps.DeveloperHost),
			// The Playground is on: the UI offers it and its saved tours.
			"playground": deps.DemoOrg,
			// Users may report issues: the deployment has operators to read them.
			"issues": len(deps.PlatformAdminEmails) > 0,
		})
	}
}

// errtrackProxy forwards browser error notices to Honeybadger with the real
// API key attached server-side. The frontend SDK is configured with a
// placeholder key and endpoint=<origin>/api/errtrack, so its standard payload
// arrives here at /v1/notices/js unmodified; the key never reaches a browser.
//
// The route is unauthenticated — the Honeybadger JS transport can't attach our
// bearer token — so the defenses are a per-IP rate limiter (registered in
// api.go), a body-size cap, and strict JSON validation. Notices are scrubbed
// again server-side: the client scrubs too, but this is the enforcement point.
type errtrackProxy struct {
	apiKey    string
	endpoint  string // upstream base, default https://api.honeybadger.io
	env       string
	revision  string
	jwtSecret string
	queries   db.Querier
	client    *http.Client
}

// noticeMaxBytes caps a single notice payload. A real SDK notice (error class,
// message, backtrace, context) is a few KB; anything near the cap is abuse.
const noticeMaxBytes = 64 << 10

// errtrackNoticePath is the route the browser SDK posts to — the exact path
// the SDK constructs from endpoint=<origin>/api/errtrack. Referenced by
// requestObservability to exempt this route's 5xx from Honeybadger capture.
const errtrackNoticePath = "/api/errtrack/v1/notices/js"

func newErrtrackProxy(deps Deps) *errtrackProxy {
	endpoint := deps.HoneybadgerEndpoint
	if endpoint == "" {
		endpoint = "https://api.honeybadger.io"
	}
	return &errtrackProxy{
		apiKey:    deps.HoneybadgerAPIKey,
		endpoint:  strings.TrimRight(endpoint, "/"),
		env:       deps.Environment,
		revision:  deps.Revision,
		jwtSecret: deps.JWTSecret,
		queries:   deps.Queries,
		client:    &http.Client{Timeout: 5 * time.Second},
	}
}

// identityKeys are the context fields the server owns. A browser posts here
// unauthenticated, so anything it claims about who it is has to be replaced
// with what the session cookie actually proves — or removed when it proves
// nothing. Listed once so overwrite and strip can't drift apart.
var identityKeys = []string{
	"user_id",
	errtrack.ContextUserEmail,
	errtrack.ContextImpersonatingUserEmail,
	"tenant_id",
	"role",
	errtrack.ContextImpersonating,
}

// applyServerIdentity replaces the notice's identity context with the identity
// the request's own session cookie proves, and strips it entirely when there is
// no valid session.
//
// The SDK's transport is a plain same-origin fetch with no credentials option,
// so the browser attaches the HttpOnly cookie for us — which is the only reason
// an unauthenticated route can know who sent the notice. It must stay
// unauthenticated regardless: a crash on the login page, or after a session
// expires, still has to be reportable, so a missing session is never an error.
//
// The addresses come from claims.UserID, which stays the operator's under
// impersonation — the same source attachIdentity uses, so browser and backend
// notices describe a session identically.
func (p *errtrackProxy) applyServerIdentity(r *http.Request, notice map[string]any) {
	req, _ := notice["request"].(map[string]any)
	if req == nil {
		req = map[string]any{}
		notice["request"] = req
	}
	ctxMap, _ := req["context"].(map[string]any)
	if ctxMap == nil {
		ctxMap = map[string]any{}
		req["context"] = ctxMap
	}
	for _, k := range identityKeys {
		delete(ctxMap, k)
	}

	claims, err := auth.SessionClaims(r, p.jwtSecret)
	if err != nil || claims == nil || claims.UserID == 0 {
		return
	}
	ctxMap["user_id"] = strconv.FormatInt(claims.UserID, 10)
	if claims.TenantID != 0 {
		ctxMap["tenant_id"] = strconv.FormatInt(claims.TenantID, 10)
	}
	if claims.Role != "" {
		ctxMap["role"] = claims.Role
	}
	if claims.Impersonated {
		ctxMap[errtrack.ContextImpersonating] = true
	}
	if p.queries == nil {
		return
	}
	// Best-effort enrichment: a failed lookup costs the notice a name, never
	// the notice itself.
	if u, uErr := p.queries.GetActiveUser(r.Context(), claims.UserID); uErr == nil {
		ctxMap[errtrack.ContextUserEmail] = u.Email
	}
	if claims.Impersonated && claims.TenantID != 0 {
		if owner, oErr := p.queries.GetTenantOwner(r.Context(), claims.TenantID); oErr == nil {
			ctxMap[errtrack.ContextImpersonatingUserEmail] = owner.Email
		}
	}
}

func (p *errtrackProxy) handleNotice(w http.ResponseWriter, r *http.Request) {
	if p.apiKey == "" {
		// Reporting disabled (dev, tests, missing secret) — same shape as an
		// unmounted route. Fail-safe default, matching errtrack.Init.
		httputil.Error(w, http.StatusNotFound, "not found")
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		httputil.BadRequest(w, "expected application/json")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, noticeMaxBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			httputil.Error(w, http.StatusRequestEntityTooLarge, "notice too large")
			return
		}
		httputil.BadRequest(w, "unreadable request body")
		return
	}
	var notice map[string]any
	if err := json.Unmarshal(body, &notice); err != nil || notice == nil {
		// Never echo the body back — its contents are untrusted.
		httputil.BadRequest(w, "notice must be a JSON object")
		return
	}

	scrubClientNotice(notice)
	// After scrubbing: the scrubber redacts credential-shaped keys, and the
	// identity we write here must not be subject to that pass.
	p.applyServerIdentity(r, notice)

	// Integrity overrides: clients don't get to pick the environment or
	// revision. Revision equality with the uploaded sourcemaps is also what
	// makes Honeybadger translate minified backtraces.
	server, _ := notice["server"].(map[string]any)
	if server == nil {
		server = map[string]any{}
	}
	server["environment_name"] = p.env
	server["revision"] = p.revision
	notice["server"] = server

	out, err := json.Marshal(notice)
	if err != nil {
		httputil.BadRequest(w, "notice not serializable")
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, p.endpoint+"/v1/notices/js", bytes.NewReader(out))
	if err != nil {
		logging.Error(r.Context(), err, "building honeybadger forward request failed", "errtrack-proxy-build-request")
		httputil.Error(w, http.StatusBadGateway, "error reporting unavailable")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		// Best-effort by design: the client treats any failure as a dropped
		// notice. Log it (in-cluster) but don't notice it — a Honeybadger
		// outage reporting itself to Honeybadger goes nowhere.
		logging.Warn(r.Context(), err, "forwarding notice to honeybadger failed", "errtrack-proxy-forward-failed")
		httputil.Error(w, http.StatusBadGateway, "error reporting unavailable")
		return
	}
	defer resp.Body.Close()
	// Drain so the keep-alive connection is reusable; the body is a tiny
	// {"id": "..."} we deliberately don't relay (nothing in it is ours).
	_, _ = io.Copy(io.Discard, resp.Body)

	httputil.JSON(w, resp.StatusCode, map[string]string{})
}

// scrubClientNotice enforces the notice scrubbing rules on a client-built
// notice, mirroring errtrack/scrub.go: URLs lose query/fragment/userinfo
// (?source_text= carries customer segment text) and token path segments
// (/invite/{token}), credential-shaped keys are redacted, and the fields we
// can't scrub reliably are dropped wholesale.
func scrubClientNotice(notice map[string]any) {
	// Breadcrumbs record full URLs of every request leading up to the error
	// and can't be scrubbed field-by-field with confidence — drop them, same
	// call the frontend makes by disabling them (defense in depth).
	delete(notice, "breadcrumbs")

	// error.message routinely embeds request URLs ("GET …?source_text=… failed")
	// and error.backtrace[].file can be a page URL (inline scripts or eval), query
	// included. The client scrubs its message too, but this is the enforcement
	// point — and the only one for error.causes[], which the SDK builds from
	// Error.cause after the client scrubber has run.
	if errObj, ok := notice["error"].(map[string]any); ok {
		scrubErrorObject(errObj)
		if causes, ok := errObj["causes"].([]any); ok {
			for _, c := range causes {
				if cause, ok := c.(map[string]any); ok {
					scrubErrorObject(cause)
				}
			}
		}
	}

	req, _ := notice["request"].(map[string]any)
	if req == nil {
		return
	}
	delete(req, "session")
	if raw, ok := req["url"].(string); ok {
		req["url"] = errtrack.ScrubURL(raw)
	}
	for _, k := range []string{"context", "params", "cgi_data"} {
		if m, ok := req[k].(map[string]any); ok {
			redactSensitiveKeys(m)
		}
	}
	// The SDK copies document.referrer into cgi_data.HTTP_REFERER after the
	// client-side scrubber has run — a full URL whose query can carry free text
	// (?q= is the SPA's file-name search) and whose path can carry a token
	// (/invite/{token}). Same rule as request.url for every URL-valued
	// cgi_data entry.
	if cgi, ok := req["cgi_data"].(map[string]any); ok {
		for k, v := range cgi {
			if s, ok := v.(string); ok {
				cgi[k] = errtrack.ScrubHeaderValue(s)
			}
		}
	}
}

// scrubErrorObject applies the browser's scrub rules to one error object of a
// notice (the error itself or one of its causes): its message, and each
// backtrace file.
func scrubErrorObject(errObj map[string]any) {
	if msg, ok := errObj["message"].(string); ok {
		errObj["message"] = errtrack.ScrubMessage(msg)
	}
	frames, _ := errObj["backtrace"].([]any)
	for _, f := range frames {
		frame, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if file, ok := frame["file"].(string); ok {
			frame["file"] = errtrack.ScrubURL(file)
		}
	}
}

func redactSensitiveKeys(m map[string]any) {
	for k, v := range m {
		if errtrack.IsSensitiveName(k) {
			m[k] = errtrack.Redacted
			continue
		}
		switch nested := v.(type) {
		case map[string]any:
			redactSensitiveKeys(nested)
		case []any:
			for _, el := range nested {
				if em, ok := el.(map[string]any); ok {
					redactSensitiveKeys(em)
				}
			}
		}
	}
}
