package invite

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// Invite create / list / revoke / resend, shared by the REST handlers and the
// MCP tools. Callers gate all of them to managers (owner/admin/PM); mayHandle
// decides which invites each manager may touch.

// mayHandle reports whether a member with actorRole may invite someone as
// inviteRole, or see, revoke or resend an invite for it. Nobody handles a role
// above their own, and project managers handle only linguist invites.
func mayHandle(actorRole, inviteRole string) bool {
	switch actorRole {
	case auth.RoleOwner, auth.RoleAdmin:
		return auth.RoleAtLeast(actorRole, inviteRole)
	case auth.RoleProjectManager:
		return inviteRole == auth.RoleLinguist
	}
	return false
}

// RuleError is a refused invite. Status is the HTTP status the REST handler
// answers with; Msg is written for the caller. RetryAfter is set on a 429: how
// long until the email budget allows another invitation.
type RuleError struct {
	Status     int
	Msg        string
	RetryAfter time.Duration
}

func (e RuleError) Error() string { return e.Msg }

// View is an invite as API callers see it. It never carries the token: only
// the invite email does, so holding a token proves that someone reads the
// invited mailbox.
type View struct {
	ID            int64              `json:"id"`
	TenantID      int64              `json:"tenant_id"`
	Email         string             `json:"email"`
	Role          string             `json:"role"`
	Kind          string             `json:"kind"`
	VendorOrgName *string            `json:"vendor_org_name,omitempty"`
	InvitedBy     int64              `json:"invited_by"`
	InvitedByName string             `json:"invited_by_name,omitempty"`
	AcceptedAt    pgtype.Timestamptz `json:"accepted_at"`
	ExpiresAt     pgtype.Timestamptz `json:"expires_at"`
	CreatedAt     pgtype.Timestamptz `json:"created_at"`
}

func viewOf(inv db.Invite) View {
	return View{
		ID: inv.ID, TenantID: inv.TenantID, Email: inv.Email, Role: inv.Role,
		Kind: inv.Kind, VendorOrgName: inv.VendorOrgName, InvitedBy: inv.InvitedBy,
		AcceptedAt: inv.AcceptedAt, ExpiresAt: inv.ExpiresAt, CreatedAt: inv.CreatedAt,
	}
}

// Service creates and resends invites and emails the link. Build one per
// process and share it: it holds the per-user email budget, so the REST
// handlers and the MCP tools must use the same instance.
type Service struct {
	q       db.Querier
	baseURL string
	sender  EmailSender
	emails  *EmailLimiter
}

// NewService builds the invite service. sender may be nil: invites are then
// created without an email, and Resend refuses.
func NewService(q db.Querier, baseURL string, sender EmailSender) *Service {
	return &Service{q: q, baseURL: baseURL, sender: sender, emails: NewEmailLimiter()}
}

// spendEmail takes one invitation email from the actor's budget.
func (s *Service) spendEmail(actor auth.MemberActor) error {
	if ok, wait := s.emails.Take(actor.UserID); !ok {
		return RuleError{Status: http.StatusTooManyRequests, Msg: "too many invitation emails at once; wait a minute and try again", RetryAfter: wait}
	}
	return nil
}

// maxVendorOrgNameLen caps a vendor org's name, in characters.
const maxVendorOrgNameLen = 120

// NewInvite is an invitation a caller asks for. A non-nil VendorOrgName asks
// for a vendor-org invite: the invitee sets up a vendor org of the caller's org
// and becomes its admin.
type NewInvite struct {
	Email         string
	Role          string
	VendorOrgName *string
}

