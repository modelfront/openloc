package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/microsoft"

	"openloc/internal/dashboarddefaults"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/mtdefaults"
	"openloc/internal/seedid"
)

const oauthStateCookie = "__oauth_state"

// oauthUser is the normalized identity an OAuth provider returns — the only
// provider-specific surface the shared callback depends on.
type oauthUser struct {
	Email string
	Name  string
}

// userInfoFetcher fetches the signed-in user's identity from a provider's
// userinfo/Graph endpoint using the exchanged token. One per provider
// (fetchGoogleUserInfo, fetchMicrosoftUserInfo).
type userInfoFetcher func(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token) (*oauthUser, error)

// Callback paths, registered with the providers and — for a relayed sign-in —
// the path the code is forwarded to on the branch host.
const (
	googleCallbackPath    = "/api/auth/google/callback"
	microsoftCallbackPath = "/api/auth/microsoft/callback"
)

// GoogleOAuthConfig builds an oauth2.Config if credentials are set.
// Returns nil if client ID is empty. redirectBaseURL is the origin registered
// with Google, which is not necessarily where this deployment runs — see
// OAuthRelay.
func GoogleOAuthConfig(clientID, clientSecret, redirectBaseURL string) *oauth2.Config {
	if clientID == "" {
		return nil
	}
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  redirectBaseURL + googleCallbackPath,
		Scopes:       []string{"openid", "email", "profile"},
	}
}

// MicrosoftOAuthConfig builds an oauth2.Config for Microsoft Entra ID, or nil
// if the client ID is unset (disables the routes, same as Google). Uses the
// "organizations" authority so ONLY work/school accounts can sign in — personal
// @outlook/@hotmail Microsoft accounts are rejected at Microsoft's consent
// screen. Identity is read from the ID token (see decodeMicrosoftIDToken).
func MicrosoftOAuthConfig(clientID, clientSecret, redirectBaseURL string) *oauth2.Config {
	if clientID == "" {
		return nil
	}
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     microsoft.AzureADEndpoint("organizations"),
		RedirectURL:  redirectBaseURL + microsoftCallbackPath,
		Scopes:       []string{"openid", "email", "profile"},
	}
}

// HandleGoogleLogin redirects to Google's OAuth consent page.
func (h *Handler) HandleGoogleLogin(cfg *oauth2.Config, relay OAuthRelay) http.HandlerFunc {
	return h.oauthLogin(cfg, relay, "Google OAuth")
}

// HandleMicrosoftLogin redirects to Microsoft's Entra ID consent page.
func (h *Handler) HandleMicrosoftLogin(cfg *oauth2.Config, relay OAuthRelay) http.HandlerFunc {
	return h.oauthLogin(cfg, relay, "Microsoft OAuth")
}

// HandleGoogleCallback handles the OAuth callback from Google.
func (h *Handler) HandleGoogleCallback(cfg *oauth2.Config, relay OAuthRelay) http.HandlerFunc {
	return h.oauthCallback(cfg, relay, googleCallbackPath, "Google OAuth", fetchGoogleUserInfo)
}

// HandleMicrosoftCallback handles the OAuth callback from Microsoft Entra ID.
func (h *Handler) HandleMicrosoftCallback(cfg *oauth2.Config, relay OAuthRelay) http.HandlerFunc {
	return h.oauthCallback(cfg, relay, microsoftCallbackPath, "Microsoft OAuth", fetchMicrosoftUserInfo)
}

