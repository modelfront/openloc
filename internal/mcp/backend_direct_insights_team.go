package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/invite"
	"openloc/internal/notification"
	"openloc/internal/userpair"
)

// --- Me ---------------------------------------------------------------------

func (b *directBackend) GetMe(ctx context.Context, _ GetMeIn) (MeProfile, error) {
	tenantID, userID := scope(ctx)
	u, err := b.q.GetUser(ctx, userID)
	if err != nil {
		return MeProfile{}, fmt.Errorf("get user: %w", err)
	}
	t, err := b.q.GetTenant(ctx, tenantID)
	if err != nil {
		return MeProfile{}, fmt.Errorf("get tenant: %w", err)
	}
	pairs, err := userpair.List(ctx, b.q, tenantID, userID)
	if err != nil {
		return MeProfile{}, err
	}
	return MeProfile{
		ID: profileID(u.ID, t.ID), Nickname: u.Name + " — " + t.Name,
		UserID: u.ID, Name: u.Name, Email: u.Email, Role: insightRole(ctx),
		TenantID: t.ID, TenantName: t.Name, LanguagePairs: pairViews(pairs),
	}, nil
}

// profileID derives the profile id from (user, organization).
func profileID(userID, tenantID int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("openloc-profile|%d|%d", userID, tenantID)))
	return "prf_" + hex.EncodeToString(sum[:8])
}

func pairViews(pairs []userpair.Pair) []MemberLanguagePair {
	out := make([]MemberLanguagePair, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, MemberLanguagePair{SourceLang: p.SourceLang, TargetLang: p.TargetLang})
	}
	return out
}

// --- Invites ----------------------------------------------------------------

func inviteStatus(accepted, expires pgtype.Timestamptz, now time.Time) string {
	switch {
	case accepted.Valid:
		return "accepted"
	case expires.Valid && expires.Time.Before(now):
		return "expired"
	default:
		return "pending"
	}
}

func (b *directBackend) ListInvites(ctx context.Context, _ ListInvitesIn) (InviteList, error) {
	rows, err := invite.List(ctx, b.q, auth.ActorFromContext(ctx))
	if err != nil {
		return InviteList{}, err
	}
	now := time.Now()
	out := make([]InviteView, 0, len(rows))
	for _, r := range rows {
		out = append(out, InviteView{
			ID: r.ID, Email: r.Email, Role: r.Role, Status: inviteStatus(r.AcceptedAt, r.ExpiresAt, now),
			InvitedByName: r.InvitedByName, CreatedAt: tsString(r.CreatedAt),
			ExpiresAt: tsString(r.ExpiresAt), AcceptedAt: tsString(r.AcceptedAt),
		})
	}
	return InviteList{Invites: out, Count: len(out)}, nil
}

// PreviewInviteMember runs the role rules and describes the invite. Duplicates
// and the email budget run on the confirmed call, inside invite.Service.Create.
func (b *directBackend) PreviewInviteMember(ctx context.Context, in InviteMemberIn) (Preview, error) {
	email := strings.TrimSpace(strings.ToLower(in.Email))
	if email == "" {
		return Preview{}, invalidf("email is required")
	}
	role := in.Role
	if role == "" {
		role = auth.RoleLinguist
	}
	if err := inviteRuleError(invite.CheckCreate(auth.ActorFromContext(ctx).Role, role)); err != nil {
		return Preview{}, err
	}
	return Preview{
		Summary: fmt.Sprintf("Invite %s as %s.", email, role),
		Effects: []string{"They get an email with a link that is valid for 7 days."},
	}, nil
}

func (b *directBackend) InviteMember(ctx context.Context, in InviteMemberIn) (InviteView, error) {
	if b.insight.Invites == nil {
		return InviteView{}, errors.New("invites are not configured")
	}
	inv, err := b.insight.Invites.Create(ctx, auth.ActorFromContext(ctx), in.Email, in.Role)
	if err := inviteRuleError(err); err != nil {
		return InviteView{}, err
	}
	return InviteView{
		ID: inv.ID, Email: inv.Email, Role: inv.Role, Status: inviteStatus(inv.AcceptedAt, inv.ExpiresAt, time.Now()),
		CreatedAt: tsString(inv.CreatedAt), ExpiresAt: tsString(inv.ExpiresAt), AcceptedAt: tsString(inv.AcceptedAt),
	}, nil
}

func (b *directBackend) PreviewRevokeInvite(ctx context.Context, in RevokeInviteIn) (Preview, error) {
	inv, err := invite.Get(ctx, b.q, auth.ActorFromContext(ctx), in.InviteID)
	if err != nil {
		return Preview{}, insightNotFound(err)
	}
	effect := "The invite link stops working."
	if inv.AcceptedAt.Valid {
		effect = "The invite was already accepted; the person stays a member. Only the invite record goes."
	}
	return Preview{
		Summary: fmt.Sprintf("Revoke the %s invite for %s.", inv.Role, inv.Email),
		Effects: []string{effect},
	}, nil
}

