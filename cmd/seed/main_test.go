// Tests for the seed binary's heal functions.
//
// Skipped with -short because they spin up a testcontainer Postgres.
package main

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/seedid"
	"openloc/internal/testutil"
)

// TestHealTenantFields_EnablesOpenRegistration pins that healTenantFields sets
// open_registration = true and keeps the domain the OAuth query matches:
// tryOpenRegistration() requires
// `WHERE domain = $1 AND open_registration = true`, and the column defaults to
// false.
func TestHealTenantFields_EnablesOpenRegistration(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Fresh tenant — same as seedCoreData creates
	tenant, err := q.CreateTenant(ctx, db.CreateTenantParams{
		Name: seedid.OrgName,
		Slug: seedid.OrgSlug,
	})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	// Sanity: defaults before heal — open_registration must be false
	var beforeOpenReg bool
	if err := pool.QueryRow(ctx,
		"SELECT open_registration FROM tenants WHERE id = $1", tenant.ID,
	).Scan(&beforeOpenReg); err != nil {
		t.Fatalf("pre-heal query: %v", err)
	}
	if beforeOpenReg {
		t.Fatalf("pre-heal: expected open_registration=false by default, got true")
	}

	// Run the heal
	healTenantFields(ctx, pool, tenant.ID)

	// Verify the heal set open_registration=true AND domain=seedid.EmailDomain.
	// Both are required for OAuth open-registration to work — the query is
	// `WHERE domain = $1 AND open_registration = true`.
	var afterOpenReg bool
	var afterDomain *string
	if err := pool.QueryRow(ctx,
		"SELECT open_registration, domain FROM tenants WHERE id = $1", tenant.ID,
	).Scan(&afterOpenReg, &afterDomain); err != nil {
		t.Fatalf("post-heal query: %v", err)
	}
	if !afterOpenReg {
		t.Errorf("healTenantFields MUST set open_registration=true, got false. " +
			"Any fresh user on the seed domain would get 'No account found' on login.")
	}
	if afterDomain == nil || *afterDomain != seedid.EmailDomain {
		got := "<nil>"
		if afterDomain != nil {
			got = *afterDomain
		}
		t.Errorf("healTenantFields MUST set domain=%s, got %q. "+
			"Without this, the OAuth open-registration query can't match the tenant.", seedid.EmailDomain, got)
	}
}

// TestHealTenantFields_Idempotent verifies that running the heal twice is a
// no-op on the second call (already-enabled state is preserved).
func TestHealTenantFields_Idempotent(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	tenant, err := q.CreateTenant(ctx, db.CreateTenantParams{
		Name: seedid.OrgName,
		Slug: seedid.OrgSlug,
	})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	healTenantFields(ctx, pool, tenant.ID)
	healTenantFields(ctx, pool, tenant.ID) // run again — must not error or flip state back

	var openReg bool
	if err := pool.QueryRow(ctx,
		"SELECT open_registration FROM tenants WHERE id = $1", tenant.ID,
	).Scan(&openReg); err != nil {
		t.Fatalf("post-heal query: %v", err)
	}
	if !openReg {
		t.Errorf("idempotency: second heal must preserve open_registration=true")
	}
}

// TestHealDemoFile_TargetsDemoProject pins that demo data binds to the project
// named "Demo Project", never the newest one: ListProjects is
// `ORDER BY created_at DESC`, so any project created after the seed would
// otherwise receive demo.txt and its jobs.
func TestHealDemoFile_TargetsDemoProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, seedid.OrgName)

	// Demo Project first, then a NEWER unrelated project — reproduces a dev box
	// where someone ran seed-spellcheck or an E2E suite after the initial seed.
	demo := testutil.SeedProject(t, q, tenant.ID, demoProjectName, "en", []string{"es", "fr", "de"})
	newer := testutil.SeedProject(t, q, tenant.ID, "Spellcheck QA", "en", []string{"de"})

	healDemoFile(ctx, db.New(pool), pool, tenant.ID)

	demoFiles, err := q.ListFilesByProject(ctx, demo.ID)
	if err != nil {
		t.Fatalf("ListFilesByProject(demo): %v", err)
	}
	if len(demoFiles) == 0 {
		t.Errorf("seed must put demo.txt in %q — got 0 files there", demoProjectName)
	}

	newerFiles, err := q.ListFilesByProject(ctx, newer.ID)
	if err != nil {
		t.Fatalf("ListFilesByProject(newer): %v", err)
	}
	if len(newerFiles) != 0 {
		t.Errorf("seed must not touch unrelated projects — %q got %d file(s)",
			newer.Name, len(newerFiles))
	}
}

// TestHealTemplateAndProject_RecreatesDeletedDemoProject pins that a deleted
// "Demo Project" is recreated even when other projects exist, rather than the
// seed adopting one of them.
func TestHealTemplateAndProject_RecreatesDeletedDemoProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, seedid.OrgName)

	// Prerequisites for template creation (heal returns early without them).
	testutil.SeedMTEngine(t, q, tenant.ID, "LibreTranslate", "libretranslate")
	testutil.SeedTM(t, q, tenant.ID, "Default TM")

	// An unrelated project exists, but Demo Project does not.
	testutil.SeedProject(t, q, tenant.ID, "Spellcheck QA", "en", []string{"de"})

	// The seed attributes what it creates to the org owner, so heal needs an
	// identity to stamp as created_by.
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@seedtest.com", "pass")
	healTemplateAndProject(ctx, db.New(pool), tenant.ID, owner.ID)

	projects, err := q.ListProjects(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	var found bool
	for _, p := range projects {
		if p.Name == demoProjectName {
			found = true
		}
	}
	if !found {
		t.Errorf("heal must recreate %q when it's missing; got projects %v",
			demoProjectName, projectNames(projects))
	}
}

func projectNames(ps []db.Project) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}
