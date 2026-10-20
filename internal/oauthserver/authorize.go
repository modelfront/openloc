package oauthserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// The authorize endpoint (RFC 6749 §4.1 + OAuth 2.1 hardening).
//
// GET renders the consent screen; POST (from that screen only) issues the
// code and redirects back to the client. Two hard rules shape the error
// handling: NEVER redirect to an unvalidated redirect_uri (invalid client or
// uri ⇒ render an error page, don't bounce the user to an attacker), and once
// client+uri are validated, protocol errors DO redirect with ?error=... so
// the client can present them.
//
// The consent screen is server-rendered: the browser session cookie
// authenticates it (any existing sign-in method produced that cookie), no SPA
// involvement, works with JavaScript disabled.

// authorizeRequest is the validated query surface both GET and POST share.
type authorizeRequest struct {
	client        clientRecord
	redirectURI   string
	state         string
	scope         string
	codeChallenge string
	resource      string
}

// parseAuthorize validates everything that must NOT cause a redirect when
// wrong. Returns a human message for the error page on failure.
func (s *Server) parseAuthorize(r *http.Request) (*authorizeRequest, string) {
	q := r.URL.Query()
	if r.Method == http.MethodPost {
		q = r.PostForm
	}
	clientID := q.Get("client_id")
	if clientID == "" {
		return nil, "The request is missing its client identity."
	}
	client, err := s.resolveClient(r.Context(), clientID)
	if err != nil {
		return nil, "Unknown or unreachable client. The connecting app may be misconfigured."
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" || !redirectAllowed(client.RedirectURIs, redirectURI) {
		return nil, "The redirect address does not match the client's registration."
	}
	return &authorizeRequest{
		client:        client,
		redirectURI:   redirectURI,
		state:         q.Get("state"),
		scope:         q.Get("scope"),
		codeChallenge: q.Get("code_challenge"),
		resource:      q.Get("resource"),
	}, ""
}

// redirectErr sends a protocol error back to the (validated) client.
func (s *Server) redirectErr(w http.ResponseWriter, r *http.Request, req *authorizeRequest, code, desc string) {
	s.redirectToClient(w, r, req, url.Values{"error": {code}, "error_description": {desc}})
}

// redirectToClient ends the authorization request at the client's validated
// redirect URI. Every response, success or error, carries state and the RFC
// 9207 iss, which must equal the metadata issuer exactly.
func (s *Server) redirectToClient(w http.ResponseWriter, r *http.Request, req *authorizeRequest, params url.Values) {
	u, _ := url.Parse(req.redirectURI)
	qs := u.Query()
	for k, v := range params {
		qs[k] = v
	}
	if req.state != "" {
		qs.Set("state", req.state)
	}
	qs.Set("iss", s.baseURL)
	u.RawQuery = qs.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// normalizeScope maps the requested scope onto read|write. Empty or
// unknown-but-read-ish requests get read; anything mentioning write gets the
// write consent screen. The USER's approval on that screen is what grants it.
func normalizeScope(raw string) (string, bool) {
	fields := strings.Fields(raw)
	for _, f := range fields {
		if f == "write" {
			return "write", true
		}
	}
	for _, f := range fields {
		if f != "read" && f != "offline_access" {
			return "", false
		}
	}
	return "read", true
}

// HandleAuthorize serves GET (consent screen) and POST (decision).
func (s *Server) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			renderError(w, r, http.StatusBadRequest, "Malformed form submission.")
			return
		}
	}
	req, errMsg := s.parseAuthorize(r)
	if errMsg != "" {
		renderError(w, r, http.StatusBadRequest, errMsg)
		return
	}

	// Browser identity: any existing sign-in produced this session. Without
	// one, bounce to the SPA login and come back here afterwards.
	claims, err := auth.SessionClaims(r, s.jwtSecret)
	if err != nil {
		next := r.URL.String()
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusFound)
		return
	}
	// The demo organization is read-only, and auth.Middleware, which holds
	// that barrier, does not run here: a grant would write into it.
	if claims.Demo {
		renderError(w, r, http.StatusForbidden, "You are in the Playground. Leave it to connect an app.")
		return
	}

	// Everything below redirects errors to the client (client+uri are valid).
	scope, ok := normalizeScope(req.scope)
	if !ok {
		s.redirectErr(w, r, req, "invalid_scope", "supported scopes: read, write")
		return
	}
	if req.resource != "" && !s.isOurResource(req.resource) {
		s.redirectErr(w, r, req, "invalid_target", "tokens are only issued for "+s.Resource())
		return
	}
	// OAuth 2.1: PKCE is mandatory, S256 only.
	if q := queryOf(r); q.Get("response_type") != "code" {
		s.redirectErr(w, r, req, "unsupported_response_type", "only response_type=code is supported")
		return
	}
	if req.codeChallenge == "" || queryOf(r).Get("code_challenge_method") != "S256" {
		s.redirectErr(w, r, req, "invalid_request", "PKCE with code_challenge_method=S256 is required")
		return
	}

	if r.Method == http.MethodGet {
		s.renderConsent(w, r, req, claims, scope)
		return
	}
	s.handleDecision(w, r, req, claims, scope)
}

func queryOf(r *http.Request) url.Values {
	if r.Method == http.MethodPost {
		return r.PostForm
	}
	return r.URL.Query()
}

