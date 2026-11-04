package publicapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/invite"
)

// Member is a member of the organization.
type Member struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Active   bool   `json:"active"`
	JoinedAt string `json:"joined_at"`
}

func memberDTO(m db.ListMembersByTenantRow) Member {
	return Member{ID: m.ID, Name: m.Name, Email: m.Email, Role: m.Role, Active: m.IsActive, JoinedAt: timestamp(m.CreatedAt)}
}

// Invite is a pending, accepted or expired invitation. The join link is sent
// by email only and is never returned. Kind member invites someone into the
// organization; kind vendor_org invites someone to set up the vendor
// organization VendorOrgName, as its admin.
type Invite struct {
	ID            int64   `json:"id"`
	Email         string  `json:"email"`
	Role          string  `json:"role"`
	Kind          string  `json:"kind"`
	VendorOrgName *string `json:"vendor_org_name,omitempty"`
	Status        string  `json:"status"`
	ExpiresAt     string  `json:"expires_at"`
	CreatedAt     string  `json:"created_at"`
}

func inviteViewDTO(i invite.View) Invite {
	status := "pending"
	switch {
	case i.AcceptedAt.Valid:
		status = "accepted"
	case i.ExpiresAt.Valid && i.ExpiresAt.Time.Before(time.Now()):
		status = "expired"
	}
	return Invite{
		ID: i.ID, Email: i.Email, Role: i.Role, Kind: i.Kind, VendorOrgName: i.VendorOrgName,
		Status: status, ExpiresAt: timestamp(i.ExpiresAt), CreatedAt: timestamp(i.CreatedAt),
	}
}

func (h Handlers) listMembers(w http.ResponseWriter, r *http.Request) {
	includeInactive, _ := strconv.ParseBool(r.URL.Query().Get("include_inactive"))
	rows, err := h.Queries.ListMembersByTenant(r.Context(), db.ListMembersByTenantParams{
		TenantID: auth.TenantIDFromContext(r.Context()), IncludeInactive: includeInactive,
	})
	if err != nil {
		httputil.ServerError(w, "list", "members")
		return
	}
	writeAll(w, rows, memberDTO)
}

func (h Handlers) changeMemberRole(w http.ResponseWriter, r *http.Request) {
	userID, err := httputil.ParseID(r, "userID")
	if err != nil {
		httputil.BadRequest(w, "invalid user ID")
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	if _, err := auth.ChangeMemberRole(ctx, h.Queries, auth.ActorFromContext(ctx), userID, req.Role); err != nil {
		auth.WriteMemberError(w, err)
		return
	}
	rows, err := h.Queries.ListMembersByTenant(ctx, db.ListMembersByTenantParams{TenantID: auth.TenantIDFromContext(ctx), IncludeInactive: true})
	if err != nil {
		httputil.ServerError(w, "load", "member")
		return
	}
	for _, m := range rows {
		if m.ID == userID {
			httputil.Data(w, memberDTO(m))
			return
		}
	}
	httputil.NotFound(w, "member")
}

func (h Handlers) removeMember(w http.ResponseWriter, r *http.Request) {
	userID, err := httputil.ParseID(r, "userID")
	if err != nil {
		httputil.BadRequest(w, "invalid user ID")
		return
	}
	if err := auth.RemoveMember(r.Context(), h.Pool, auth.ActorFromContext(r.Context()), userID); err != nil {
		auth.WriteMemberError(w, err)
		return
	}
	httputil.NoContent(w)
}

func (h Handlers) listInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := invite.List(r.Context(), h.Queries, auth.ActorFromContext(r.Context()))
	if err != nil {
		httputil.ServerError(w, "list", "invites")
		return
	}
	writeAll(w, rows, inviteViewDTO)
}

// writeInviteError writes an invite rule refusal with its own status.
func writeInviteError(w http.ResponseWriter, err error, verb string) {
	var rule invite.RuleError
	if errors.As(err, &rule) {
		if rule.Status == http.StatusTooManyRequests {
			httputil.TooManyRequests(w, rule.RetryAfter, rule.Msg)
			return
		}
		httputil.Error(w, rule.Status, rule.Msg)
		return
	}
	httputil.ServiceError(w, err, verb, "invite")
}

// createInvite emails an invitation. Nobody invites above their own role, and
// a project manager invites linguists only; the service enforces it.
func (h Handlers) createInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	inv, err := h.Invites.Create(r.Context(), auth.ActorFromContext(r.Context()), invite.NewInvite{Email: req.Email, Role: req.Role})
	if err != nil {
		writeInviteError(w, err, "create")
		return
	}
	httputil.Created(w, inviteViewDTO(inv))
}

func (h Handlers) revokeInvite(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "inviteID")
	if err != nil {
		httputil.BadRequest(w, "invalid invite ID")
		return
	}
	// An invite the caller may not handle answers like a missing one.
	err = invite.Revoke(r.Context(), h.Queries, auth.ActorFromContext(r.Context()), id)
	if errors.Is(err, pgx.ErrNoRows) {
		httputil.NotFound(w, "invite")
		return
	}
	if err != nil {
		writeInviteError(w, err, "revoke")
		return
	}
	httputil.NoContent(w)
}