// CheckCreate normalizes in (email, default role, trimmed vendor name) and
// reports whether actor may create it. It returns a RuleError, so previews
// refuse exactly like Create. Duplicates and the email budget are checked by
// Create only.
func CheckCreate(ctx context.Context, q db.Querier, actor auth.MemberActor, in NewInvite) (NewInvite, error) {
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" {
		return in, RuleError{Status: http.StatusBadRequest, Msg: "email is required"}
	}
	tenant, err := q.GetTenant(ctx, actor.TenantID)
	if err != nil {
		return in, fmt.Errorf("load organization: %w", err)
	}
	if in.VendorOrgName != nil {
		return checkVendorOrgInvite(actor.Role, tenant.Kind, in)
	}
	if in.Role == "" {
		in.Role = auth.RoleLinguist
	}
	return in, checkMemberRole(actor.Role, tenant.Kind, in.Role)
}

// checkMemberRole reports whether callerRole may invite someone as role into
// an org of tenantKind.
func checkMemberRole(callerRole, tenantKind, role string) error {
	// Owner is never invited from inside an org: ownership comes from a role
	// change by an owner, or from a platform operator creating the org. A
	// vendor org has no owner, and its admins come from a role change.
	if tenantKind == auth.TenantKindVendor {
		// A vendor org's admin runs its team; its project managers invite nobody.
		if callerRole == auth.RoleProjectManager {
			return RuleError{Status: http.StatusForbidden, Msg: "in a vendor organization, only its admins invite people"}
		}
		if role != auth.RoleProjectManager && role != auth.RoleLinguist {
			return RuleError{Status: http.StatusBadRequest, Msg: "in a vendor organization, role must be 'project_manager' or 'linguist'"}
		}
	} else if role != auth.RoleAdmin && role != auth.RoleProjectManager && role != auth.RoleLinguist {
		return RuleError{Status: http.StatusBadRequest, Msg: "role must be 'admin', 'project_manager', or 'linguist'"}
	}
	if !mayHandle(callerRole, role) {
		msg := "your role can't invite someone as " + role
		if callerRole == auth.RoleProjectManager {
			msg = "project managers can only invite linguists"
		}
		return RuleError{Status: http.StatusForbidden, Msg: msg}
	}
	return nil
}

// checkVendorOrgInvite validates a vendor-org invite. It is an admin invite in
// the client org, so mayHandle limits it to the client's owners and admins.
// Only a customer org invites vendors: vendor orgs don't nest.
func checkVendorOrgInvite(callerRole, tenantKind string, in NewInvite) (NewInvite, error) {
	if tenantKind != auth.TenantKindCustomer {
		return in, RuleError{Status: http.StatusForbidden, Msg: "only a client organization can invite a vendor"}
	}
	if in.Role != "" && in.Role != auth.RoleAdmin {
		return in, RuleError{Status: http.StatusBadRequest, Msg: "a vendor invite makes the person the vendor's admin; leave role empty or set it to 'admin'"}
	}
	in.Role = auth.RoleAdmin
	if !mayHandle(callerRole, in.Role) {
		return in, RuleError{Status: http.StatusForbidden, Msg: "only owners and admins can invite a vendor"}
	}
	name := strings.TrimSpace(*in.VendorOrgName)
	if n := utf8.RuneCountInString(name); n == 0 || n > maxVendorOrgNameLen {
		return in, RuleError{Status: http.StatusBadRequest, Msg: fmt.Sprintf("vendor_org_name must be 1 to %d characters", maxVendorOrgNameLen)}
	}
	// It becomes an org name and an email subject: one line of text.
	if strings.ContainsFunc(name, unicode.IsControl) {
		return in, RuleError{Status: http.StatusBadRequest, Msg: "vendor_org_name can't contain line breaks or control characters"}
	}
	in.VendorOrgName = &name
	return in, nil
}

