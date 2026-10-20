package auth

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/tenantdelete"
)

const tokenExpiry = 24 * time.Hour
const cookieName = "openloc_token"

// EmailSender delivers transactional emails (org-signup notifications).
// Satisfied by notification.Sender (SMTPSender or GmailSender, ConsoleSender
// in dev) — declared locally so the auth package doesn't depend on the
// notification package. nil disables signup emails.
type EmailSender interface {
	SendEmail(ctx context.Context, to, subject string, body emaillayout.Email) error
}

// normalizeEmail lowercases and trims an email so that "Foo@Bar.com" and
// "  foo@bar.com  " resolve to the same identity. Per RFC 5321 §2.4 the domain
// is case-insensitive and the local-part SHOULD be treated case-insensitively
// in practice; every major TMS and web app does this. Applied at every entry
// point that compares emails (OAuth, dev-login, profile update).
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Handler handles authentication endpoints.
type Handler struct {
	queries        db.Querier
	pool           *pgxpool.Pool // for multi-step signup transactions; nil in unit tests that don't hit signup
	jwtSecret      string
	baseURL        string
	devAutoLogin   bool
	sender         EmailSender               // nil disables signup emails
	platformAdmins EmailSet                  // operator emails (PLATFORM_ADMIN_EMAILS)
	demoOrg        bool                      // the Playground is on (DEMO_ORG)
	deleteQueue    tenantdelete.TaskEnqueuer // nil: an organization delete runs in the request
}

func NewHandler(q db.Querier, pool *pgxpool.Pool, jwtSecret, baseURL string, devAutoLogin bool, sender EmailSender, platformAdminEmails []string) *Handler {
	return &Handler{
		queries:        q,
		pool:           pool,
		jwtSecret:      jwtSecret,
		baseURL:        baseURL,
		devAutoLogin:   devAutoLogin,
		sender:         sender,
		platformAdmins: NewEmailSet(platformAdminEmails),
	}
}

// SetDemoOrg turns the Playground on. Off (the default), entering it is refused
// even when a demo organization is still in the database.
func (h *Handler) SetDemoOrg(on bool) {
	h.demoOrg = on
}

// IsPlatformAdmin reports whether an email is a configured platform operator
// (PLATFORM_ADMIN_EMAILS). Case-insensitive.
func (h *Handler) IsPlatformAdmin(email string) bool {
	return h.platformAdmins.Has(email)
}

// meResponse builds the /auth/me payload. It's identity-aware: the
// org fields are populated only when the caller holds a tenant-scoped token
// (an org has been selected). An identity-scoped token, or a token of an org
// being deleted, reports needs_org_selection=true and the client routes to
// the org picker.
func (h *Handler) meResponse(r *http.Request, user db.User) map[string]any {
	claims, _ := ClaimsFromContext(r.Context())
	inOrg := claims != nil && claims.Scope == ScopeTenant && !h.orgDeleting(r, claims.TenantID)
	resp := map[string]any{
		"id":                  user.ID,
		"email":               user.Email,
		"name":                user.Name,
		"needs_org_selection": !inOrg,
		"is_platform_admin":   h.IsPlatformAdmin(user.Email),
	}
	// An operator viewing an organization keeps the flag even once it is being
	// deleted, so the app still offers the way back to the console.
	if claims != nil && claims.Impersonated {
		resp["impersonating"] = true
		resp["is_platform_admin"] = false
	}
	if inOrg {
		resp["tenant_id"] = claims.TenantID
		resp["role"] = claims.Role
		// Operator "logged in as" this org. The SPA hides the platform
		// surface + shows an impersonation banner while true.
		resp["impersonating"] = claims.Impersonated
		if t, err := h.queries.GetTenant(r.Context(), claims.TenantID); err == nil {
			resp["tenant_name"] = t.Name
			resp["tenant_slug"] = t.Slug
			// Signal a not-yet-approved org so the SPA routes to the
			// blocked screen instead of the app. /me is identity-scoped so it
			// keeps working while the org is gated, unlike app routes which the
			// pending-gate 403s. tenant_status lets the screen tailor its copy
			// (under review vs disabled).
			resp["org_pending"] = t.Status != statusApproved
			resp["tenant_status"] = t.Status
		}
		// A demo visit: the profile is the demo guide's;
		// demo_return_org_name names the org that leaving the demo returns to.
		if claims.Demo {
			resp["demo"] = true
			resp["is_platform_admin"] = false
			if claims.RealTenantID != 0 {
				if t, err := h.queries.GetTenant(r.Context(), claims.RealTenantID); err == nil {
					resp["demo_return_org_name"] = t.Name
				}
			}
		}
		// Operator "logged in as" this org: present the session AS the
		// org — show its owner as the signed-in identity and drop the platform
		// flag, so everything reads like that org is logged in. The amber banner
		// remains the one tell, and `id` stays the operator's user so actions are
		// still audited under them (only the display identity is swapped).
		if claims.Impersonated {
			resp["is_platform_admin"] = false
			if owner, err := h.queries.GetTenantOwner(r.Context(), claims.TenantID); err == nil {
				resp["name"] = owner.Name
				resp["email"] = owner.Email
			}
		}
	}
	return resp
}

