package auth

import (
	"context"
	"net/http"

	"openloc/internal/httputil"

	db "openloc/internal/database/sqlcgen"
)

// Email+password login. Password accounts are created only via
// invite acceptance or the reset flow — both prove address ownership, keeping
// the email-is-identity invariant OAuth relies on.

// HandlePasswordLogin authenticates an email+password pair and starts a
// session exactly like the OAuth callback / dev login: materialize any
// pending memberships, then land based on the identity's org count.
//
// Every failure mode — unknown email, OAuth-only account (no hash),
// deactivated user, wrong password — returns the same 401 body, and paths
// with nothing to compare burn a dummy bcrypt verify so response timing
// doesn't reveal account existence.
func (h *Handler) HandlePasswordLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	email := normalizeEmail(req.Email)
	if email == "" || req.Password == "" {
		httputil.BadRequest(w, "email and password are required")
		return
	}

	user, err := h.queries.GetUserByEmail(r.Context(), email)
	if err != nil || !userHasPassword(user) || !user.IsActive {
		equalizeTiming(req.Password)
		h.rejectLogin(w)
		return
	}
	if VerifyPassword(*user.PasswordHash, req.Password) != nil {
		h.rejectLogin(w)
		return
	}

	// Same post-auth path as OAuth: pick up pending invites + domain auto-join.
	h.materializeMemberships(r.Context(), user.ID, user.Email, "")
	h.respondLoggedIn(w, r.Context(), user)
}

// rejectLogin writes the uniform password-login failure. One body for every
// cause so the endpoint can't be used to enumerate accounts.
func (h *Handler) rejectLogin(w http.ResponseWriter) {
	httputil.Error(w, http.StatusUnauthorized, "invalid email or password")
}

// respondLoggedIn mints the right token for the identity's current org count,
// sets the session cookie, and writes the login JSON the SPA expects — the
// same shape for password login, invite accept, password reset, and dev login:
//   - 1 org  → tenant token, needs_org_selection=false, user incl. tenant/role
//   - else   → identity token, needs_org_selection=true, orgs list for picker
func (h *Handler) respondLoggedIn(w http.ResponseWriter, ctx context.Context, user db.User) {
	orgs, err := h.queries.ListMembershipsWithTenant(ctx, user.ID)
	if err != nil {
		httputil.ServerError(w, "list", "organizations")
		return
	}

	if len(orgs) == 1 {
		only := orgs[0]
		token, err := GenerateToken(user.ID, only.TenantID, only.Role, h.jwtSecret, tokenExpiry)
		if err != nil {
			httputil.ServerError(w, "generate", "token")
			return
		}
		if err := h.queries.SetLastTenant(ctx, db.SetLastTenantParams{ID: user.ID, LastTenantID: &only.TenantID}); err != nil {
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
				"tenant_id": only.TenantID,
				"role":      only.Role,
			},
		})
		return
	}

	token, err := GenerateIdentityToken(user.ID, h.jwtSecret)
	if err != nil {
		httputil.ServerError(w, "generate", "token")
		return
	}
	setTokenCookie(w, token)
	httputil.JSON(w, http.StatusOK, map[string]any{
		"token":               token,
		"needs_org_selection": true,
		"orgs":                orgs,
		"user": map[string]any{
			"id":    user.ID,
			"email": user.Email,
			"name":  user.Name,
		},
	})
}
