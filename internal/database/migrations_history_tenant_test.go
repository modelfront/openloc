package database_test

import (
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	verBeforeHistoryTenant = int64(20261019060915)
	verHistoryTenant       = int64(20261019061807)
)

// TestMigrationHistoryTenantBackfill verifies migration 20261019061807 on a
// table that already holds history: every row of two tenants, across more
// than one 10,000-id backfill window, gets its project's tenant, the NOT NULL
// check is validated, and Down removes the column again.
func TestMigrationHistoryTenantBackfill(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verBeforeHistoryTenant); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}
	seed := func(slug string, rows int) int64 {
		tenant := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ($1, $1) RETURNING id`, slug)
		profile := f.scanID("insert profile", `INSERT INTO settings_profiles (tenant_id) VALUES ($1) RETURNING id`, tenant)
		project := f.scanID("insert project",
			`INSERT INTO projects (tenant_id, settings_profile_id, name, source_language, target_languages)
			 VALUES ($1,$2,'P','en','{de}') RETURNING id`, tenant, profile)
		file := f.scanID("insert file",
			`INSERT INTO files (project_id, name, format, storage_path) VALUES ($1,'f.txt','txt','x') RETURNING id`, project)
		f.exec("insert segments", `INSERT INTO segments (file_id, source_text, position)
			SELECT $1, 's', g FROM generate_series(1, 100) g`, file)
		f.exec("insert translations", `INSERT INTO translations (segment_id, target_language, status)
			SELECT id, 'de', 'needs_review' FROM segments WHERE file_id = $1`, file)
		f.exec("insert history", `INSERT INTO translation_history (translation_id, kind)
			SELECT t.id, 'mt' FROM translations t JOIN segments s ON s.id = t.segment_id,
			     generate_series(1, $2::int / 100)
			WHERE s.file_id = $1`, file, rows)
		return tenant
	}
	seed("alpha", 15000)
	seed("beta", 10000)

	if err := goose.UpTo(sqldb, dir, verHistoryTenant); err != nil {
		t.Fatalf("up to the tenant_id migration: %v", err)
	}
	f.count(25000, `SELECT count(*) FROM translation_history`)
	f.count(0, `SELECT count(*) FROM translation_history th
		JOIN translations t ON t.id = th.translation_id JOIN segments s ON s.id = t.segment_id
		JOIN files fl ON fl.id = s.file_id JOIN projects p ON p.id = fl.project_id
		WHERE th.tenant_id IS DISTINCT FROM p.tenant_id`)
	f.count(1, `SELECT count(*) FROM pg_constraint
		WHERE conname = 'translation_history_tenant_id_not_null' AND convalidated`)

	if err := goose.DownTo(sqldb, dir, verBeforeHistoryTenant); err != nil {
		t.Fatalf("down: %v", err)
	}
	f.count(0, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'translation_history' AND column_name = 'tenant_id'`)
}