// HandleGetMe returns the current authenticated identity's profile (+ current
// org context when one is selected). Served under RequireIdentity so the org
// picker can render before an org is chosen.
func (h *Handler) HandleGetMe(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	user, err := h.queries.GetUser(r.Context(), userID)
	if err != nil {
		httputil.ServerError(w, "load", "user")
		return
	}
	httputil.Data(w, h.meResponse(r, user))
}

// maxUserNameLen caps a person's name, in characters. The name fields in
// Settings and on the invite page have the same maxLength.
const maxUserNameLen = 200

// nameTooLong answers 400 when name is past maxUserNameLen characters, and
// reports whether it did.
func nameTooLong(w http.ResponseWriter, name string) bool {
	if utf8.RuneCountInString(name) <= maxUserNameLen {
		return false
	}
	httputil.BadRequest(w, fmt.Sprintf("name must be at most %d characters", maxUserNameLen))
	return true
}

// HandleUpdateMe updates the current authenticated identity's profile. The
// email can't change: email-matched invites, domain auto-join and the
// platform-operator list all trust that it belongs to this user. `email` is
// still accepted when it equals the current address, so a form that sends the
// whole profile keeps working. An operator logged in as an org can't save:
// the form shows the org owner's profile, but the session's user is the
// operator, so a save would rename the operator.
func (h *Handler) HandleUpdateMe(w http.ResponseWriter, r *http.Request) {
	if claims, ok := ClaimsFromContext(r.Context()); ok && claims != nil && claims.Impersonated {
		httputil.Error(w, http.StatusForbidden, "the profile can't be changed while you are logged in as an organization")
		return
	}
	userID := UserIDFromContext(r.Context())

	var req struct {
		Name  *string `json:"name"`
		Email *string `json:"email"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	current, err := h.queries.GetUser(r.Context(), userID)
	if err != nil {
		httputil.ServerError(w, "load", "user")
		return
	}
	if req.Email != nil && normalizeEmail(*req.Email) != current.Email {
		httputil.BadRequest(w, "email cannot be changed")
		return
	}

	name := current.Name
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
		if name == "" {
			httputil.BadRequest(w, "name cannot be empty")
			return
		}
		if nameTooLong(w, name) {
			return
		}
	}
	user, err := h.queries.UpdateUserProfile(r.Context(), db.UpdateUserProfileParams{
		ID:   userID,
		Name: name,
	})
	if err != nil {
		logging.Error(r.Context(), err, "updating the profile failed", "auth.update_me_failed")
		httputil.ServerError(w, "update", "profile")
		return
	}

	httputil.Data(w, h.meResponse(r, user))
}

// HandleListOrgs returns every org the authenticated identity can enter (the
// Slack-style workspace list). It first idempotently materializes any org
// access the identity is entitled to — pending invites and email-domain
// auto-join — so a returning user picks up newly-granted orgs without
// re-authenticating. Served under RequireIdentity.
func (h *Handler) HandleListOrgs(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	user, err := h.queries.GetUser(r.Context(), userID)
	if err != nil {
		httputil.ServerError(w, "load", "user")
		return
	}
	h.materializeMemberships(r.Context(), userID, user.Email, "")

	orgs, err := h.queries.ListMembershipsWithTenant(r.Context(), userID)
	if err != nil {
		httputil.ServerError(w, "list", "organizations")
		return
	}
	httputil.Data(w, orgs)
}

// HandleSelectOrg mints a tenant-scoped token for one of the identity's orgs.
// 404 if the identity isn't a member of the requested tenant (don't leak
// existence). Served under RequireIdentity — an identity token calls this to
// exchange for a tenant token.
func (h *Handler) HandleSelectOrg(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	tenantID, err := httputil.ParseID(r, "tenantID")
	if err != nil {
		httputil.BadRequest(w, "invalid organization ID")
		return
	}

	membership, err := h.queries.GetMembership(r.Context(), db.GetMembershipParams{
		UserID:   userID,
		TenantID: tenantID,
	})
	if err != nil || h.orgDeleting(r, tenantID) {
		httputil.NotFound(w, "organization")
		return
	}

	token, err := GenerateToken(userID, tenantID, membership.Role, h.jwtSecret, tokenExpiry)
	if err != nil {
		httputil.ServerError(w, "generate", "token")
		return
	}
	// Remember this as the identity's most recent org (defaults the picker).
	if err := h.queries.SetLastTenant(r.Context(), db.SetLastTenantParams{ID: userID, LastTenantID: &tenantID}); err != nil {
		httputil.ServerError(w, "update", "user")
		return
	}
	setTokenCookie(w, token)

	tenant, err := h.queries.GetTenant(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "load", "organization")
		return
	}
	user, err := h.queries.GetUser(r.Context(), userID)
	if err != nil {
		httputil.ServerError(w, "load", "user")
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user": map[string]any{
			"id":        user.ID,
			"email":     user.Email,
			"name":      user.Name,
			"tenant_id": tenantID,
			"role":      membership.Role,
		},
		"tenant": map[string]any{
			"id":   tenant.ID,
			"name": tenant.Name,
			"slug": tenant.Slug,
		},
	})
}

// devSwitchableRoles is the set the dev role switcher can assume.
var devSwitchableRoles = map[string]bool{
	RoleOwner: true, RoleAdmin: true, RoleProjectManager: true, RoleLinguist: true,
}

// HandleDevSetRole lets the current user switch their OWN role in the CURRENT
// org, bypassing the normal admin/hierarchy guards. DEV-ONLY — returns 404
// unless dev auto-login is enabled. Powers the header role switcher used to
// test role-gated UI locally. Role lives on the membership and in the
// JWT, so we persist the membership role AND mint a fresh tenant token.
func (h *Handler) HandleDevSetRole(w http.ResponseWriter, r *http.Request) {
	if !h.devAutoLogin {
		httputil.Error(w, http.StatusNotFound, "not found")
		return
	}
	userID := UserIDFromContext(r.Context())
	tenantID := TenantIDFromContext(r.Context())

	var req struct {
		Role string `json:"role"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if !devSwitchableRoles[req.Role] {
		httputil.BadRequest(w, "invalid role")
		return
	}

	if err := h.queries.UpdateMembershipRole(r.Context(), db.UpdateMembershipRoleParams{
		UserID:   userID,
		TenantID: tenantID,
		Role:     req.Role,
	}); err != nil {
		httputil.ServerError(w, "update", "role")
		return
	}
	token, err := GenerateToken(userID, tenantID, req.Role, h.jwtSecret, tokenExpiry)
	if err != nil {
		httputil.ServerError(w, "generate", "token")
		return
	}
	setTokenCookie(w, token)
	httputil.JSON(w, http.StatusOK, map[string]any{"token": token, "role": req.Role})
}

// HandleLogout clears the auth cookie.
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	httputil.NoContent(w)
}

