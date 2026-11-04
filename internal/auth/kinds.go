package auth

// tenants.kind values besides TenantKindDemo. A vendor org belongs to one
// client org (tenants.client_tenant_id) and has no owner.
const (
	TenantKindCustomer = "customer"
	TenantKindVendor   = "vendor"
)

// invites.kind values. A vendor-org invite lives in the client org and, once
// accepted, creates a vendor org with the invitee as its admin.
const (
	InviteKindMember    = "member"
	InviteKindVendorOrg = "vendor_org"
)