type hiddenField struct{ Name, Value string }

func (s *Server) renderConsent(w http.ResponseWriter, r *http.Request, req *authorizeRequest, claims *auth.Claims, scope string) {
	orgs, err := s.q.ListMembershipsWithTenant(r.Context(), claims.UserID)
	if err != nil || len(orgs) == 0 {
		logging.Error(r.Context(), err, "consent membership load failed", "oauth-consent-orgs-failed")
		renderError(w, r, http.StatusInternalServerError, "Could not load your organizations.")
		return
	}
	type orgOpt struct {
		ID       int64
		Name     string
		Role     string
		Selected bool
	}
	opts := make([]orgOpt, 0, len(orgs))
	for _, m := range orgs {
		opts = append(opts, orgOpt{ID: m.TenantID, Name: m.TenantName, Role: m.Role, Selected: m.TenantID == claims.TenantID})
	}

	// CSRF: the POST must echo an HMAC bound to this user + client. No stored
	// state; SameSite=Lax on the session cookie blocks cross-site POSTs too —
	// this is the belt to that braces.
	nonce := s.consentNonce(claims.UserID, req.client.ID)

	hidden := []hiddenField{
		{"client_id", req.client.ID},
		{"redirect_uri", req.redirectURI},
		{"response_type", "code"},
		{"state", req.state},
		{"scope", req.scope},
		{"code_challenge", req.codeChallenge},
		{"code_challenge_method", "S256"},
		{"resource", req.resource},
		{"csrf", nonce},
	}
	// No middleware ran here (public route) — resolve the email directly.
	email := "you"
	if u, err := s.q.GetUser(r.Context(), claims.UserID); err == nil {
		email = u.Email
	}
	// The redirect host is where the authorization code actually goes; showing
	// it lets a user spot a client whose (self-chosen) name doesn't match its
	// destination — the DCR name-spoofing defense.
	redirectHost := req.redirectURI
	if u, err := url.Parse(req.redirectURI); err == nil && u.Host != "" {
		redirectHost = u.Host
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = consentTmpl.Execute(w, map[string]any{
		"ClientName":   req.client.Name,
		"RedirectHost": redirectHost,
		"Write":        scope == "write",
		"Orgs":         opts,
		"Hidden":       hidden,
		"UserEmail":    email,
	})
	if err != nil {
		logging.Error(r.Context(), err, "consent render failed", "oauth-consent-render-failed")
	}
}

func (s *Server) handleDecision(w http.ResponseWriter, r *http.Request, req *authorizeRequest, claims *auth.Claims, scope string) {
	if !hmac.Equal([]byte(r.PostForm.Get("csrf")), []byte(s.consentNonce(claims.UserID, req.client.ID))) {
		renderError(w, r, http.StatusForbidden, "The consent form has expired. Start the connection again.")
		return
	}
	if r.PostForm.Get("decision") != "approve" {
		s.redirectErr(w, r, req, "access_denied", "the user denied the request")
		return
	}

	// The org choice must be one of the USER's memberships — the form value
	// is client-controlled, the membership check is what makes it real.
	tenantID, err := strconv.ParseInt(r.PostForm.Get("tenant_id"), 10, 64)
	if err != nil {
		s.redirectErr(w, r, req, "invalid_request", "bad organization selection")
		return
	}
	if _, err := s.q.GetMembership(r.Context(), db.GetMembershipParams{UserID: claims.UserID, TenantID: tenantID}); err != nil {
		s.redirectErr(w, r, req, "access_denied", "not a member of the selected organization")
		return
	}

	code, codeHash, err := newSecret(codePrefix)
	if err != nil {
		s.redirectErr(w, r, req, "server_error", "could not issue an authorization code")
		return
	}
	if err := s.q.CreateOAuthCode(r.Context(), db.CreateOAuthCodeParams{
		CodeHash:      codeHash,
		ClientID:      req.client.ID,
		UserID:        claims.UserID,
		TenantID:      tenantID,
		Scope:         scope,
		RedirectUri:   req.redirectURI,
		CodeChallenge: req.codeChallenge,
		Resource:      req.resource,
		ExpiresAt:     pgtype.Timestamptz{Time: time.Now().Add(codeTTL), Valid: true},
	}); err != nil {
		logging.Error(r.Context(), err, "authorization code insert failed", "oauth-code-insert-failed")
		s.redirectErr(w, r, req, "server_error", "could not issue an authorization code")
		return
	}

	s.redirectToClient(w, r, req, url.Values{"code": {code}})
}

// consentBucketSeconds is the CSRF nonce's validity window: a form minted in
// one 2h bucket is rejected once the wall clock crosses into the next.
const consentBucketSeconds = 7200

func currentBucket() int64 { return time.Now().Unix() / consentBucketSeconds }

// consentNonce is an HMAC over (user, client, current 2h bucket) — a
// self-expiring CSRF proof with no server-side state.
func (s *Server) consentNonce(userID int64, clientID string) string {
	return s.consentNonceAtBucket(userID, clientID, currentBucket())
}

func (s *Server) consentNonceAtBucket(userID int64, clientID string, bucket int64) string {
	mac := hmac.New(sha256.New, []byte(s.jwtSecret))
	fmt.Fprintf(mac, "consent|%d|%s|%d", userID, clientID, bucket)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