// oauthLogin is the provider-agnostic sign-in redirect: generate CSRF state
// (optionally carrying an invite token, and this deployment's host when the
// redirect URI belongs to another one), stash it in an HttpOnly cookie, and
// bounce to the provider's consent page. 404s if the provider isn't configured.
func (h *Handler) oauthLogin(cfg *oauth2.Config, relay OAuthRelay, providerName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg == nil {
			httputil.NotFound(w, providerName)
			return
		}

		stateBytes := make([]byte, 32)
		if _, err := rand.Read(stateBytes); err != nil {
			httputil.ServerError(w, "generate", "state")
			return
		}
		state := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(stateBytes)

		// If an invite token is present, encode it in the state
		if inviteToken := r.URL.Query().Get("invite"); inviteToken != "" {
			state = state + ":" + inviteToken
		}

		// The provider will redirect to another host, which needs to know where
		// to forward the code back to. Cookie and provider get the same string,
		// so the callback's equality check still holds.
		if relay.stamp() {
			state = stampStateHost(state, relay.selfHost())
		}

		http.SetCookie(w, &http.Cookie{
			Name:     oauthStateCookie,
			Value:    state,
			Path:     "/",
			MaxAge:   600, // 10 minutes
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		http.Redirect(w, r, cfg.AuthCodeURL(state), http.StatusTemporaryRedirect)
	}
}

// oauthCallback is the provider-agnostic OAuth callback: relay the code onward
// if the state names another of our hosts, otherwise validate CSRF state,
// exchange the code, fetch the identity via the provider's fetcher, then run
// the shared find-or-create-identity → materialize-memberships → land flow.
// Only `fetch` differs between Google and Microsoft.
func (h *Handler) oauthCallback(cfg *oauth2.Config, relay OAuthRelay, callbackPath, providerName string, fetch userInfoFetcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg == nil {
			httputil.NotFound(w, providerName)
			return
		}

		// A state naming another host means the flow started there and this
		// deployment only owns the registered redirect URI. Forward the code and
		// stop — no cookie to check (it belongs to the other host), no identity
		// resolved and no token minted here.
		stateParam := r.URL.Query().Get("state")
		stateHost, stateBody := splitStateHost(stateParam)
		if stateHost != "" && stateHost != relay.selfHost() {
			target, ok := relay.forwardTarget(stateHost, callbackPath)
			if !ok {
				logging.Warn(r.Context(), nil, "refused to relay OAuth callback to an unrecognized host", "oauth-relay-refused",
					slog.String("oauth.relay_host", stateHost))
				http.Redirect(w, r, h.baseURL+"/login?error=invalid_state", http.StatusTemporaryRedirect)
				return
			}
			logging.Info(r.Context(), "relaying OAuth callback to the host that started the flow", "oauth-relay-forward",
				slog.String("oauth.relay_host", stateHost))
			http.Redirect(w, r, target+"?"+relayedQuery(r.URL.Query()).Encode(), http.StatusTemporaryRedirect)
			return
		}

		// Validate state
		stateCookie, err := r.Cookie(oauthStateCookie)
		if err != nil || stateCookie.Value == "" {
			http.Redirect(w, r, h.baseURL+"/login?error=invalid_state", http.StatusTemporaryRedirect)
			return
		}
		if stateParam != stateCookie.Value {
			http.Redirect(w, r, h.baseURL+"/login?error=invalid_state", http.StatusTemporaryRedirect)
			return
		}

		// Clear state cookie
		http.SetCookie(w, &http.Cookie{
			Name:   oauthStateCookie,
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})

		// Extract optional invite token from state
		var inviteToken string
		if idx := strings.Index(stateBody, ":"); idx > 0 {
			inviteToken = stateBody[idx+1:]
		}

		// Exchange code for token
		code := r.URL.Query().Get("code")
		oauthToken, err := cfg.Exchange(r.Context(), code)
		if err != nil {
			http.Redirect(w, r, h.baseURL+"/login?error=oauth_failed", http.StatusTemporaryRedirect)
			return
		}

		// Fetch user info from the provider
		providerUser, err := fetch(r.Context(), cfg, oauthToken)
		if err != nil {
			http.Redirect(w, r, h.baseURL+"/login?error=oauth_failed", http.StatusTemporaryRedirect)
			return
		}
		// Normalize the email the provider returned — they typically lowercase
		// already, but be defensive (and consistent with form-based login).
		providerUser.Email = normalizeEmail(providerUser.Email)

		// An explicit invite link must be redeemed by the invited address: a
		// forwarded or leaked link must never attach an org to a different
		// identity. Bounce back to the invite page with a clear error instead of
		// silently signing them in without the membership.
		if inviteToken != "" {
			if inv, invErr := h.queries.GetPendingUnexpiredInviteByToken(r.Context(), inviteToken); invErr == nil && inv.Email != providerUser.Email {
				http.Redirect(w, r, h.baseURL+"/invite/"+inviteToken+"?error=email_mismatch", http.StatusTemporaryRedirect)
				return
			}
		}

		// Resolve the identity: find the existing user, or create one. ANY
		// account may sign in with their Google/Microsoft
		// account. Whether they can actually *do*
		// anything is governed by org membership downstream — an identity with
		// no orgs lands on the empty picker (where they can create a new org),
		// not rejected.
		user, err := h.queries.GetUserByEmail(r.Context(), providerUser.Email)
		var userID int64
		switch {
		case err == nil:
			userID = user.ID
		case errors.Is(err, pgx.ErrNoRows):
			name := providerUser.Name
			if name == "" {
				name = providerUser.Email
			}
			newUser, cErr := h.queries.CreateUser(r.Context(), db.CreateUserParams{
				Email:        providerUser.Email,
				PasswordHash: nil, // OAuth identity — no password
				Name:         name,
			})
			if cErr != nil {
				http.Redirect(w, r, h.baseURL+"/login?error=create_failed", http.StatusTemporaryRedirect)
				return
			}
			userID = newUser.ID
		default:
			http.Redirect(w, r, h.baseURL+"/login?error=server_error", http.StatusTemporaryRedirect)
			return
		}

		// Grant any org access the identity is entitled to (invites + domain).
		h.materializeMemberships(r.Context(), userID, providerUser.Email, inviteToken)

		// Branch on how many orgs the identity can now enter.
		token, redirect, err := h.tokenForLanding(r.Context(), userID)
		if err != nil {
			http.Redirect(w, r, h.baseURL+"/login?error=token_failed", http.StatusTemporaryRedirect)
			return
		}
		setTokenCookie(w, token)
		http.Redirect(w, r, h.baseURL+redirect, http.StatusTemporaryRedirect)
	}
}

