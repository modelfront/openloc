package auth

import (
	"context"
	"strings"

	db "openloc/internal/database/sqlcgen"

	"openloc/internal/logging"
)

// Membership materialization + landing — shared by every sign-in path (OAuth
// callback, password login, invite accept, dev login).

// tokenForLanding inspects the identity's org memberships and returns the token
// to set plus the SPA path to land on:
//   - exactly 1 org → tenant token for that org, land in the app ("/")
//   - 0 or >1 orgs → short-lived identity token, land on the org picker
func (h *Handler) tokenForLanding(ctx context.Context, userID int64) (token, redirect string, err error) {
	orgs, err := h.queries.ListMembershipsWithTenant(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if len(orgs) == 1 {
		only := orgs[0]
		token, err = GenerateToken(userID, only.TenantID, only.Role, h.jwtSecret, tokenExpiry)
		if err != nil {
			return "", "", err
		}
		if err = h.queries.SetLastTenant(ctx, db.SetLastTenantParams{ID: userID, LastTenantID: &only.TenantID}); err != nil {
			return "", "", err
		}
		return token, "/", nil
	}
	token, err = GenerateIdentityToken(userID, h.jwtSecret)
	if err != nil {
		return "", "", err
	}
	return token, "/select-org", nil
}

// hasJoinableOrg reports whether an unknown email is entitled to join at least
// one org — a pending invite (by token or email) or an open-registration email
// domain. Gates identity creation so we never leave orphan (0-org) accounts for
// random Google sign-ins.
func (h *Handler) hasJoinableOrg(ctx context.Context, email, inviteToken string) bool {
	if inviteToken != "" {
		// Token links honor expiry and the invited address — same
		// rules as materializeMemberships' explicit-token branch.
		if inv, err := h.queries.GetPendingUnexpiredInviteByToken(ctx, inviteToken); err == nil && inv.Email == email {
			return true
		}
	}
	if invites, err := h.queries.GetPendingInvitesByEmail(ctx, email); err == nil {
		for _, inv := range invites {
			if !inv.AcceptedAt.Valid {
				return true
			}
		}
	}
	if parts := strings.SplitN(email, "@", 2); len(parts) == 2 {
		if _, err := h.queries.GetTenantByDomainOpenReg(ctx, &parts[1]); err == nil {
			return true
		}
	}
	return false
}

// materializeMemberships idempotently grants a user every org their identity is
// entitled to: pending invites (accepted here) and email-domain auto-join.
// Best-effort — a single failing grant is logged and skipped so it
// never blocks login. Safe to call on every login / org-list.
//
// The explicit invite token (from the emailed /invite/{token} link) is held to
// stricter rules than the email-matched pass: it must be unexpired AND its
// invited email must match the signed-in identity — a forwarded or leaked link
// must never attach an org to somebody else's account. Email-matched invites
// skip both checks by design (see GetPendingInvitesByEmail in
// invite/queries.sql).
func (h *Handler) materializeMemberships(ctx context.Context, userID int64, email, inviteToken string) {
	seen := map[int64]bool{}
	if inviteToken != "" {
		if inv, err := h.queries.GetPendingUnexpiredInviteByToken(ctx, inviteToken); err == nil {
			if inv.Email == email {
				h.acceptInviteAsMembership(ctx, userID, inv)
				seen[inv.ID] = true
			} else {
				logging.Warn(ctx, nil, "invite token presented by a different email; skipped", "auth-invite-email-mismatch", logging.InviteID(inv.ID))
			}
		}
	}
	if invites, err := h.queries.GetPendingInvitesByEmail(ctx, email); err == nil {
		for _, inv := range invites {
			if inv.AcceptedAt.Valid || seen[inv.ID] {
				continue
			}
			h.acceptInviteAsMembership(ctx, userID, inv)
		}
	}
	h.ensureDomainMembership(ctx, userID, email)
}

// acceptInviteAsMembership creates the membership the invite grants and marks
// the invite accepted. ON CONFLICT keeps an existing role, so re-running is safe.
func (h *Handler) acceptInviteAsMembership(ctx context.Context, userID int64, inv db.Invite) {
	if err := h.queries.CreateMembership(ctx, db.CreateMembershipParams{
		UserID:   userID,
		TenantID: inv.TenantID,
		Role:     inv.Role,
	}); err != nil {
		logging.Error(ctx, err, "invite membership not created", "auth-invite-membership-failed", logging.InviteID(inv.ID))
		return
	}
	if _, err := h.queries.AcceptInvite(ctx, inv.ID); err != nil {
		logging.Error(ctx, err, "invite not marked accepted", "auth-invite-accept-failed", logging.InviteID(inv.ID))
	}
}

// ensureDomainMembership grants a project_manager membership if the email domain
// matches a tenant with open_registration=true. Idempotent.
func (h *Handler) ensureDomainMembership(ctx context.Context, userID int64, email string) {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return
	}
	tenant, err := h.queries.GetTenantByDomainOpenReg(ctx, &parts[1])
	if err != nil {
		return
	}
	if err := h.queries.CreateMembership(ctx, db.CreateMembershipParams{
		UserID:   userID,
		TenantID: tenant.ID,
		Role:     RoleProjectManager,
	}); err != nil {
		logging.Error(ctx, err, "domain auto-join membership failed", "auth-domain-join-failed", logging.UserID(userID))
	}
}
