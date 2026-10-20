package invite

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	InvitedBy     int64              `json:"invited_by"`
	InvitedByName string             `json:"invited_by_name,omitempty"`
	AcceptedAt    pgtype.Timestamptz `json:"accepted_at"`
	ExpiresAt     pgtype.Timestamptz `json:"expires_at"`
	CreatedAt     pgtype.Timestamptz `json:"created_at"`
}

func viewOf(inv db.Invite) View {
	return View{
		ID: inv.ID, TenantID: inv.TenantID, Email: inv.Email, Role: inv.Role, InvitedBy: inv.InvitedBy,
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

// CheckCreate reports whether callerRole may invite someone as role (already
// defaulted). It returns a RuleError, so previews refuse exactly like Create.
func CheckCreate(callerRole, role string) error {
	// Owner is never invited from inside an org: ownership comes from a role
	// change by an owner, or from a platform operator creating the org.
	if role != auth.RoleAdmin && role != auth.RoleProjectManager && role != auth.RoleLinguist {
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

// Create validates and stores a 7-day invite, then emails the link. Role
// defaults to linguist. A failed email is logged, not returned: the invite row
// is durable and the inviter can resend.
func (s *Service) Create(ctx context.Context, actor auth.MemberActor, email, role string) (View, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return View{}, RuleError{Status: http.StatusBadRequest, Msg: "email is required"}
	}
	if role == "" {
		role = auth.RoleLinguist
	}
	if err := CheckCreate(actor.Role, role); err != nil {
		return View{}, err
	}
	tenantID := actor.TenantID

	_, err := s.q.GetPendingInviteByEmailAndTenant(ctx, db.GetPendingInviteByEmailAndTenantParams{Email: email, TenantID: tenantID})
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
	inv, err := s.q.CreateInvite(ctx, db.CreateInviteParams{
		TenantID:  tenantID,
		Email:     email,
		Role:      role,
		InvitedBy: actor.UserID,
		Token:     token,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(inviteExpiry), Valid: true},
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
	subject, content, err := s.inviteEmail(ctx, inv.TenantID, inv.Role, token)
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
	out := make([]View, 0)
	roles := handledRoles(actor.Role)
	if len(roles) == 0 {
		return out, nil
	}
	rows, err := q.ListInvitesByTenant(ctx, db.ListInvitesByTenantParams{TenantID: actor.TenantID, Roles: roles})
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	for _, r := range rows {
		out = append(out, View{
			ID: r.ID, TenantID: r.TenantID, Email: r.Email, Role: r.Role,
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
	if !mayHandle(actor.Role, inv.Role) {
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