// HandleListMembers returns members of the current org.
//
// By default returns ACTIVE users only. Pass `?include_inactive=true` to
// include globally-deactivated identities (rare — per-org "remove member"
// deletes the membership rather than deactivating the account).
func (h *Handler) HandleListMembers(w http.ResponseWriter, r *http.Request) {
	tenantID := TenantIDFromContext(r.Context())
	includeInactive := r.URL.Query().Get("include_inactive") == "true"
	members, err := h.queries.ListMembersByTenant(r.Context(), db.ListMembersByTenantParams{
		TenantID:        tenantID,
		IncludeInactive: includeInactive,
	})
	if err != nil {
		httputil.ServerError(w, "list", "members")
		return
	}
	httputil.Data(w, members)
}

// HandleRemoveMember removes a user from the CURRENT org by deleting their
// membership and releasing their segment holds; see RemoveMember for the
// guards.
func (h *Handler) HandleRemoveMember(w http.ResponseWriter, r *http.Request) {
	userID, err := httputil.ParseID(r, "userID")
	if err != nil {
		httputil.BadRequest(w, "invalid user ID")
		return
	}
	if err := RemoveMember(r.Context(), h.pool, ActorFromContext(r.Context()), userID); err != nil {
		WriteMemberError(w, err)
		return
	}
	httputil.NoContent(w)
}

