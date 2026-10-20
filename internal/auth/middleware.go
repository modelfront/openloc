package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// UserExists checks whether a user ID exists in the database and returns the
// user's email. The email is a free by-product — implementations already fetch
// the user row to answer the existence question — and feeds error-notice
// identity (auth.WithUserEmail → user_email in Honeybadger).
// The caller passes this function when constructing the middleware
// so the auth package doesn't need to import the db package.
//
// pgx.ErrNoRows means the user is gone (401); any other error means the
// check could not run (503).
type UserExists func(ctx context.Context, id int64) (email string, err error)

// ErrTenantNotApproved is TenantActive's answer for an organization that may
// not use the app.
var ErrTenantNotApproved = errors.New("auth: organization not approved")

// TenantActive reports whether a tenant is usable (approved). It returns
// ErrTenantNotApproved when the tenant is NOT approved — self-serve signups
// sit in 'pending' until a platform operator approves them, and 'rejected'
// stays gated — and pgx.ErrNoRows when it does not exist. The middleware
// turns those into a 403 with code "org_pending" so the SPA routes to the
// awaiting-approval screen rather than logging the user out; any other error
// means the check could not run (503). Passed as a callback so auth needn't
// import db; pass nil to disable the check (used by unit tests).
//
// demo reports the shared demo organization, which the
// middleware keeps read-only.
type TenantActive func(ctx context.Context, tenantID int64) (demo bool, err error)

// writeCheckFailed answers a request whose credential could not be checked
// because the database failed: 503, so the client keeps its session and
// retries, rather than the 401 that signs it out.
func writeCheckFailed(w http.ResponseWriter, r *http.Request, err error) {
	logging.Error(r.Context(), err, "credential check failed", "auth-check-failed")
	httputil.ErrorCode(w, http.StatusServiceUnavailable, "unavailable", "The service is busy. Try again in a moment.")
}

// writeUserLookupError answers a failed UserExists check: 401 when the user is
// gone, 503 when the check could not run.
func writeUserLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		httputil.Error(w, http.StatusUnauthorized, "user not found")
		return
	}
	writeCheckFailed(w, r, err)
}

// OAuthResolver resolves an OAuth access token. Same
// contract as PATResolver; wired from internal/oauthserver.
type OAuthResolver func(ctx context.Context, raw string) (*Claims, *PATInfo, error)

