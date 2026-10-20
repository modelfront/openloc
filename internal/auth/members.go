package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Member role changes and removal, shared by the REST handlers and the MCP
// tools. The callers gate both to owner/admin; the rules below add the role
// hierarchy and the last-admin guard.

// ErrNotMember means the target user has no membership in the caller's org.
// It is the same answer for "no such user" and "member of another org".
var ErrNotMember = errors.New("user is not a member of this organization")

// MemberRuleError is a refused change. Status is the HTTP status the REST
// handler answers with; Msg is written for the caller.
type MemberRuleError struct {
	Status int
	Msg    string
}

func (e MemberRuleError) Error() string { return e.Msg }

// assignableRoles is the four-role model: owner, admin,
// project_manager, linguist.
var assignableRoles = map[string]bool{
	RoleOwner: true, RoleAdmin: true, RoleProjectManager: true, RoleLinguist: true,
}

// MemberActor is who asks for a membership change.
type MemberActor struct {
	TenantID int64
	UserID   int64
	Role     string
}

// CheckRoleChange validates a role change without applying it and returns the
// target's current membership.
//
// The membership lookup runs before the role is validated, so a caller cannot
// tell "no such member" from "bad role" for a user in another org.
func CheckRoleChange(ctx context.Context, q db.Querier, actor MemberActor, userID int64, role string) (db.Membership, error) {
	if actor.UserID == userID {
		return db.Membership{}, MemberRuleError{http.StatusBadRequest, "cannot change your own role"}
	}
	target, err := membership(ctx, q, actor.TenantID, userID)
	if err != nil {
		return db.Membership{}, err
	}
	if !assignableRoles[role] {
		return db.Membership{}, MemberRuleError{http.StatusBadRequest, "invalid role"}
	}
	// Nobody grants a role above their own or touches a member who outranks
	// them: owner-only capabilities could otherwise be laundered through a
	// second account.
	if !RoleAtLeast(actor.Role, role) {
		return db.Membership{}, MemberRuleError{http.StatusForbidden, "only an owner can grant owner role"}
	}
	if !RoleAtLeast(actor.Role, target.Role) {
		return db.Membership{}, MemberRuleError{http.StatusForbidden, "only an owner can change another owner's role"}
	}
	demotes := isAdminRole(target.Role) && !isAdminRole(role)
	if demotes {
		if err := requireAnotherAdmin(ctx, q, actor.TenantID, "cannot demote the last admin — at least one admin is required"); err != nil {
			return db.Membership{}, err
		}
	}
	return target, nil
}

// ChangeMemberRole applies a role change that CheckRoleChange accepts and
// returns the membership as it was before.
func ChangeMemberRole(ctx context.Context, q db.Querier, actor MemberActor, userID int64, role string) (db.Membership, error) {
	prev, err := CheckRoleChange(ctx, q, actor, userID, role)
	if err != nil {
		return db.Membership{}, err
	}
	if err := q.UpdateMembershipRole(ctx, db.UpdateMembershipRoleParams{UserID: userID, TenantID: actor.TenantID, Role: role}); err != nil {
		return db.Membership{}, fmt.Errorf("update membership role: %w", err)
	}
	return prev, nil
}

// CheckRemoveMember validates removing a user from the org without doing it
// and returns the target's membership.
func CheckRemoveMember(ctx context.Context, q db.Querier, actor MemberActor, userID int64) (db.Membership, error) {
	if actor.UserID == userID {
		return db.Membership{}, MemberRuleError{http.StatusBadRequest, "cannot remove yourself"}
	}
	target, err := membership(ctx, q, actor.TenantID, userID)
	if err != nil {
		return db.Membership{}, err
	}
	if !RoleAtLeast(actor.Role, target.Role) {
		return db.Membership{}, MemberRuleError{http.StatusForbidden, "only an owner can remove another owner"}
	}
	if isAdminRole(target.Role) {
		if err := requireAnotherAdmin(ctx, q, actor.TenantID, "cannot remove the last active admin/owner"); err != nil {
			return db.Membership{}, err
		}
	}
	return target, nil
}

// RemoveMember deletes the user's membership in this org and, in the same
// transaction, releases every segment they hold there. Their identity and
// memberships in other orgs stay. A membership is a link row, so the delete
// is hard.
func RemoveMember(ctx context.Context, b database.TxBeginner, actor MemberActor, userID int64) error {
	return database.InTx(ctx, b, func(_ pgx.Tx, q db.Querier) error {
		if _, err := CheckRemoveMember(ctx, q, actor, userID); err != nil {
			return err
		}
		if err := q.DeleteUserHoldsInTenant(ctx, db.DeleteUserHoldsInTenantParams{TenantID: actor.TenantID, UserID: userID}); err != nil {
			return fmt.Errorf("release segment holds: %w", err)
		}
		if err := q.DeleteMembership(ctx, db.DeleteMembershipParams{UserID: userID, TenantID: actor.TenantID}); err != nil {
			return fmt.Errorf("delete membership: %w", err)
		}
		return nil
	})
}

func membership(ctx context.Context, q db.Querier, tenantID, userID int64) (db.Membership, error) {
	m, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: userID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Membership{}, ErrNotMember
	}
	if err != nil {
		return db.Membership{}, fmt.Errorf("get membership: %w", err)
	}
	return m, nil
}

// ActorFromContext is the authenticated caller as a MemberActor.
func ActorFromContext(ctx context.Context) MemberActor {
	return MemberActor{TenantID: TenantIDFromContext(ctx), UserID: UserIDFromContext(ctx), Role: RoleFromContext(ctx)}
}

func isAdminRole(role string) bool { return role == RoleOwner || role == RoleAdmin }

// requireAnotherAdmin refuses when the org has at most one active admin/owner.
func requireAnotherAdmin(ctx context.Context, q db.Querier, tenantID int64, msg string) error {
	n, err := q.CountAdminsInTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if n <= 1 {
		return MemberRuleError{http.StatusConflict, msg}
	}
	return nil
}

// WriteMemberError answers a member-change error over HTTP.
func WriteMemberError(w http.ResponseWriter, err error) {
	var rule MemberRuleError
	switch {
	case errors.As(err, &rule):
		httputil.Error(w, rule.Status, rule.Msg)
	case errors.Is(err, ErrNotMember):
		httputil.NotFound(w, "user")
	default:
		httputil.ServerError(w, "update", "member")
	}
}
