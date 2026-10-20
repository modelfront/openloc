package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"openloc/internal/dashboarddefaults"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/mtdefaults"
)

// Tenant approval lifecycle. Existing tenants default to
// statusApproved via migration 064; self-serve signups start statusPending and
// are gated by auth.Middleware until an operator approves.
const (
	statusPending   = "pending"
	statusApproved  = "approved"
	statusRejected  = "rejected"  // signup declined (never activated)
	statusSuspended = "suspended" // was active, disabled by an operator
)

// maxSlugAttempts bounds the slug-collision retry loop. tenants.name has no
// unique constraint but tenants.slug does, so a fresh slug may collide with an
// existing org of a similar name; we suffix -2, -3, … until one sticks.
const maxSlugAttempts = 10

// createTenantWithSlugRetry creates a tenant, retrying with a numeric slug
// suffix on collision. `create` receives the candidate slug and performs the
// actual INSERT, so every org-creation path shares one collision-handling loop.
// Any other error is returned at once.
//
// Inside a transaction, `create` must report a taken slug without an error
// (CreatePendingTenant and CreateApprovedTenant use ON CONFLICT DO NOTHING):
// a unique violation aborts the transaction, and every retry after it fails.
func createTenantWithSlugRetry(ctx context.Context, name string, create func(slug string) (db.Tenant, error)) (db.Tenant, error) {
	base := Slugify(name)
	for attempt := 0; attempt < maxSlugAttempts; attempt++ {
		slug := base
		if attempt > 0 {
			slug = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		tenant, err := create(slug)
		if err == nil {
			return tenant, nil
		}
		if !isSlugTaken(err) {
			return db.Tenant{}, err
		}
	}
	return db.Tenant{}, fmt.Errorf("no free slug for %q after %d attempts", name, maxSlugAttempts)
}

// isSlugTaken reports whether a tenant INSERT lost to an existing slug: no row
// from an ON CONFLICT DO NOTHING insert, or a unique violation on the slug index.
func isSlugTaken(err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	return httputil.IsUniqueViolationOn(err, "idx_tenants_slug")
}

// orgNameFromDomain derives a human org name from an email domain:
// "example.com" → "Example", "sub.acme.co.uk" → "Sub". Used as the default
// when the signup request doesn't supply a name.
func orgNameFromDomain(domain string) string {
	label := domain
	if i := strings.IndexByte(domain, '.'); i > 0 {
		label = domain[:i]
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return "Organization"
	}
	return strings.ToUpper(label[:1]) + label[1:]
}

// HandleCreateOrg is the self-serve org-signup endpoint. Served under
// RequireIdentity: the caller has a valid identity but belongs to no org yet.
//
// The org's domain is derived from the caller's VERIFIED email (never the
// request body) so a gmail identity can't mint an "example" org. Free/personal
// email providers are rejected; an already-claimed domain is rejected (the
// caller must be invited or wait for approval instead). On success the tenant
// is created in 'pending' status with the caller as owner, platform
// operators are notified, and a tenant token is returned — the pending-gate in
// auth.Middleware keeps the owner on the "awaiting approval" screen until an
// operator approves.
func (h *Handler) HandleCreateOrg(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	user, err := h.queries.GetUser(r.Context(), userID)
	if err != nil {
		httputil.ServerError(w, "load", "user")
		return
	}

	domain := emailDomain(user.Email)
	if domain == "" {
		httputil.BadRequest(w, "your account has no valid email domain")
		return
	}
	if IsFreeEmailDomain(domain) {
		httputil.ErrorCode(w, http.StatusBadRequest, "free_email",
			"Personal email addresses can't create an organization. Use your work email, or ask an admin to invite you.")
		return
	}

	// A domain can back exactly one org. If one already exists (any status), the
	// caller must be invited / wait — never a duplicate. (An approved+open org
	// would already have auto-joined them, so they wouldn't reach here.)
	if existing, err := h.queries.GetTenantByDomain(r.Context(), &domain); err == nil {
		msg := fmt.Sprintf("An organization for %s already exists. Ask an admin to invite you.", domain)
		if existing.Status == statusPending {
			msg = fmt.Sprintf("A signup for %s is already pending approval.", domain)
		}
		httputil.ErrorCode(w, http.StatusConflict, "domain_taken", msg)
		return
	}

	// Optional caller-supplied name; default to the domain label.
	var req struct {
		Name string `json:"name"`
	}
	_ = httputil.Decode(r, &req) // body optional
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = orgNameFromDomain(domain)
	}

	// Create tenant (pending) + owner membership atomically (multi-step write).
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "create", "organization")
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)

	tenant, err := createTenantWithSlugRetry(r.Context(), name, func(slug string) (db.Tenant, error) {
		return q.CreatePendingTenant(r.Context(), db.CreatePendingTenantParams{
			Name:   name,
			Slug:   slug,
			Domain: &domain,
		})
	})
	if err != nil {
		httputil.ServerError(w, "create", "organization")
		return
	}

	// A new org must get the deployment's built-in MT engine straight away,
	// not at the next server restart: the AI settings page shows LibreTranslate
	// as built in and ready, so without the row it advertises a vendor that
	// cannot be assigned to anything. Inside the same
	// transaction as the tenant, so a half-provisioned org cannot exist.
	if err := mtdefaults.EnsureForTenant(r.Context(), q, tenant.ID); err != nil {
		httputil.ServerError(w, "create", "organization")
		return
	}
	// The org's starting dashboards, in the same transaction as everything else
	// it is provisioned with. A new org opening an empty Dashboards page learns
	// nothing about what the product can measure.
	if err := dashboarddefaults.InstallDefaults(r.Context(), q, tenant.ID, &userID); err != nil {
		httputil.ServerError(w, "create", "organization")
		return
	}
	if err := q.CreateMembership(r.Context(), db.CreateMembershipParams{
		UserID:   userID,
		TenantID: tenant.ID,
		Role:     RoleOwner,
	}); err != nil {
		httputil.ServerError(w, "create", "membership")
		return
	}
	if err := q.SetLastTenant(r.Context(), db.SetLastTenantParams{ID: userID, LastTenantID: &tenant.ID}); err != nil {
		httputil.ServerError(w, "update", "user")
		return
	}
	// A "Default" library workflow, so its Workflows and
	// Steps pages are never empty.
	if err := ensureDefaultWorkflow(r.Context(), q, tenant.ID); err != nil {
		httputil.ServerError(w, "create", "default workflow")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "create", "organization")
		return
	}

	// Notify platform operators to review the signup. Best-effort: the tenant
	// row is durable, so an email failure must not fail the request (mirrors the
	// invite-email pattern).
	h.notifyOperatorsOfSignup(r.Context(), tenant, user)

	// Mint a tenant token for the pending org. The pending-gate blocks real work
	// until approval, but this lets a reload land on the awaiting-approval screen
	// without another round-trip.
	token, err := GenerateToken(userID, tenant.ID, RoleOwner, h.jwtSecret, tokenExpiry)
	if err != nil {
		httputil.ServerError(w, "generate", "token")
		return
	}
	setTokenCookie(w, token)
	httputil.JSON(w, http.StatusCreated, map[string]any{
		"token":  token,
		"status": statusPending,
		"tenant": map[string]any{
			"id":     tenant.ID,
			"name":   tenant.Name,
			"slug":   tenant.Slug,
			"domain": domain,
		},
	})
	logging.Info(r.Context(), "org signup created pending", "auth-org-signup-pending", logging.TenantID(tenant.ID), logging.UserID(userID), logging.Label("domain", domain))
}