// Middleware returns a Chi-compatible middleware that validates JWT tokens
// and verifies the user still exists in the database.
// Checks Authorization header first, then falls back to the HttpOnly cookie.
//
// resolvePAT handles personal access tokens for machine clients. Pass
// nil to disable PAT authentication. The two credential paths are mutually
// exclusive: a bearer value carrying auth.PATPrefix is verified ONLY as a PAT
// and a failure is a flat 401 — it never falls through to JWT validation, so a
// bad PAT can't be probed against the JWT verifier.
func Middleware(jwtSecret string, userExists UserExists, tenantActive TenantActive, resolvePAT PATResolver, resolveOAuth OAuthResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Authorization header first, then HttpOnly cookie.
			token := tokenFromRequest(r)
			if token == "" {
				httputil.Error(w, http.StatusUnauthorized, "missing authorization")
				return
			}

			// PAT path — exclusive; never falls through to JWT.
			if IsPAT(token) {
				if resolvePAT == nil {
					httputil.Error(w, http.StatusUnauthorized, "access key authentication is not enabled")
					return
				}
				claims, info, err := resolvePAT(r.Context(), token)
				if err != nil {
					if !errors.Is(err, ErrInvalidPAT) {
						writeCheckFailed(w, r, err)
						return
					}
					httputil.Error(w, http.StatusUnauthorized, "invalid or expired access key")
					return
				}
				// Belt and braces. The resolver's query can only return a row
				// with a real tenant + membership role, so this should be
				// unreachable — but every downstream TenantIDFromContext caller
				// trusts a non-zero tenant, and that invariant is worth an
				// explicit guard rather than an implicit one.
				if claims.TenantID == 0 || claims.Role == "" {
					httputil.Error(w, http.StatusUnauthorized, "invalid or expired access key")
					return
				}
				r, ok := checkTenantActive(w, r, tenantActive, claims.TenantID, false)
				if !ok {
					return
				}
				if !patRequestAllowed(r, info) {
					WriteReadOnlyKey(w)
					return
				}
				// WithUserEmail: PAT requests carry the owner's email so error
				// notices show user_email, same as the session-JWT path below.
				ctx := WithUserEmail(WithPATInfo(WithClaims(r.Context(), claims), info), info.UserEmail)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// OAuth access-token path — exclusive like the
			// PAT path, and AUDIENCE-BOUND: these tokens exist to let Claude /
			// ChatGPT call /mcp on a user's behalf and are valid nowhere else.
			if strings.HasPrefix(token, OAuthAccessPrefix) {
				if resolveOAuth == nil {
					httputil.Error(w, http.StatusUnauthorized, "token authentication is not enabled")
					return
				}
				if r.URL.Path != "/mcp" {
					httputil.Error(w, http.StatusUnauthorized, "this token is only valid for the MCP endpoint")
					return
				}
				claims, info, err := resolveOAuth(r.Context(), token)
				if err != nil {
					if !errors.Is(err, ErrInvalidOAuthToken) {
						writeCheckFailed(w, r, err)
						return
					}
					httputil.Error(w, http.StatusUnauthorized, "invalid or expired token")
					return
				}
				if claims.TenantID == 0 || claims.Role == "" {
					httputil.Error(w, http.StatusUnauthorized, "invalid or expired token")
					return
				}
				r, ok := checkTenantActive(w, r, tenantActive, claims.TenantID, false)
				if !ok {
					return
				}
				ctx := WithUserEmail(WithPATInfo(WithClaims(r.Context(), claims), info), info.UserEmail)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			claims, err := ValidateToken(token, jwtSecret)
			if err != nil {
				httputil.Error(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			// Org-scope guard: every route behind this middleware is
			// org-scoped. An identity token (no org selected yet) must go pick an
			// org first. This single check is why the ~hundreds of downstream
			// TenantIDFromContext/RoleFromContext callers can trust a real tenant.
			if claims.Scope != ScopeTenant || claims.TenantID == 0 {
				httputil.Error(w, http.StatusForbidden, "select an organization")
				return
			}

			// Verify the user still exists — a deleted user's JWT should not work
			email, err := userExists(r.Context(), claims.UserID)
			if err != nil {
				writeUserLookupError(w, r, err)
				return
			}
			if claims.Demo {
				if _, err := userExists(r.Context(), claims.RealUserID); err != nil {
					writeUserLookupError(w, r, err)
					return
				}
			}

			// Pending-gate: a self-serve org sits in 'pending' until a
			// platform operator approves it. Checked per-request so approval takes
			// effect without a re-login.
			r, ok := checkTenantActive(w, r, tenantActive, claims.TenantID, claims.Demo)
			if !ok {
				return
			}

			ctx := WithUserEmail(WithClaims(r.Context(), claims), email)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// checkTenantActive runs the optional tenant-approval gate. Returns true to
// continue; on a non-approved tenant it writes a 403 org_pending and returns
// false. A nil callback (unit tests) always continues.
//
// It also holds the demo organization's read-only barrier: every credential
// path passes here, so no route can write to the demo org by accident.
//
// demoToken is a demo visit: its tenant goes away when the demo organization
// is rebuilt, which answers demo_reset so the app can enter the new one.
func checkTenantActive(w http.ResponseWriter, r *http.Request, tenantActive TenantActive, tenantID int64, demoToken bool) (*http.Request, bool) {
	if tenantActive == nil {
		return r, true
	}
	demo, err := tenantActive(r.Context(), tenantID)
	if err != nil && !errors.Is(err, ErrTenantNotApproved) && !errors.Is(err, pgx.ErrNoRows) {
		writeCheckFailed(w, r, err)
		return r, false
	}
	if err != nil && demoToken {
		httputil.ErrorCode(w, http.StatusForbidden, "demo_reset", "The Playground was refreshed.")
		return r, false
	}
	if err != nil {
		httputil.ErrorCode(w, http.StatusForbidden, "org_pending", "your organization is awaiting approval")
		return r, false
	}
	if demo {
		if !demoRequestAllowed(r) {
			writeDemoReadOnly(w)
			return r, false
		}
		r = r.WithContext(withDemo(r.Context()))
	}
	return r, true
}

// RequireIdentity validates the JWT and verifies the user exists, but does NOT
// require an org to be selected. Used only by the org-picker endpoints
// (/auth/orgs, /auth/orgs/{id}/select, /auth/me) — an identity-scoped OR a
// tenant-scoped token both pass. Everything else must use Middleware.
func RequireIdentity(jwtSecret string, userExists UserExists) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := tokenFromRequest(r)
			if token == "" {
				httputil.Error(w, http.StatusUnauthorized, "missing authorization")
				return
			}
			claims, err := ValidateToken(token, jwtSecret)
			if err != nil {
				httputil.Error(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}
			email, err := userExists(r.Context(), claims.UserID)
			if err != nil {
				writeUserLookupError(w, r, err)
				return
			}
			// A demo visit acts as the demo org's guide, so the identity routes
			// (profile, org list, new org, platform) would act on the guide:
			// only reading the profile and leaving the demo are allowed.
			if claims.Demo && !demoIdentityAllowed(r) {
				writeDemoReadOnly(w)
				return
			}
			ctx := WithUserEmail(WithClaims(r.Context(), claims), email)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Optional runs mw only when the request carries a credential. A request
// without one passes through anonymous (empty role); a request with a bad one
// still gets mw's 401. For content that is public but filtered per role.
func Optional(mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		authed := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tokenFromRequest(r) == "" {
				next.ServeHTTP(w, r)
				return
			}
			authed.ServeHTTP(w, r)
		})
	}
}

// tokenFromRequest extracts the JWT from the Authorization header, falling back
// to the HttpOnly cookie. Shared by Middleware and RequireIdentity.
func tokenFromRequest(r *http.Request) string {
	if header := r.Header.Get("Authorization"); header != "" {
		if t, ok := strings.CutPrefix(header, "Bearer "); ok && t != "" {
			return t
		}
	}
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

// RequireRole returns middleware that checks the user's role from JWT claims.
// Returns 403 if the user's role is not in the allowed set.
// Must be chained after Middleware (which populates claims in context).
func RequireRole(allowed ...string) func(http.Handler) http.Handler {
	set := make(map[string]bool, len(allowed))
	for _, r := range allowed {
		set[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := RoleFromContext(r.Context())
			if !set[role] {
				httputil.Error(w, http.StatusForbidden, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
