package api

import (
	"context"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	notif "openloc/internal/notification"
	"openloc/internal/projectevents"
)

// vendorNoticeTimeout bounds the client's bells, which outlive the request.
const vendorNoticeTimeout = 10 * time.Second

// notifyVendorAdminAdded rings the bell of the client's owners and admins when
// a vendor org makes someone an admin: they see the vendor's admins in their
// Vendors list and should know who now runs it. Only a change that makes a
// new admin rings; a failure is logged and never undoes the role change.
func notifyVendorAdminAdded(reqCtx context.Context, q db.Querier, n *notif.Service, actor auth.MemberActor, prev db.Membership, role string) {
	if n == nil || role != auth.RoleAdmin || prev.Role == auth.RoleAdmin {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), vendorNoticeTimeout)
	defer cancel()
	vendor, err := q.GetTenant(ctx, actor.TenantID)
	if err != nil {
		logging.Error(ctx, err, "loading the org for a vendor admin notice failed", "vendor-admin-notice-failed")
		return
	}
	if vendor.Kind != auth.TenantKindVendor || vendor.ClientTenantID == nil {
		return
	}
	member, err := q.GetUser(ctx, prev.UserID)
	if err != nil {
		logging.Error(ctx, err, "loading the new vendor admin failed", "vendor-admin-notice-failed", logging.UserID(prev.UserID))
		return
	}
	promoter, err := q.GetUser(ctx, actor.UserID)
	if err != nil {
		logging.Error(ctx, err, "loading the promoting vendor admin failed", "vendor-admin-notice-failed")
		return
	}
	recipients, err := q.ListTenantAdminIDs(ctx, *vendor.ClientTenantID)
	if err != nil {
		logging.Error(ctx, err, "loading the client's admins for a vendor admin notice failed", "vendor-admin-notice-failed")
		return
	}
	e := notif.Event{
		Type: notif.EventVendorAdminAdded, TenantID: *vendor.ClientTenantID,
		ActorID: actor.UserID, ActorName: projectevents.PersonName(promoter),
		VendorOrgID: vendor.ID, VendorOrgName: vendor.Name, MemberName: memberLabel(member),
	}
	if err := n.SendBells(ctx, e, recipients); err != nil {
		logging.Error(ctx, err, "sending the vendor admin notice failed", "vendor-admin-notice-failed")
	}
}

// memberLabel names a person with their email, which is how the client's
// Vendors list shows a vendor admin.
func memberLabel(u db.User) string {
	if name := projectevents.PersonName(u); name != u.Email {
		return name + " (" + u.Email + ")"
	}
	return u.Email
}
