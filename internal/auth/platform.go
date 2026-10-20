package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// platformSettableStatuses are the statuses an operator may set an org to.
// 'pending' is not settable — it's the initial state only.
var platformSettableStatuses = map[string]bool{
	statusApproved:  true, // approve a signup / re-enable a disabled org
	statusRejected:  true, // decline a pending signup
	statusSuspended: true, // disable a previously-approved org
}

// RequirePlatformAdmin gates the /api/platform/* operator surface.
// Chain AFTER RequireIdentity — it reads the caller's identity from context,
// loads their email, and 403s unless it's in PLATFORM_ADMIN_EMAILS. These are
// low-traffic operator routes, so the per-request user lookup is fine.
func (h *Handler) RequirePlatformAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An impersonation token (operator "logged in as" an org) must not reach
		// the platform surface — you can't manage the platform while acting as a
		// tenant. Exit the impersonation first.
		if claims, ok := ClaimsFromContext(r.Context()); ok && claims != nil && claims.Impersonated {
			httputil.Error(w, http.StatusForbidden, "not available while viewing an organization")
			return
		}
		userID := UserIDFromContext(r.Context())
		user, err := h.queries.GetUser(r.Context(), userID)
		if err != nil {
			httputil.ServerError(w, "load", "user")
			return
		}
		if !h.IsPlatformAdmin(user.Email) {
			httputil.Error(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// platformOrgRow is a listing row plus is_self — whether the current operator
// is a member of that org. The SPA uses it to hide the Open/Disable actions for
// the operator's own workspace. The embedded row flattens into the JSON object.
type platformOrgRow struct {
	db.ListTenantsForPlatformRow
	IsSelf bool `json:"is_self"`
}

// HandleListPlatformOrgs returns every organization with its status + owner,
// pending first, each flagged is_self for the calling operator. The operator
// console for approving signups and enabling/disabling existing orgs.
func (h *Handler) HandleListPlatformOrgs(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.queries.ListTenantsForPlatform(r.Context())
	if err != nil {
		httputil.ServerError(w, "list", "organizations")
		return
	}
	// Which orgs the operator belongs to (best-effort — a lookup failure just
	// means no self flags, never a broken list).
	self := map[int64]bool{}
	operatorID := UserIDFromContext(r.Context())
	if memberships, mErr := h.queries.ListMembershipsWithTenant(r.Context(), operatorID); mErr == nil {
		for _, m := range memberships {
			self[m.TenantID] = true
		}
	}
	rows := make([]platformOrgRow, len(orgs))
	for i, o := range orgs {
		rows[i] = platformOrgRow{ListTenantsForPlatformRow: o, IsSelf: self[o.TenantID]}
	}
	httputil.Data(w, rows)
}

// HandleSetOrgStatus is the single operator lever over an org's lifecycle:
// approve (→approved), reject (→rejected), disable (→suspended), or
// re-enable (→approved). A non-approved status is gated out of the app by
// auth.Middleware and takes effect on the org's next request (no re-login).
// The owner is emailed on the change.
func (h *Handler) HandleSetOrgStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, err := httputil.ParseID(r, "tenantID")
	if err != nil {
		httputil.BadRequest(w, "invalid organization ID")
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if !platformSettableStatuses[req.Status] {
		httputil.BadRequest(w, "status must be 'approved', 'rejected', or 'suspended'")
		return
	}

	// The demo organization's status is the server's to manage, never an operator's.
	if t, err := h.queries.GetTenant(r.Context(), tenantID); err == nil && t.Kind == TenantKindDemo {
		httputil.ErrorCode(w, http.StatusConflict, "demo_org", "the Playground is managed by the server")
		return
	}
	// Don't let an operator disable an org they belong to — that would lock them
	// (and their colleagues) out of their own workspace. Enabling/approving an
	// org you're in is harmless, so only the disabling transitions are guarded.
	if req.Status == statusSuspended || req.Status == statusRejected {
		operatorID := UserIDFromContext(r.Context())
		if _, err := h.queries.GetMembership(r.Context(), db.GetMembershipParams{UserID: operatorID, TenantID: tenantID}); err == nil {
			httputil.ErrorCode(w, http.StatusConflict, "own_org", "you can't disable an organization you belong to")
			return
		}
	}

	tenant, err := h.queries.SetTenantStatus(r.Context(), db.SetTenantStatusParams{ID: tenantID, Status: req.Status})
	if err != nil {
		if httputil.DBError(w, err, "organization") {
			return
		}
		return
	}

	// Notify the owner, best-effort. A platform-created org whose owner invite
	// hasn't been accepted yet has no owner membership — the status
	// change must still work, there's just nobody to email.
	if owner, oErr := h.queries.GetTenantOwner(r.Context(), tenantID); oErr == nil {
		switch req.Status {
		case statusApproved:
			h.notifyOwnerApproved(r.Context(), owner.Email, tenant.Name)
		case statusSuspended:
			h.notifyOwnerDisabled(r.Context(), owner.Email, tenant.Name)
		case statusRejected:
			h.notifyOwnerRejected(r.Context(), owner.Email, tenant.Name)
		}
	}
	httputil.Data(w, map[string]any{"tenant_id": tenant.ID, "status": tenant.Status})
}

// ownerInviteExpiry matches the tenant-facing invite expiry (invite package).
// Expiry only guards the shareable token link — the email-matched auto-accept
// at sign-in stays durable.
const ownerInviteExpiry = 7 * 24 * time.Hour

// HandlePlatformCreateOrg provisions an org for a customer:
// POST /api/platform/orgs {name, owner_email, domain?}. The tenant starts
// APPROVED — an operator created it deliberately; the pending/approval loop
// only gates self-serve signups. The owner is seated via an
// owner-role invite (works for new and existing identities alike): a new user
// sets a password on the invite page, an existing user just signs in and the
// membership materializes. The invite row is inserted directly here — the
// tenant-facing invite endpoint keeps its owner-role rejection.
func (h *Handler) HandlePlatformCreateOrg(w http.ResponseWriter, r *http.Request) {
	operatorID := UserIDFromContext(r.Context())

	var req struct {
		Name       string `json:"name"`
		OwnerEmail string `json:"owner_email"`
		Domain     string `json:"domain"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httputil.BadRequest(w, "organization name is required")
		return
	}
	ownerEmail := normalizeEmail(req.OwnerEmail)
	if ownerEmail == "" || !strings.Contains(ownerEmail, "@") {
		httputil.BadRequest(w, "a valid owner email is required")
		return
	}

	// Optional domain — same rules as self-serve signup: no free-email
	// providers, one org per domain.
	var domain *string
	if d := strings.ToLower(strings.TrimSpace(req.Domain)); d != "" {
		if IsFreeEmailDomain(d) {
			httputil.ErrorCode(w, http.StatusBadRequest, "free_email",
				"personal email providers can't be an organization domain")
			return
		}
		if _, err := h.queries.GetTenantByDomain(r.Context(), &d); err == nil {
			httputil.ErrorCode(w, http.StatusConflict, "domain_taken",
				"an organization for this domain already exists")
			return
		}
		domain = &d
	}

	token, err := newAuthToken()
	if err != nil {
		httputil.ServerError(w, "generate", "invite token")
		return
	}

	// Tenant, its defaults and the owner invite in one transaction.
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "create", "organization")
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)

	tenant, err := provisionApprovedTenant(r.Context(), q, name, domain)
	if err != nil {
		what := "organization"
		var pe *provisionError
		if errors.As(err, &pe) {
			what = pe.what
		}
		httputil.ServerError(w, "create", what)
		return
	}
	invite, err := q.CreateInvite(r.Context(), db.CreateInviteParams{
		TenantID:  tenant.ID,
		Email:     ownerEmail,
		Role:      RoleOwner,
		InvitedBy: operatorID,
		Token:     token,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ownerInviteExpiry), Valid: true},
	})
	if err != nil {
		httputil.ServerError(w, "create", "owner invite")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "create", "organization")
		return
	}

	// Best-effort: the org + invite are durable; a failed email is recoverable
	// via the invite link (mirrors the tenant invite-email pattern).
	h.notifyInvitedOwner(r.Context(), ownerEmail, tenant.Name, token)
	logging.Info(r.Context(), "platform operator created an org", "platform-org-created", logging.UserID(operatorID), logging.TenantID(tenant.ID))

	httputil.JSON(w, http.StatusCreated, map[string]any{
		"tenant": map[string]any{
			"id":     tenant.ID,
			"name":   tenant.Name,
			"slug":   tenant.Slug,
			"domain": tenant.Domain,
			"status": tenant.Status,
		},
		"invite": map[string]any{
			"email":      invite.Email,
			"role":       invite.Role,
			"expires_at": invite.ExpiresAt,
			// The shareable accept link, so the operator can hand it to the
			// customer directly if the email doesn't land. Safe to return:
			// the caller is a platform admin who just created this org.
			"link": h.appLink("/invite/" + token),
		},
	})
}

// HandleLoginAsOrg lets a platform operator ENTER an org to inspect/support it,
// even one they're not a member of. It mints a tenant-scoped token
// for the OPERATOR's own identity (so any actions are audited under their user)
// with owner-level access to the target org — impersonation / god-mode, hence
// platform-admin-only. Approved orgs only: a non-approved org is gated by
// auth.Middleware anyway, so entering it would just hit the blocked screen.
//
// No membership is created and last_tenant is NOT moved, so this leaves the
// operator's own org list untouched — they return via the org switcher.
func (h *Handler) HandleLoginAsOrg(w http.ResponseWriter, r *http.Request) {
	tenantID, err := httputil.ParseID(r, "tenantID")
	if err != nil {
		httputil.BadRequest(w, "invalid organization ID")
		return
	}
	operatorID := UserIDFromContext(r.Context())

	tenant, err := h.queries.GetTenant(r.Context(), tenantID)
	if err != nil {
		if httputil.DBError(w, err, "organization") {
			return
		}
		return
	}
	// You can't "log in as" an org you already belong to — that's what the org
	// switcher is for, and it avoids a needless impersonation session.
	if _, err := h.queries.GetMembership(r.Context(), db.GetMembershipParams{UserID: operatorID, TenantID: tenantID}); err == nil {
		httputil.ErrorCode(w, http.StatusConflict, "own_org",
			"you're already a member of this organization — use the org switcher")
		return
	}
	if tenant.Kind == TenantKindDemo {
		httputil.ErrorCode(w, http.StatusConflict, "demo_org", "open the Playground from the org switcher")
		return
	}
	if tenant.Status != statusApproved {
		httputil.ErrorCode(w, http.StatusConflict, "org_not_active",
			"enable the organization before opening it")
		return
	}

	token, err := GenerateImpersonationToken(operatorID, tenantID, RoleOwner, h.jwtSecret, tokenExpiry)
	if err != nil {
		httputil.ServerError(w, "generate", "token")
		return
	}
	setTokenCookie(w, token)
	logging.Info(r.Context(), "platform operator opened an org", "platform-org-opened", logging.UserID(operatorID), logging.TenantID(tenantID))
	httputil.JSON(w, http.StatusOK, map[string]any{
		"token":  token,
		"tenant": map[string]any{"id": tenant.ID, "name": tenant.Name, "slug": tenant.Slug},
	})
}
