package database_test

import (
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	verBeforePairsPerOrg = int64(20261103103118)
	verPairsPerOrg       = int64(20261103104204)
)

// TestMigrationLanguagePairsPerOrg verifies migration 20261103104204 on
// existing pairs: a person in two orgs gets a copy of every pair in each, with
// its row group, a person in no org keeps none, and Down merges each person's
// copies back into one set, each pair keeping its lowest row group.
func TestMigrationLanguagePairsPerOrg(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verBeforePairsPerOrg); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}
	alpha := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	beta := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Beta','beta') RETURNING id`)
	both := f.scanID("insert user", `INSERT INTO users (email, name) VALUES ('both@test.com','B') RETURNING id`)
	nowhere := f.scanID("insert user", `INSERT INTO users (email, name) VALUES ('nowhere@test.com','N') RETURNING id`)
	f.exec("memberships", `INSERT INTO memberships (user_id, tenant_id) VALUES ($1,$2), ($1,$3)`, both, alpha, beta)
	f.exec("pairs", `INSERT INTO user_language_pairs (user_id, source_lang, target_lang, row_group)
		VALUES ($1,'en','de',0), ($1,'en','fr',2), ($2,'en','es',0)`, both, nowhere)

	if err := goose.UpTo(sqldb, dir, verPairsPerOrg); err != nil {
		t.Fatalf("up to the per-org migration: %v", err)
	}
	f.count(4, `SELECT count(*) FROM user_language_pairs WHERE user_id = $1`, both)
	for _, tenant := range []int64{alpha, beta} {
		f.count(1, `SELECT count(*) FROM user_language_pairs
			WHERE user_id = $1 AND tenant_id = $2 AND target_lang = 'de' AND row_group = 0`, both, tenant)
		f.count(1, `SELECT count(*) FROM user_language_pairs
			WHERE user_id = $1 AND tenant_id = $2 AND target_lang = 'fr' AND row_group = 2`, both, tenant)
	}
	f.count(0, `SELECT count(*) FROM user_language_pairs WHERE user_id = $1`, nowhere)

	// Alpha regroups en→fr into row 1; Beta keeps row 2.
	f.exec("regroup", `UPDATE user_language_pairs SET row_group = 1
		WHERE user_id = $1 AND tenant_id = $2 AND target_lang = 'fr'`, both, alpha)
	if err := goose.DownTo(sqldb, dir, verBeforePairsPerOrg); err != nil {
		t.Fatalf("down: %v", err)
	}
	f.count(2, `SELECT count(*) FROM user_language_pairs WHERE user_id = $1`, both)
	f.count(1, `SELECT count(*) FROM user_language_pairs WHERE user_id = $1 AND target_lang = 'fr' AND row_group = 1`, both)
	f.count(0, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'user_language_pairs' AND column_name = 'tenant_id'`)
}