func (b *directBackend) RevokeInvite(ctx context.Context, in RevokeInviteIn) (InviteRevoked, error) {
	if err := invite.Revoke(ctx, b.q, auth.ActorFromContext(ctx), in.InviteID); err != nil {
		return InviteRevoked{}, insightNotFound(err)
	}
	return InviteRevoked{InviteID: in.InviteID, Revoked: true}, nil
}

// --- Members ----------------------------------------------------------------

func (b *directBackend) SetMemberLanguagePairs(ctx context.Context, in SetMemberLanguagePairsIn) (MemberLanguagePairs, error) {
	tenantID, _ := scope(ctx)
	pairs := make([]userpair.Pair, 0, len(in.Pairs))
	for _, p := range in.Pairs {
		pairs = append(pairs, userpair.Pair{SourceLang: p.SourceLang, TargetLang: p.TargetLang})
	}
	stored, err := userpair.Replace(ctx, b.q, b.pool, tenantID, in.UserID, pairs)
	var inputErr userpair.InputError
	if errors.As(err, &inputErr) {
		return MemberLanguagePairs{}, invalidf("%s", inputErr.Msg)
	}
	if err != nil {
		return MemberLanguagePairs{}, insightNotFound(err)
	}
	return MemberLanguagePairs{UserID: in.UserID, Pairs: pairViews(stored)}, nil
}

// memberError maps the shared membership rules onto MCP errors.
func memberError(err error) error {
	var rule auth.MemberRuleError
	switch {
	case errors.As(err, &rule):
		return invalidf("%s", rule.Msg)
	case errors.Is(err, auth.ErrNotMember):
		return errNotFound
	default:
		return err
	}
}

// memberName is the member's display name, for previews.
func (b *directBackend) memberName(ctx context.Context, userID int64) (string, error) {
	u, err := b.q.GetUser(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("get user: %w", err)
	}
	return u.Name, nil
}

// activeJobCount is how many jobs have their current step assigned to the
// member and in progress — the same count openloc_list_members serves.
func (b *directBackend) activeJobCount(ctx context.Context, userID int64) (int64, error) {
	tenantID, _ := scope(ctx)
	members, err := b.q.ListMCPMembers(ctx, db.ListMCPMembersParams{TenantID: tenantID, UserID: userID, Lim: 1})
	if err != nil {
		return 0, fmt.Errorf("list members: %w", err)
	}
	if len(members) == 0 {
		return 0, nil
	}
	return members[0].ActiveJobs, nil
}

func (b *directBackend) PreviewChangeMemberRole(ctx context.Context, in ChangeMemberRoleIn) (Preview, error) {
	target, err := auth.CheckRoleChange(ctx, b.q, auth.ActorFromContext(ctx), in.UserID, in.Role)
	if err != nil {
		return Preview{}, memberError(err)
	}
	name, err := b.memberName(ctx, in.UserID)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Summary: fmt.Sprintf("Change %s from %s to %s.", name, target.Role, in.Role)}
	switch {
	case in.Role == auth.RoleLinguist:
		p.Effects = append(p.Effects, "As a linguist they only see their own tasks and the jobs assigned to them.")
	case in.Role == auth.RoleProjectManager && target.Role != auth.RoleLinguist:
		p.Effects = append(p.Effects, "They can no longer change organization settings or member roles.")
	case in.Role == auth.RoleAdmin || in.Role == auth.RoleOwner:
		p.Effects = append(p.Effects, "They can change organization settings and member roles.")
	}
	n, err := b.activeJobCount(ctx, in.UserID)
	if err != nil {
		return Preview{}, err
	}
	if n > 0 {
		p.Effects = append(p.Effects, fmt.Sprintf("They are working on %d job(s) right now; those assignments stay.", n))
	}
	return p, nil
}

func (b *directBackend) ChangeMemberRole(ctx context.Context, in ChangeMemberRoleIn) (MemberRoleChanged, error) {
	target, err := auth.ChangeMemberRole(ctx, b.q, auth.ActorFromContext(ctx), in.UserID, in.Role)
	if err != nil {
		return MemberRoleChanged{}, memberError(err)
	}
	return MemberRoleChanged{UserID: in.UserID, PreviousRole: target.Role, Role: in.Role}, nil
}

