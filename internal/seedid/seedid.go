// Package seedid names the development seed's organization and people.
//
// `make seed` creates them, dev login signs in as the owner when no email is
// given, and the dev scenario seeders pick their assignees by EmailDomain. The
// e2e suite mirrors these values in e2e/seed-identity.ts.
package seedid

const (
	// OrgName and OrgSlug identify the seed organization.
	OrgName = "Example Org"
	OrgSlug = "example-org"

	// EmailDomain is the seed organization's domain. Open registration on it
	// lets any address there join without an invite. It is a reserved
	// example domain, distinct from the @example.com addresses tests create,
	// so seeders can tell seed people apart from test leftovers.
	EmailDomain = "seed.example.com"

	// OwnerEmail is the seed organization's owner and the dev-login default.
	OwnerEmail = "owner@" + EmailDomain
	OwnerName  = "Olivia Owner"

	// LinguistEmail is an in-house linguist in the seed organization, a
	// member of it, unlike the vendor org's people below.
	LinguistEmail = "linguist@" + EmailDomain
	LinguistName  = "Ivy Inhouse"

	// VendorOrgName and VendorOrgSlug identify the seed vendor organization,
	// a vendor org of the seed organization.
	VendorOrgName = "Acme Vendors"
	VendorOrgSlug = "acme-vendors"

	// VendorOrgEmailDomain is the vendor org's people's domain. It is not
	// EmailDomain, whose open registration would also make them members of
	// the seed organization, their client.
	VendorOrgEmailDomain = "vendors." + EmailDomain

	// The vendor org's admin (it has no owner), project manager and linguist.
	VendorAdminEmail    = "vendor-admin@" + VendorOrgEmailDomain
	VendorAdminName     = "Vera Admin"
	VendorPMEmail       = "vendor-pm@" + VendorOrgEmailDomain
	VendorPMName        = "Pablo Manager"
	VendorLinguistEmail = "vendor-linguist@" + VendorOrgEmailDomain
	VendorLinguistName  = "Lina Linguist"
	// Idle vendor people: an admin and a linguist disabled after 90 days
	// without use, and a linguist the next sweep warns.
	VendorIdleAdminEmail    = "vendor-admin-idle@" + VendorOrgEmailDomain
	VendorIdleAdminName     = "Dana Dormant"
	VendorIdleLinguistEmail = "vendor-linguist-idle@" + VendorOrgEmailDomain
	VendorIdleLinguistName  = "Lars Lapsed"
	VendorDueLinguistEmail  = "vendor-linguist-due@" + VendorOrgEmailDomain
	VendorDueLinguistName   = "Wanda Waiting"

	// OperatorEmail is the dev and test platform admin (PLATFORM_ADMIN_EMAILS
	// in the Makefile and the e2e runners), who reads reported issues. An
	// admin of the second seed org only: it is outside EmailDomain, whose open
	// registration would make it a member of the seed organization at sign-in.
	OperatorEmail = "ops@operators.example.com"
	OperatorName  = "Oscar Operator"
)
