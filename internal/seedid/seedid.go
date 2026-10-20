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

	// VendorEmail is a linguist in the seed organization.
	VendorEmail = "vendor@" + EmailDomain
	VendorName  = "Val Vendor"

	// OperatorEmail is the dev and test platform admin (PLATFORM_ADMIN_EMAILS
	// in the Makefile and the e2e runners), who reads reported issues. An
	// admin of the second seed org only: it is outside EmailDomain, whose open
	// registration would make it a member of the seed organization at sign-in.
	OperatorEmail = "ops@operators.example.com"
	OperatorName  = "Oscar Operator"
)
