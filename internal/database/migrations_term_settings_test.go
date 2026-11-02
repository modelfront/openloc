package database_test

import (
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	verLinguistsAddTerms        = int64(20261029145209)
	verLinguistTerms            = int64(20261030074505)
	verTermAssignmentsReadWrite = int64(20261030091429)
)

// TestMigrationLinguistTerms verifies migration 20261030074505: the on/off
// linguists_add_terms becomes linguist_terms (on → pending, off → off), and
// its Down turns it back (anything but off → on).
func TestMigrationLinguistTerms(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verLinguistsAddTerms); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}
	tenant := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	on := f.scanID("insert on", `INSERT INTO settings_profiles (tenant_id, linguists_add_terms) VALUES ($1, true) RETURNING id`, tenant)
	off := f.scanID("insert off", `INSERT INTO settings_profiles (tenant_id, linguists_add_terms) VALUES ($1, false) RETURNING id`, tenant)

	if err := goose.UpTo(sqldb, dir, verLinguistTerms); err != nil {
		t.Fatalf("up to linguist-terms migration: %v", err)
	}
	f.count(1, `SELECT count(*) FROM settings_profiles WHERE id = $1 AND linguist_terms = 'pending'`, on)
	f.count(1, `SELECT count(*) FROM settings_profiles WHERE id = $1 AND linguist_terms = 'off'`, off)
	f.count(0, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'settings_profiles' AND column_name = 'linguists_add_terms'`)
	approved := f.scanID("insert approved", `INSERT INTO settings_profiles (tenant_id, linguist_terms) VALUES ($1, 'approved') RETURNING id`, tenant)

	if err := goose.DownTo(sqldb, dir, verLinguistsAddTerms); err != nil {
		t.Fatalf("down: %v", err)
	}
	f.count(1, `SELECT count(*) FROM settings_profiles WHERE id = $1 AND linguists_add_terms`, on)
	f.count(1, `SELECT count(*) FROM settings_profiles WHERE id = $1 AND NOT linguists_add_terms`, off)
	f.count(1, `SELECT count(*) FROM settings_profiles WHERE id = $1 AND linguists_add_terms`, approved)
}

// A database that already has linguist_terms and no linguists_add_terms still
// migrates, keeping its values.
func TestMigrationLinguistTermsAlreadyThere(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verLinguistsAddTerms); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}
	if _, err := sqldb.Exec(`ALTER TABLE settings_profiles DROP COLUMN linguists_add_terms;
		ALTER TABLE settings_profiles ADD COLUMN linguist_terms text NOT NULL DEFAULT 'pending'`); err != nil {
		t.Fatalf("set up the draft schema: %v", err)
	}
	tenant := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	approved := f.scanID("insert approved", `INSERT INTO settings_profiles (tenant_id, linguist_terms) VALUES ($1, 'approved') RETURNING id`, tenant)

	if err := goose.UpTo(sqldb, dir, verLinguistTerms); err != nil {
		t.Fatalf("up to linguist-terms migration: %v", err)
	}
	f.count(1, `SELECT count(*) FROM settings_profiles WHERE id = $1 AND linguist_terms = 'approved'`, approved)
}

// TestMigrationTermAssignmentsReadWrite verifies migration 20261030091429: a
// Read-only term-base row gets Write, other rows keep their mode, and a row
// inserted without a mode gets both.
func TestMigrationTermAssignmentsReadWrite(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verLinguistTerms); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}
	tenant := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	profile := f.scanID("insert profile", `INSERT INTO settings_profiles (tenant_id) VALUES ($1) RETURNING id`, tenant)
	base := f.scanID("insert term base", `INSERT INTO term_bases (tenant_id, name, source_language) VALUES ($1, 'B', 'en') RETURNING id`, tenant)
	row := func(target, mode string) int64 {
		return f.scanID("insert assignment",
			`INSERT INTO settings_terminology_assignments (settings_profile_id, term_base_id, source_language, target_language, mode, priority)
			 VALUES ($1, $2, 'en', $3, $4, 0) RETURNING id`, profile, base, target, mode)
	}
	readOnly, writeOnly, both := row("de", "read"), row("fr", "write"), row("es", "read_write")

	if err := goose.UpTo(sqldb, dir, verTermAssignmentsReadWrite); err != nil {
		t.Fatalf("up to read-write migration: %v", err)
	}
	f.count(1, `SELECT count(*) FROM settings_terminology_assignments WHERE id = $1 AND mode = 'read_write'`, readOnly)
	f.count(1, `SELECT count(*) FROM settings_terminology_assignments WHERE id = $1 AND mode = 'write'`, writeOnly)
	f.count(1, `SELECT count(*) FROM settings_terminology_assignments WHERE id = $1 AND mode = 'read_write'`, both)
	noMode := f.scanID("insert without mode",
		`INSERT INTO settings_terminology_assignments (settings_profile_id, term_base_id, source_language, target_language, priority)
		 VALUES ($1, $2, 'en', 'it', 0) RETURNING id`, profile, base)
	f.count(1, `SELECT count(*) FROM settings_terminology_assignments WHERE id = $1 AND mode = 'read_write'`, noMode)
}
