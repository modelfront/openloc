package database_test

import (
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	verBeforeFilterProfiles = int64(20261026081409)
	verFilterProfiles       = int64(20261029133857)
)

// TestMigrationFilterProfiles verifies migration 20261029133857: a settings
// row with "Strings contain HTML" off gets the Plain text strings built-in
// for JSON, PO, TXT and Excel and nothing else; a row with it on gets no
// pick (every format's Default); the column is dropped. The down migration
// puts the column back from the picks.
func TestMigrationFilterProfiles(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verBeforeFilterProfiles); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}
	tenant := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	settingsRow := func(inlineHTML bool) int64 {
		return f.scanID("insert settings profile",
			`INSERT INTO settings_profiles (tenant_id, inline_html) VALUES ($1, $2) RETURNING id`, tenant, inlineHTML)
	}
	off, on := settingsRow(false), settingsRow(true)

	if err := goose.UpTo(sqldb, dir, verFilterProfiles); err != nil {
		t.Fatalf("up: %v", err)
	}
	f.count(4, `SELECT count(*) FROM settings_profile_filter_profiles WHERE settings_profile_id = $1`, off)
	f.count(4, `SELECT count(*) FROM settings_profile_filter_profiles l JOIN filter_builtin_profiles b ON b.id = l.filter_profile_id
		WHERE l.settings_profile_id = $1 AND b.builtin_key = 'builtin:' || l.format || '-plain@1'
		  AND l.format IN ('json_i18n', 'po', 'txt', 'xlsx')`, off)
	f.count(0, `SELECT count(*) FROM settings_profile_filter_profiles WHERE settings_profile_id = $1`, on)
	f.count(0, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'settings_profiles' AND column_name = 'inline_html'`)

	if err := goose.DownTo(sqldb, dir, verBeforeFilterProfiles); err != nil {
		t.Fatalf("down: %v", err)
	}
	f.count(1, `SELECT count(*) FROM settings_profiles WHERE id = $1 AND NOT inline_html`, off)
	f.count(1, `SELECT count(*) FROM settings_profiles WHERE id = $1 AND inline_html`, on)
	if err := goose.UpTo(sqldb, dir, verFilterProfiles); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