// HandleDeleteTenant closes the caller's organization: it marks it
// 'deleting', which shuts every member out at once, and the worker deletes
// its data in bounded steps (tenantdelete). Answers 202 Accepted. Members'
// global identities survive (users.last_tenant_id is SET NULL); only their
// membership in this org and this org's data go. Without a worker queue the
// delete runs in the request.
//
// Guards:
//   - Owner-only
//   - No "type the name to confirm" backend check (UI handles confirmation)
func (h *Handler) HandleDeleteTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := TenantIDFromContext(r.Context())
	if _, err := h.queries.MarkTenantDeleting(r.Context(), tenantID); err != nil {
		httputil.ServiceError(w, err, "delete", "tenant")
		return
	}
	if h.deleteQueue != nil {
		if err := tenantdelete.Enqueue(h.deleteQueue, tenantID); err != nil {
			// The sweep enqueues it again; the organization is closed already.
			logging.Error(r.Context(), err, "tenant delete not queued; the sweep retries it", "tenant-delete-enqueue-failed")
		}
	} else if err := tenantdelete.New(h.pool, nil).Delete(r.Context(), tenantID); err != nil {
		httputil.ServiceError(w, err, "delete", "tenant")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// SetTenantDeleteQueue makes organization deletes run on the worker.
func (h *Handler) SetTenantDeleteQueue(q tenantdelete.TaskEnqueuer) { h.deleteQueue = q }

// orgDeleting reports whether the organization is being deleted, which makes
// it gone for its members. A failed lookup counts as not deleting: the
// tenant gate on every app route still decides access.
func (h *Handler) orgDeleting(r *http.Request, tenantID int64) bool {
	t, err := h.queries.GetTenantStatus(r.Context(), tenantID)
	return err == nil && t.Status == tenantdelete.StatusDeleting
}

// setTokenCookie sets an HttpOnly cookie with the JWT token.
func setTokenCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(tokenExpiry.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify converts a name to a URL-friendly slug.
func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = nonAlphanumRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "org"
	}
	return s
}