// HandleDevLogin provides a one-click dev login, then runs the same
// org-resolution as Google: 1 org → tenant token, else identity token + the
// org list for the picker. DEV-ONLY (404 in prod).
//
// Optional JSON body (dev/test tooling):
//   - {"email": "someone@x"} — dev-login AS an existing seeded identity (e.g. a
//     vendor) instead of the default seed owner.
//   - {"tenant_name": "Foo"} — provision a throwaway tenant + owner (identity =
//     the given/default email) and return a tenant token for it. Used by
//     cross-tenant E2E tests.
func (h *Handler) HandleDevLogin(w http.ResponseWriter, r *http.Request) {
	if !h.devAutoLogin {
		httputil.NotFound(w, "endpoint")
		return
	}

	var req struct {
		Email      string `json:"email"`
		TenantName string `json:"tenant_name"`
	}
	_ = httputil.Decode(r, &req) // body optional
	email := normalizeEmail(req.Email)
	if email == "" {
		email = seedid.OwnerEmail
	}

	if req.TenantName != "" {
		h.devProvisionTenant(w, r, email, req.TenantName)
		return
	}

	user, err := h.queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		// Dev/test: create the identity on the fly so dev-login can act as an
		// invited-but-not-yet-joined email — materializeMemberships then accepts
		// the pending invite into a membership.
		created, cErr := h.queries.CreateUser(r.Context(), db.CreateUserParams{Email: email, Name: email})
		if cErr != nil {
			httputil.ServerError(w, "find", "seed user")
			return
		}
		user = created
	}
	h.materializeMemberships(r.Context(), user.ID, user.Email, "")
	h.respondLoggedIn(w, r.Context(), user)
}

