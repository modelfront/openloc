package database_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

const (
	verBeforeVendorOrgs = int64(20261026081409)
	verVendorOrgs       = int64(20261030164237)
)

// rejects runs a statement that must fail on the named constraint.
func (f *routerFixture) rejects(constraint, query string, args ...any) {
	f.t.Helper()
	_, err := f.db.Exec(query, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != constraint {
		f.t.Errorf("%s: err = %v, want a violation of %s", query, err, constraint)
	}
}

// TestMigrationVendorOrgs verifies migration 20261030164237: existing tenants
// and invites stay valid, a vendor org must name its client and only a vendor
// org may, a vendor-org invite must name the org and only it may, and Down
// removes vendor-org invites and keeps vendor orgs as stand-alone orgs.
func TestMigrationVendorOrgs(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verBeforeVendorOrgs); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}
	client := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Client','client') RETURNING id`)
	user := f.scanID("insert user", `INSERT INTO users (email, name) VALUES ('inviter@test.com','I') RETURNING id`)
	const insertInvite = `INSERT INTO invites (tenant_id, email, role, invited_by, token, expires_at)
		VALUES ($1, $2, 'linguist', $3, $4, now() + interval '7 days') RETURNING id`
	oldInvite := f.scanID("insert invite", insertInvite, client, "old@test.com", user, "tok-old")

	if err := goose.UpTo(sqldb, dir, verVendorOrgs); err != nil {
		t.Fatalf("up to vendor-orgs migration: %v", err)
	}

	f.count(1, `SELECT count(*) FROM tenants WHERE id = $1 AND kind = 'customer' AND client_tenant_id IS NULL`, client)
	f.count(1, `SELECT count(*) FROM invites WHERE id = $1 AND kind = 'member' AND vendor_org_name IS NULL`, oldInvite)

	f.rejects("tenants_vendor_has_client",
		`INSERT INTO tenants (name, slug, kind) VALUES ('No client','no-client','vendor')`)
	f.rejects("tenants_vendor_has_client",
		`INSERT INTO tenants (name, slug, client_tenant_id) VALUES ('Customer','customer-with-client',$1)`, client)
	f.rejects("tenants_kind_check",
		`INSERT INTO tenants (name, slug, kind, client_tenant_id) VALUES ('Bad','bad-kind','partner',$1)`, client)
	vendor := f.scanID("insert vendor org",
		`INSERT INTO tenants (name, slug, kind, client_tenant_id) VALUES ('Vendor','vendor','vendor',$1) RETURNING id`, client)

	const insertKindInvite = `INSERT INTO invites (tenant_id, email, role, invited_by, token, expires_at, kind, vendor_org_name)
		VALUES ($1, $2, 'admin', $3, $4, now() + interval '7 days', $5, $6)`
	f.rejects("invites_vendor_org_has_name", insertKindInvite, client, "a@test.com", user, "tok-a", "vendor_org", nil)
	f.rejects("invites_vendor_org_has_name", insertKindInvite, client, "b@test.com", user, "tok-b", "member", "Named")
	f.rejects("invites_kind_check", insertKindInvite, client, "c@test.com", user, "tok-c", "partner", nil)
	f.exec("insert vendor-org invite", insertKindInvite, client, "d@test.com", user, "tok-d", "vendor_org", "Vendor Two")

	// Deleting the client deletes its vendor orgs.
	other := f.scanID("insert second client", `INSERT INTO tenants (name, slug) VALUES ('Other','other') RETURNING id`)
	otherVendor := f.scanID("insert its vendor org",
		`INSERT INTO tenants (name, slug, kind, client_tenant_id) VALUES ('OV','ov','vendor',$1) RETURNING id`, other)
	f.exec("delete second client", `DELETE FROM tenants WHERE id = $1`, other)
	f.count(0, `SELECT count(*) FROM tenants WHERE id = $1`, otherVendor)

	if err := goose.DownTo(sqldb, dir, verBeforeVendorOrgs); err != nil {
		t.Fatalf("down to pre-migration: %v", err)
	}
	f.count(0, `SELECT count(*) FROM invites WHERE email = 'd@test.com'`)
	f.count(1, `SELECT count(*) FROM invites WHERE id = $1`, oldInvite)
	f.count(1, `SELECT count(*) FROM tenants WHERE id = $1 AND kind = 'customer'`, vendor)
}
