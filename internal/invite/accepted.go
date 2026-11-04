package invite

import (
	"context"
	"fmt"
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// acceptedPageMax caps one page of the Accepted invites list.
const acceptedPageMax = 100

// ListAccepted returns one page of the accepted invites in the actor's tenant
// that the actor may handle, newest acceptance first, and how many match in
// all. q matches the email or the vendor org's name.
func ListAccepted(ctx context.Context, q db.Querier, actor auth.MemberActor, search *string, off, limit int32) ([]View, int64, error) {
	out := make([]View, 0)
	role, err := handlerRole(ctx, q, actor)
	if err != nil {
		return nil, 0, err
	}
	roles := handledRoles(role)
	if len(roles) == 0 {
		return out, 0, nil
	}
	rows, err := q.ListAcceptedInvitesPage(ctx, db.ListAcceptedInvitesPageParams{
		TenantID: actor.TenantID, Roles: roles, Q: search, Off: off, Lim: limit,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list accepted invites: %w", err)
	}
	var total int64
	for i, r := range rows {
		if i == 0 {
			total = r.Total
		}
		out = append(out, View{
			ID: r.ID, TenantID: r.TenantID, Email: r.Email, Role: r.Role,
			Kind: r.Kind, VendorOrgName: r.VendorOrgName, InvitedBy: r.InvitedBy,
			AcceptedAt: r.AcceptedAt, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
		})
	}
	return out, total, nil
}

// HandleListAccepted serves the Members tab's Accepted invites: one page,
// searched by email or vendor name; the cursor is the row offset. Managers,
// each seeing the invites they may handle (see mayHandle).
func (h *Handler) HandleListAccepted(w http.ResponseWriter, r *http.Request) {
	page, err := httputil.ParseOffsetPage(r, acceptedPageMax)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	views, total, err := ListAccepted(r.Context(), h.queries, auth.ActorFromContext(r.Context()), page.Q, page.Off(), page.Limit+1)
	if err != nil {
		logging.Error(r.Context(), err, "listing accepted invites failed", "invite.list_accepted_failed")
		httputil.ServerError(w, "list", "accepted invites")
		return
	}
	hasMore := len(views) > int(page.Limit)
	if hasMore {
		views = views[:page.Limit]
	}
	total = httputil.PageTotal(page.Cursor, len(views), total)
	httputil.Paginated(w, views, page.Cursor+int64(len(views)), hasMore, total)
}