// devProvisionTenant creates a fresh tenant + owner identity (dev/test only)
// and returns a tenant-scoped token — the primitive cross-tenant E2E tests use
// to spin up an isolated second org.
func (h *Handler) devProvisionTenant(w http.ResponseWriter, r *http.Request, email, tenantName string) {
	tenant, err := createTenantWithSlugRetry(r.Context(), tenantName, func(slug string) (db.Tenant, error) {
		return h.queries.CreateTenant(r.Context(), db.CreateTenantParams{Name: tenantName, Slug: slug})
	})
	if err != nil {
		httputil.ServerError(w, "create", "tenant")
		return
	}
	// Same provisioning as the real signup paths — E2E spins up isolated orgs
	// through here, and one without the built-in engine behaves differently from
	// a genuine new org, which is exactly what those tests exist to catch.
	if err := mtdefaults.EnsureForTenant(r.Context(), h.queries, tenant.ID); err != nil {
		httputil.ServerError(w, "create", "tenant")
		return
	}
	// The org's starting dashboards, in the same transaction as everything else
	// it is provisioned with. A new org opening an empty Dashboards page learns
	// nothing about what the product can measure.
	if err := dashboarddefaults.InstallDefaults(r.Context(), h.queries, tenant.ID, nil); err != nil {
		httputil.ServerError(w, "create", "tenant")
		return
	}

	user, err := h.queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		created, cErr := h.queries.CreateUser(r.Context(), db.CreateUserParams{Email: email, Name: email})
		if cErr != nil {
			httputil.ServerError(w, "create", "user")
			return
		}
		user = created
	}
	if err := h.queries.CreateMembership(r.Context(), db.CreateMembershipParams{
		UserID:   user.ID,
		TenantID: tenant.ID,
		Role:     RoleOwner,
	}); err != nil {
		httputil.ServerError(w, "create", "membership")
		return
	}
	if err := ensureDefaultWorkflow(r.Context(), h.queries, tenant.ID); err != nil {
		httputil.ServerError(w, "create", "default workflow")
		return
	}

	token, err := GenerateToken(user.ID, tenant.ID, RoleOwner, h.jwtSecret, tokenExpiry)
	if err != nil {
		httputil.ServerError(w, "generate", "token")
		return
	}
	if err := h.queries.SetLastTenant(r.Context(), db.SetLastTenantParams{ID: user.ID, LastTenantID: &tenant.ID}); err != nil {
		httputil.ServerError(w, "update", "user")
		return
	}
	setTokenCookie(w, token)
	httputil.JSON(w, http.StatusOK, map[string]any{
		"token":               token,
		"needs_org_selection": false,
		"user": map[string]any{
			"id":        user.ID,
			"email":     user.Email,
			"name":      user.Name,
			"tenant_id": tenant.ID,
			"role":      RoleOwner,
		},
	})
}

// HandleAuthConfig returns which auth methods are available (public endpoint).
func (h *Handler) HandleAuthConfig(googleEnabled, microsoftEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httputil.JSON(w, http.StatusOK, map[string]any{
			"google_enabled":    googleEnabled,
			"microsoft_enabled": microsoftEnabled,
			"dev_login_enabled": h.devAutoLogin,
			// Email+password login. Always on: password accounts are
			// created via invites, which exist regardless of OAuth config.
			"password_enabled": true,
		})
	}
}

func fetchGoogleUserInfo(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token) (*oauthUser, error) {
	client := cfg.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeGoogleUser(resp.Body)
}

// decodeGoogleUser parses a Google userinfo response. An address Google has
// not verified is refused: it proves nothing about who owns the mailbox.
func decodeGoogleUser(r io.Reader) (*oauthUser, error) {
	var info struct {
		Email         string `json:"email"`
		VerifiedEmail bool   `json:"verified_email"`
		Name          string `json:"name"`
	}
	if err := json.NewDecoder(r).Decode(&info); err != nil {
		return nil, err
	}
	if info.Email == "" {
		return nil, errors.New("no email in Google response")
	}
	if !info.VerifiedEmail {
		return nil, errors.New("google email is not verified")
	}
	return &oauthUser{Email: info.Email, Name: info.Name}, nil
}

// fetchMicrosoftUserInfo reads the identity from the ID token of the code
// exchange. Graph `mail` and `userPrincipalName` are never used: any Entra
// tenant admin can set them to an address their tenant does not own.
func fetchMicrosoftUserInfo(_ context.Context, _ *oauth2.Config, token *oauth2.Token) (*oauthUser, error) {
	raw, _ := token.Extra("id_token").(string)
	return decodeMicrosoftIDToken(raw)
}

// decodeMicrosoftIDToken reads the claims of an Entra ID token and accepts the
// `email` claim only when `xms_edov` says the tenant owns its domain. The
// signature is not checked: the token comes straight from Microsoft's token
// endpoint over TLS in the code exchange, never from the browser.
func decodeMicrosoftIDToken(raw string) (*oauthUser, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("microsoft ID token is missing or malformed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims struct {
		Email   string          `json:"email"`
		Name    string          `json:"name"`
		EmailOK json.RawMessage `json:"xms_edov"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	if claims.Email == "" {
		return nil, errors.New("no email claim in Microsoft ID token")
	}
	if !claimTrue(claims.EmailOK) {
		return nil, errors.New("microsoft email domain is not verified (xms_edov)")
	}
	return &oauthUser{Email: claims.Email, Name: claims.Name}, nil
}

// claimTrue reports whether a boolean token claim is set. Entra may encode
// it as a JSON bool, a string, or 1.
func claimTrue(v json.RawMessage) bool {
	switch strings.ToLower(strings.Trim(string(v), `"`)) {
	case "true", "1":
		return true
	}
	return false
}