// Create validates and stores a 7-day invite, then emails the link. Role
// defaults to linguist. A failed email is logged, not returned: the invite row
// is durable and the inviter can resend.
func (s *Service) Create(ctx context.Context, actor auth.MemberActor, in NewInvite) (View, error) {
	in, err := CheckCreate(ctx, s.q, actor, in)
	if err != nil {
		return View{}, err
	}
	email, role := in.Email, in.Role
	tenantID := actor.TenantID

	_, err = s.q.GetPendingInviteByEmailAndTenant(ctx, db.GetPendingInviteByEmailAndTenantParams{Email: email, TenantID: tenantID})
	if err == nil {
		return View{}, RuleError{Status: http.StatusConflict, Msg: "a pending invite already exists for this email"}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return View{}, fmt.Errorf("check existing invites: %w", err)
	}
	// A person is a global identity and membership is per org.
	existing, err := s.q.GetUserByEmail(ctx, email)
	switch {
	case err == nil:
		_, mErr := s.q.GetMembership(ctx, db.GetMembershipParams{UserID: existing.ID, TenantID: tenantID})
		if mErr == nil {
			return View{}, RuleError{Status: http.StatusConflict, Msg: "user is already a member of this organization"}
		}
		if !errors.Is(mErr, pgx.ErrNoRows) {
			return View{}, fmt.Errorf("check membership: %w", mErr)
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return View{}, fmt.Errorf("look up user: %w", err)
	}

	// Without a sender no email goes out, so there is nothing to budget.
	if s.sender != nil {
		if err := s.spendEmail(actor); err != nil {
			return View{}, err
		}
	}
	token, err := generateToken()
	if err != nil {
		return View{}, fmt.Errorf("generate invite token: %w", err)
	}
	kind := auth.InviteKindMember
	if in.VendorOrgName != nil {
		kind = auth.InviteKindVendorOrg
	}
	inv, err := s.q.CreateInvite(ctx, db.CreateInviteParams{
		TenantID:      tenantID,
		Email:         email,
		Role:          role,
		InvitedBy:     actor.UserID,
		Token:         token,
		ExpiresAt:     pgtype.Timestamptz{Time: time.Now().Add(inviteExpiry), Valid: true},
		Kind:          kind,
		VendorOrgName: in.VendorOrgName,
	})
	if err != nil {
		return View{}, fmt.Errorf("create invite: %w", err)
	}
	s.emailNewInvite(ctx, inv)
	return viewOf(inv), nil
}

// Resend emails an invite the actor may handle again, with a new link and a
// fresh 7-day expiry. The old token dies, so a link seen before the resend
// stops working. The new token is stored only after the email went out, so a
// failed email changes nothing and the old link keeps working.
func (s *Service) Resend(ctx context.Context, actor auth.MemberActor, id int64) (View, error) {
	inv, err := get(ctx, s.q, actor, id)
	if err != nil {
		return View{}, err
	}
	if inv.AcceptedAt.Valid {
		return View{}, RuleError{Status: http.StatusConflict, Msg: "this invitation was already accepted"}
	}
	if s.sender == nil {
		return View{}, RuleError{Status: http.StatusServiceUnavailable, Msg: "email sending is not configured"}
	}
	if err := s.spendEmail(actor); err != nil {
		return View{}, err
	}
	token, err := generateToken()
	if err != nil {
		return View{}, fmt.Errorf("generate invite token: %w", err)
	}
	subject, content, err := s.inviteEmail(ctx, inv, token)
	if err != nil {
		return View{}, err
	}
	if err := s.send(ctx, inv.Email, subject, content); err != nil {
		logging.Warn(ctx, err, "resending the invite email failed", "invite.resend_email_failed",
			logging.InviteID(inv.ID), logging.Label("invite_email", inv.Email))
		return View{}, RuleError{Status: http.StatusBadGateway, Msg: "the invitation email could not be sent"}
	}
	updated, err := s.q.ResendInvite(ctx, db.ResendInviteParams{
		NewToken:  token,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(inviteExpiry), Valid: true},
		ID:        inv.ID,
		TenantID:  inv.TenantID,
		OldToken:  inv.Token,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Accepted, revoked or resent by someone else while the email went out,
		// so the link in that email doesn't work.
		logging.Warn(ctx, nil, "invite changed while its resend email was out; that email's link is dead",
			"invite.resend_lost_race", logging.InviteID(inv.ID))
		return View{}, RuleError{Status: http.StatusConflict, Msg: "the invitation changed while it was being sent; reload and try again"}
	}
	if err != nil {
		return View{}, fmt.Errorf("store resent invite: %w", err)
	}
	return viewOf(updated), nil
}

// handlerRole is the role whose invites the actor may handle: their own,
// except that a vendor org's project manager handles none (only its admins
// run its team).
func handlerRole(ctx context.Context, q db.Querier, actor auth.MemberActor) (string, error) {
	if actor.Role != auth.RoleProjectManager {
		return actor.Role, nil
	}
	tenant, err := q.GetTenant(ctx, actor.TenantID)
	if err != nil {
		return "", fmt.Errorf("load organization: %w", err)
	}
	if tenant.Kind == auth.TenantKindVendor {
		return "", nil
	}
	return actor.Role, nil
}

// handledRoles lists the invite roles the actor may handle, for queries that
// filter by role.
func handledRoles(actorRole string) []string {
	var out []string
	for _, role := range []string{auth.RoleOwner, auth.RoleAdmin, auth.RoleProjectManager, auth.RoleLinguist} {
		if mayHandle(actorRole, role) {
			out = append(out, role)
		}
	}
	return out
}

// List returns the invites in the actor's tenant that the actor may handle,
// newest first, with the inviter's name. The role filter runs in SQL, so a
// page of results is a page of invites the actor may see.
func List(ctx context.Context, q db.Querier, actor auth.MemberActor) ([]View, error) {
	return list(ctx, q, actor, false)
}

// list is List; pendingOnly leaves out the accepted invites.
func list(ctx context.Context, q db.Querier, actor auth.MemberActor, pendingOnly bool) ([]View, error) {
	out := make([]View, 0)
	role, err := handlerRole(ctx, q, actor)
	if err != nil {
		return nil, err
	}
	roles := handledRoles(role)
	if len(roles) == 0 {
		return out, nil
	}
	rows, err := q.ListInvitesByTenant(ctx, db.ListInvitesByTenantParams{TenantID: actor.TenantID, Roles: roles, PendingOnly: pendingOnly})
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	for _, r := range rows {
		out = append(out, View{
			ID: r.ID, TenantID: r.TenantID, Email: r.Email, Role: r.Role,
			Kind: r.Kind, VendorOrgName: r.VendorOrgName,
			InvitedBy: r.InvitedBy, InvitedByName: r.InvitedByName,
			AcceptedAt: r.AcceptedAt, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// Get returns one invite in the actor's tenant that the actor may handle.
// pgx.ErrNoRows when there is none, so "no such invite" and "not yours to
// handle" give the same answer.
func Get(ctx context.Context, q db.Querier, actor auth.MemberActor, id int64) (View, error) {
	inv, err := get(ctx, q, actor, id)
	if err != nil {
		return View{}, err
	}
	return viewOf(inv), nil
}

// get is Get with the token, for this package only.
func get(ctx context.Context, q db.Querier, actor auth.MemberActor, id int64) (db.Invite, error) {
	inv, err := q.GetInviteTenant(ctx, db.GetInviteTenantParams{ID: id, TenantID: actor.TenantID})
	if err != nil {
		return db.Invite{}, err
	}
	role, err := handlerRole(ctx, q, actor)
	if err != nil {
		return db.Invite{}, err
	}
	if !mayHandle(role, inv.Role) {
		return db.Invite{}, pgx.ErrNoRows
	}
	return inv, nil
}

// Revoke deletes an invite the actor may handle, which kills its link.
// Invites are a revocation table, so the delete is hard.
func Revoke(ctx context.Context, q db.Querier, actor auth.MemberActor, id int64) error {
	if _, err := get(ctx, q, actor, id); err != nil {
		return err
	}
	if err := q.DeleteInvite(ctx, db.DeleteInviteParams{ID: id, TenantID: actor.TenantID}); err != nil {
		return fmt.Errorf("delete invite: %w", err)
	}
	return nil
}
