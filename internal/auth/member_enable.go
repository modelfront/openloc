package auth

import (
	"net/http"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// HandleEnableMember enables a member whose membership this org disabled for
// disuse (only vendor orgs disable them). Owner/admin: in a vendor org that is
// its admins; a project manager or linguist enables nobody. 404 when the
// person has no disabled membership here.
func (h *Handler) HandleEnableMember(w http.ResponseWriter, r *http.Request) {
	userID, err := httputil.ParseID(r, "userID")
	if err != nil {
		httputil.BadRequest(w, "invalid user ID")
		return
	}
	ctx := r.Context()
	m, err := h.queries.EnableMembership(ctx, db.EnableMembershipParams{TenantID: TenantIDFromContext(ctx), UserID: userID})
	if httputil.DBError(w, err, "disabled member") {
		return
	}
	logging.Info(ctx, "enabled a disabled member", "member-enabled", logging.UserID(m.UserID))
	httputil.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleEnableVendorAdmin lets a client org enable an admin that one of its
// vendor orgs disabled for disuse, whether or not the vendor has another
// admin. Owner/admin of the client; a vendor org never reaches it (the
// barrier refuses the route). 404 unless the person is a disabled admin of a
// vendor of this client.
func (h *Handler) HandleEnableVendorAdmin(w http.ResponseWriter, r *http.Request) {
	vendorID, err := httputil.ParseID(r, "vendorID")
	if err != nil {
		httputil.BadRequest(w, "invalid vendor ID")
		return
	}
	userID, err := httputil.ParseID(r, "userID")
	if err != nil {
		httputil.BadRequest(w, "invalid user ID")
		return
	}
	ctx := r.Context()
	clientID := TenantIDFromContext(ctx)
	m, err := h.queries.EnableVendorAdmin(ctx, db.EnableVendorAdminParams{ClientTenantID: &clientID, VendorTenantID: vendorID, UserID: userID})
	if httputil.DBError(w, err, "disabled vendor admin") {
		return
	}
	logging.Info(ctx, "enabled a disabled vendor admin", "vendor-admin-enabled",
		logging.TenantID(vendorID), logging.UserID(m.UserID))
	httputil.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
