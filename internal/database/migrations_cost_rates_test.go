package database_test

import (
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	verBeforeDeletePerUserRates = int64(20261017072537)
	verDeletePerUserRates       = int64(20261018012204)
)

// TestMigrationDeletePerUserCostRates verifies migration 20261018012204: every
// per-translator rate is deleted and org-wide rates stay.
func TestMigrationDeletePerUserCostRates(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verBeforeDeletePerUserRates); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}

	tenant := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	user := f.scanID("insert user",
		`INSERT INTO users (last_tenant_id, email, name) VALUES ($1,'rate@test.com','R') RETURNING id`, tenant)
	orgRate := f.scanID("insert org rate",
		`INSERT INTO cost_rates (tenant_id, source_language, target_language, rate_per_word, currency, user_id)
		 VALUES ($1,'en','de',0.05,'USD',NULL) RETURNING id`, tenant)
	f.exec("insert user rate",
		`INSERT INTO cost_rates (tenant_id, source_language, target_language, rate_per_word, currency, user_id)
		 VALUES ($1,'en','de',0.10,'USD',$2)`, tenant, user)

	if err := goose.UpTo(sqldb, dir, verDeletePerUserRates); err != nil {
		t.Fatalf("up to delete-per-user-rates migration: %v", err)
	}

	f.count(0, `SELECT count(*) FROM cost_rates WHERE user_id IS NOT NULL`)
	f.count(1, `SELECT count(*) FROM cost_rates WHERE id = $1 AND user_id IS NULL`, orgRate)
}
