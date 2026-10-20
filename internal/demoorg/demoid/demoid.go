// Package demoid names the shared demo organization. It
// holds constants only, so a test or package can refer to the demo tenant and
// its guide without importing internal/demoorg and the whole demo dataset.
// Code finds the tenant with GetDemoTenant and the guide with GetTenantOwner.
package demoid

// Name and Slug of the demo tenant.
const (
	Name = "Playground"
	Slug = "playground"
)

// GuideEmail is the synthetic owner every visitor of the demo organization
// acts as. example.com is a reserved domain and the user has no password, so
// nobody can sign in as the guide.
const GuideEmail = "alex.morgan@demo.example.com"