func (b *directBackend) PreviewRemoveMember(ctx context.Context, in RemoveMemberIn) (Preview, error) {
	target, err := auth.CheckRemoveMember(ctx, b.q, auth.ActorFromContext(ctx), in.UserID)
	if err != nil {
		return Preview{}, memberError(err)
	}
	name, err := b.memberName(ctx, in.UserID)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{
		Summary: fmt.Sprintf("Remove %s (%s) from this organization.", name, target.Role),
		Effects: []string{"They lose access to this organization. Their account and their other organizations stay."},
	}
	n, err := b.activeJobCount(ctx, in.UserID)
	if err != nil {
		return Preview{}, err
	}
	if n > 0 {
		p.Effects = append(p.Effects, fmt.Sprintf("%d job(s) they are working on stay assigned to them and need a new assignee.", n))
	}
	return p, nil
}

func (b *directBackend) RemoveMember(ctx context.Context, in RemoveMemberIn) (MemberRemoved, error) {
	if err := b.needPool("removing a member"); err != nil {
		return MemberRemoved{}, err
	}
	if err := auth.RemoveMember(ctx, b.pool, auth.ActorFromContext(ctx), in.UserID); err != nil {
		return MemberRemoved{}, memberError(err)
	}
	return MemberRemoved{UserID: in.UserID, Removed: true}, nil
}

// --- Notifications ----------------------------------------------------------

func notificationView(n db.Notification) NotificationView {
	return NotificationView{
		ID: n.ID, EventType: n.EventType, Subject: n.Subject, Preview: n.BodyPreview,
		ReferenceType: n.ReferenceType, ReferenceID: n.ReferenceID, Read: n.Read, CreatedAt: tsString(n.CreatedAt),
	}
}

func (b *directBackend) ListNotifications(ctx context.Context, in ListNotificationsIn) (NotificationList, error) {
	tenantID, userID := scope(ctx)
	page, err := notification.List(ctx, b.q, tenantID, userID, in.UnreadOnly, in.Cursor, in.Limit)
	if err != nil {
		return NotificationList{}, err
	}
	unread, err := notification.UnreadCount(ctx, b.q, tenantID, userID)
	if err != nil {
		return NotificationList{}, err
	}
	out := make([]NotificationView, 0, len(page.Notifications))
	for _, n := range page.Notifications {
		out = append(out, notificationView(n))
	}
	return NotificationList{
		Notifications: out, Count: len(out), UnreadCount: unread,
		NextCursor: page.NextCursor, HasMore: page.HasMore,
	}, nil
}

func (b *directBackend) GetNotificationPreferences(ctx context.Context, _ GetNotificationPreferencesIn) (notification.Preferences, error) {
	_, userID := scope(ctx)
	return notification.GetPreferences(ctx, b.q, userID)
}

func (b *directBackend) MarkNotificationsRead(ctx context.Context, in MarkNotificationsReadIn) (NotificationsMarked, error) {
	tenantID, userID := scope(ctx)
	switch {
	case in.All && len(in.IDs) > 0:
		return NotificationsMarked{}, invalidf("pass either ids or all=true, not both")
	case in.All:
		if err := notification.MarkAllRead(ctx, b.q, tenantID, userID); err != nil {
			return NotificationsMarked{}, err
		}
	case len(in.IDs) > 0:
		if len(in.IDs) > notification.MaxPageSize {
			return NotificationsMarked{}, invalidf("at most %d ids per call", notification.MaxPageSize)
		}
		if err := notification.MarkRead(ctx, b.q, tenantID, userID, in.IDs); err != nil {
			return NotificationsMarked{}, err
		}
	default:
		return NotificationsMarked{}, invalidf("pass ids from openloc_list_notifications, or all=true")
	}
	unread, err := notification.UnreadCount(ctx, b.q, tenantID, userID)
	if err != nil {
		return NotificationsMarked{}, err
	}
	return NotificationsMarked{UnreadCount: unread}, nil
}

func (b *directBackend) UpdateNotificationPreferences(ctx context.Context, in UpdateNotificationPreferencesIn) (notification.Preferences, error) {
	_, userID := scope(ctx)
	if in.EmailAssignment == nil && in.EmailRejection == nil && in.EmailCompletion == nil && in.EmailDeadline == nil && in.EmailIssueUpdates == nil {
		return notification.Preferences{}, invalidf("set at least one preference")
	}
	return notification.UpdatePreferences(ctx, b.q, userID, notification.PreferencesPatch{
		EmailAssignment: in.EmailAssignment, EmailRejection: in.EmailRejection,
		EmailCompletion: in.EmailCompletion, EmailDeadline: in.EmailDeadline,
		EmailIssueUpdates: in.EmailIssueUpdates,
	})
}

// inviteRuleError maps an invite.RuleError onto the MCP error kinds; other
// errors pass through.
func inviteRuleError(err error) error {
	var rule invite.RuleError
	if errors.As(err, &rule) {
		if rule.Status == http.StatusTooManyRequests {
			return errLimited{rule.Msg}
		}
		return invalidf("%s", rule.Msg)
	}
	return err
}
