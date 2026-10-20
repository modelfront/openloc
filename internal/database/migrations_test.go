package database_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"openloc/internal/testutil"
)

func migrationsDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "migrations")
}

// setupDB starts a fresh Postgres container and returns a *sql.DB connected to it.
func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := setupDBWithURL(t)
	return db
}

// setupDBWithURL is setupDB plus the connection string, for code that opens
// its own connections.
func setupDBWithURL(t *testing.T) (*sql.DB, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping migration rollback test (-short)")
	}

	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "false")

	ctx := context.Background()
	container, err := postgres.Run(ctx,
		testutil.PostgresImage,
		postgres.WithDatabase("migration_rollback_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		// Wait for the log AND the mapped port: under full-gate docker load the
		// "ready" log can appear before the host port mapping is inspectable,
		// making ConnectionString fail with `port "5432/tcp" not found`
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second),
				wait.ForListeningPort("5432/tcp").
					WithStartupTimeout(30*time.Second),
			),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		container.Terminate(ctx)
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	return db, connStr
}

// TestMigration087DataMigration verifies the deploy-time transform: existing
// flat terminology_entries must each become 1 concept + 1 source term + 1 target
// term (bijective 1:1), including an entry with an empty forbidden target.
func TestMigration087DataMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration data test (-short)")
	}
	sqldb := setupDB(t)
	dir := migrationsDir()

	// Apply everything up to the flat model, before the concept migration (087).
	if err := goose.UpTo(sqldb, dir, 84); err != nil {
		t.Fatalf("up to 84: %v", err)
	}

	var tenantID, baseID int64
	if err := sqldb.QueryRow(
		`INSERT INTO tenants (name, slug) VALUES ('T', 't') RETURNING id`).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := sqldb.QueryRow(
		`INSERT INTO term_bases (tenant_id, name, source_language, target_language)
		 VALUES ($1, 'B', 'en', 'es') RETURNING id`, tenantID).Scan(&baseID); err != nil {
		t.Fatalf("insert term base: %v", err)
	}
	entries := []struct{ src, tgt, status string }{
		{"password", "contraseña", "approved"},
		{"email", "correo electrónico", "approved"},
		{"clave", "", "forbidden"}, // empty forbidden target must survive
	}
	for _, e := range entries {
		if _, err := sqldb.Exec(
			`INSERT INTO terminology_entries (term_base_id, source_term, target_term, status)
			 VALUES ($1, $2, $3, $4)`, baseID, e.src, e.tgt, e.status); err != nil {
			t.Fatalf("insert entry %q: %v", e.src, err)
		}
	}

	// Run the concept migration (087; 085 dashboards + 086 job-timings are unrelated).
	if err := goose.UpTo(sqldb, dir, 87); err != nil {
		t.Fatalf("up to 87: %v", err)
	}

	assertCount := func(query string, want int, args ...any) {
		t.Helper()
		var got int
		if err := sqldb.QueryRow(query, args...).Scan(&got); err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		if got != want {
			t.Errorf("query %q = %d, want %d", query, got, want)
		}
	}

	// 1 concept per entry, 2 terms per concept.
	assertCount(`SELECT count(*) FROM term_concept WHERE term_base_id = $1`, 3, baseID)
	assertCount(`SELECT count(*) FROM term WHERE concept_id IN (SELECT id FROM term_concept WHERE term_base_id = $1)`, 6, baseID)
	// The empty forbidden target survived with its status.
	assertCount(`SELECT count(*) FROM term WHERE language = 'es' AND text = '' AND status = 'forbidden'`, 1)
	// Approved target preserved.
	assertCount(`SELECT count(*) FROM term WHERE language = 'es' AND text = 'contraseña' AND status = 'approved'`, 1)
	// Every source-language term is 'approved'.
	assertCount(`SELECT count(*) FROM term WHERE language = 'en' AND status <> 'approved'`, 0)
	// The flat table is gone; the projection view exists.
	var exists bool
	if err := sqldb.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'terminology_entries')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("terminology_entries should be dropped after 087")
	}
}

// TestMigrationUpDown verifies that all migrations can be applied in order
// and then rolled back one-by-one without errors.
//
// Known limitations:
//   - 001_init.sql: Down drops the entire public schema (squashed migration).
//     Goose then fails to update goose_db_version, so we stop rollback at version 2.
//   - 007_xlsx_format.sql: Adds an enum value; PostgreSQL cannot remove enum values,
//     so the Down section is empty. This is acceptable — enum additions are append-only.
func TestMigrationUpDown(t *testing.T) {
	db := setupDB(t)
	dir := migrationsDir()

	// Collect all migrations via goose
	migrations, err := goose.CollectMigrations(dir, 0, goose.MaxVersion)
	if err != nil {
		t.Fatalf("collect migrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations found")
	}
	t.Logf("found %d migrations", len(migrations))

	// Step 1: Apply all migrations one by one
	for _, m := range migrations {
		if err := goose.UpTo(db, dir, m.Version); err != nil {
			t.Fatalf("up to version %d (%s): %v", m.Version, filepath.Base(m.Source), err)
		}
	}

	currentVersion, err := goose.GetDBVersion(db)
	if err != nil {
		t.Fatalf("get db version after up: %v", err)
	}
	lastMigration := migrations[len(migrations)-1]
	if currentVersion != lastMigration.Version {
		t.Fatalf("expected version %d, got %d", lastMigration.Version, currentVersion)
	}
	t.Logf("all %d migrations applied (version %d)", len(migrations), currentVersion)

	// Step 2: Roll back each migration one by one (newest first).
	// Stop at version 2 because 001_init.sql drops the entire public schema
	// which also removes goose_db_version, causing goose to fail.
	for i := len(migrations) - 1; i >= 1; i-- {
		m := migrations[i]
		if err := goose.Down(db, dir); err != nil {
			t.Fatalf("down from version %d (%s): %v", m.Version, filepath.Base(m.Source), err)
		}
	}

	finalVersion, err := goose.GetDBVersion(db)
	if err != nil {
		t.Fatalf("get db version after rollback: %v", err)
	}
	if finalVersion != 1 {
		t.Fatalf("expected version 1 after rollback (001 can't be rolled back via goose), got %d", finalVersion)
	}
	t.Logf("rolled back to version %d (migrations 002-%d verified)", finalVersion, lastMigration.Version)
}

// TestMigrationUpDownIndividual applies and rolls back each migration individually
// to verify each Up/Down pair is self-consistent.
//
// Migrations that have known rollback limitations are tested for Up only
// and their Down is verified to at least not corrupt the schema.
func TestMigrationUpDownIndividual(t *testing.T) {
	db := setupDB(t)
	dir := migrationsDir()

	migrations, err := goose.CollectMigrations(dir, 0, goose.MaxVersion)
	if err != nil {
		t.Fatalf("collect migrations: %v", err)
	}

	// Migrations with known rollback limitations (test Up only, skip Down+Re-Up cycle).
	// Keys are goose versions: 1-86 for the legacy numeric migrations; timestamped
	// migrations (YYYYMMDDHHMMSS_*.sql) use the full 14-digit timestamp as the key.
	skipDownReUp := map[int64]string{
		1:  "squashed init — Down drops public schema + goose_db_version",
		7:  "ALTER TYPE ADD VALUE — PostgreSQL cannot remove enum values",
		87: "concept/term data migration is lossy — multilingual→flat cannot round-trip",
	}

	for _, m := range migrations {
		name := filepath.Base(m.Source)
		t.Run(fmt.Sprintf("up_down_%s", name), func(t *testing.T) {
			// Apply
			if err := goose.UpTo(db, dir, m.Version); err != nil {
				t.Fatalf("up to %d: %v", m.Version, err)
			}

			ver, _ := goose.GetDBVersion(db)
			if ver != m.Version {
				t.Fatalf("expected version %d after up, got %d", m.Version, ver)
			}

			if reason, skip := skipDownReUp[m.Version]; skip {
				t.Logf("skipping down+re-up: %s", reason)
				return
			}

			// Rollback
			if err := goose.Down(db, dir); err != nil {
				t.Fatalf("down from %d: %v", m.Version, err)
			}

			ver, _ = goose.GetDBVersion(db)
			expected := int64(0)
			if m.Version > 1 {
				for j := 0; j < len(migrations); j++ {
					if migrations[j].Version == m.Version && j > 0 {
						expected = migrations[j-1].Version
						break
					}
				}
			}
			if ver != expected {
				t.Fatalf("expected version %d after down, got %d", expected, ver)
			}

			// Re-apply so the next iteration starts from this version
			if err := goose.UpTo(db, dir, m.Version); err != nil {
				t.Fatalf("re-up to %d: %v", m.Version, err)
			}
		})
	}
}

// TestMigrationPromoteProjectStepsToLibrary verifies migration 20260908133337:
// distinct HUMAN step names already used on a project/template are promoted into
// the step-definition library and the human profile rows are linked to them,
// while hybrid/ai rows and existing definitions are left alone.
func TestMigrationPromoteProjectStepsToLibrary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration data test (-short)")
	}
	sqldb := setupDB(t)
	dir := migrationsDir()

	// Everything up to (but not including) the promotion migration.
	if err := goose.UpTo(sqldb, dir, 20260908115706); err != nil {
		t.Fatalf("up to default-workflow migration: %v", err)
	}

	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	exec := func(q string, args ...any) error { _, err := sqldb.Exec(q, args...); return err }

	var tenantID, profileID int64
	must(sqldb.QueryRow(`INSERT INTO tenants (name, slug) VALUES ('T','t') RETURNING id`).Scan(&tenantID), "insert tenant")
	must(sqldb.QueryRow(`INSERT INTO settings_profiles (tenant_id) VALUES ($1) RETURNING id`, tenantID).Scan(&profileID), "insert profile")

	// An existing library definition — must NOT be duplicated by the migration.
	must(exec(`INSERT INTO step_definitions (tenant_id, name, step_type, role_required) VALUES ($1,'Translation','human','linguist')`, tenantID), "seed Translation def")

	// Profile steps: a custom human step (no def), a human step matching the
	// existing def, and a hybrid step that must stay project-local.
	steps := []struct {
		name, stepType string
		order          int
	}{
		{"Proofread", "human", 0},
		{"Translation", "human", 1},
		{"AI QA", "hybrid", 2},
	}
	for _, s := range steps {
		must(exec(`INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required, step_type)
			VALUES ($1,$2,$3,'linguist',$4)`, profileID, s.name, s.order, s.stepType), "insert step "+s.name)
	}

	if err := goose.UpTo(sqldb, dir, 20260908133337); err != nil {
		t.Fatalf("run promotion migration: %v", err)
	}

	assertCount := func(want int, query string, args ...any) {
		t.Helper()
		var got int
		if err := sqldb.QueryRow(query, args...).Scan(&got); err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		if got != want {
			t.Errorf("query %q = %d, want %d", query, got, want)
		}
	}

	// Proofread promoted; Translation not duplicated; AI QA not promoted.
	assertCount(1, `SELECT count(*) FROM step_definitions WHERE tenant_id=$1 AND lower(name)='proofread' AND deleted_at IS NULL`, tenantID)
	assertCount(1, `SELECT count(*) FROM step_definitions WHERE tenant_id=$1 AND lower(name)='translation' AND deleted_at IS NULL`, tenantID)
	assertCount(0, `SELECT count(*) FROM step_definitions WHERE tenant_id=$1 AND lower(name)='ai qa'`, tenantID)
	// A promoted definition is human with no engine.
	assertCount(1, `SELECT count(*) FROM step_definitions WHERE tenant_id=$1 AND lower(name)='proofread' AND step_type='human' AND ai_system_id IS NULL`, tenantID)

	// Human rows linked to a definition; the hybrid row left unlinked.
	assertCount(1, `SELECT count(*) FROM workflow_steps WHERE settings_profile_id=$1 AND name='Proofread' AND step_definition_id IS NOT NULL`, profileID)
	assertCount(1, `SELECT count(*) FROM workflow_steps WHERE settings_profile_id=$1 AND name='Translation' AND step_definition_id IS NOT NULL`, profileID)
	assertCount(1, `SELECT count(*) FROM workflow_steps WHERE settings_profile_id=$1 AND name='AI QA' AND step_definition_id IS NULL`, profileID)
}

// TestMigrationPromoteNonHumanProjectSteps verifies migration 20260908135715:
// hybrid/ai project steps are promoted into the library carrying their engine,
// a name already taken (e.g. a human "Review") is left project-local, and a
// same-named row with a different engine does not link to the new definition.
func TestMigrationPromoteNonHumanProjectSteps(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration data test (-short)")
	}
	sqldb := setupDB(t)
	dir := migrationsDir()

	// Everything up to (but not including) the non-human promotion migration.
	if err := goose.UpTo(sqldb, dir, 20260908133337); err != nil {
		t.Fatalf("up to human-promotion migration: %v", err)
	}

	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	exec := func(q string, args ...any) error { _, err := sqldb.Exec(q, args...); return err }
	scanID := func(q string, args ...any) int64 {
		t.Helper()
		var id int64
		must(sqldb.QueryRow(q, args...).Scan(&id), "scan id: "+q)
		return id
	}

	tenantID := scanID(`INSERT INTO tenants (name, slug) VALUES ('T','t') RETURNING id`)
	profileID := scanID(`INSERT INTO settings_profiles (tenant_id) VALUES ($1) RETURNING id`, tenantID)
	// ai_systems' engine config lives in a separate rows table (migration 068);
	// the base row only needs a name, which is all the FK needs here.
	engine1 := scanID(`INSERT INTO ai_systems (tenant_id, name) VALUES ($1,'E1') RETURNING id`, tenantID)
	engine2 := scanID(`INSERT INTO ai_systems (tenant_id, name) VALUES ($1,'E2') RETURNING id`, tenantID)

	// A pre-existing human definition whose name a hybrid row also uses.
	must(exec(`INSERT INTO step_definitions (tenant_id, name, step_type, role_required) VALUES ($1,'Review','human','linguist')`, tenantID), "seed human Review def")

	insertStep := func(name, stepType string, order int, engine int64) {
		must(exec(`INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required, step_type, ai_system_id)
			VALUES ($1,$2,$3,'linguist',$4,$5)`, profileID, name, order, stepType, engine), "insert step "+name)
	}
	insertStep("AI Review", "hybrid", 0, engine1) // promoted with engine1, linked
	insertStep("AI Review", "hybrid", 1, engine2) // same name, other engine — stays custom
	insertStep("Auto APE", "ai", 2, engine1)      // promoted as ai, linked
	insertStep("Review", "hybrid", 3, engine1)    // name taken by the human def — stays custom

	if err := goose.UpTo(sqldb, dir, 20260908135715); err != nil {
		t.Fatalf("run non-human promotion migration: %v", err)
	}

	assertCount := func(want int, query string, args ...any) {
		t.Helper()
		var got int
		if err := sqldb.QueryRow(query, args...).Scan(&got); err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		if got != want {
			t.Errorf("query %q = %d, want %d", query, got, want)
		}
	}

	// "AI Review" promoted as hybrid with the lower engine; "Auto APE" as ai.
	assertCount(1, `SELECT count(*) FROM step_definitions WHERE tenant_id=$1 AND lower(name)='ai review' AND step_type='hybrid' AND ai_system_id=$2 AND deleted_at IS NULL`, tenantID, engine1)
	assertCount(1, `SELECT count(*) FROM step_definitions WHERE tenant_id=$1 AND lower(name)='auto ape' AND step_type='ai' AND ai_system_id=$2 AND deleted_at IS NULL`, tenantID, engine1)
	// "Review" stays the single human definition — no hybrid twin.
	assertCount(1, `SELECT count(*) FROM step_definitions WHERE tenant_id=$1 AND lower(name)='review'`, tenantID)
	assertCount(1, `SELECT count(*) FROM step_definitions WHERE tenant_id=$1 AND lower(name)='review' AND step_type='human'`, tenantID)

	// Linking: matching identity links; different engine or a taken name does not.
	assertCount(1, `SELECT count(*) FROM workflow_steps WHERE settings_profile_id=$1 AND name='AI Review' AND ai_system_id=$2 AND step_definition_id IS NOT NULL`, profileID, engine1)
	assertCount(1, `SELECT count(*) FROM workflow_steps WHERE settings_profile_id=$1 AND name='AI Review' AND ai_system_id=$2 AND step_definition_id IS NULL`, profileID, engine2)
	assertCount(1, `SELECT count(*) FROM workflow_steps WHERE settings_profile_id=$1 AND name='Auto APE' AND step_definition_id IS NOT NULL`, profileID)
	assertCount(1, `SELECT count(*) FROM workflow_steps WHERE settings_profile_id=$1 AND name='Review' AND step_type='hybrid' AND step_definition_id IS NULL`, profileID)
}

// TestTenantDeleteCascadesWorkflowLibrary guards migration 20260908142131: a
// tenant that owns a library workflow (workflow_definition_steps referencing
// step_definitions) must hard-delete cleanly, which holds only because the
// composition FK cascades.
func TestTenantDeleteCascadesWorkflowLibrary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration data test (-short)")
	}
	sqldb := setupDB(t)
	dir := migrationsDir()
	if err := goose.UpTo(sqldb, dir, goose.MaxVersion); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	scanID := func(q string, args ...any) int64 {
		t.Helper()
		var id int64
		must(sqldb.QueryRow(q, args...).Scan(&id), "scan id: "+q)
		return id
	}

	tenantID := scanID(`INSERT INTO tenants (name, slug) VALUES ('DelCo','delco') RETURNING id`)
	defID := scanID(`INSERT INTO step_definitions (tenant_id, name, step_type, role_required) VALUES ($1,'Translation','human','linguist') RETURNING id`, tenantID)
	wfID := scanID(`INSERT INTO workflows (tenant_id, name) VALUES ($1,'Translate and Review') RETURNING id`, tenantID)
	_, err := sqldb.Exec(`INSERT INTO workflow_definition_steps (workflow_id, step_definition_id, step_order) VALUES ($1,$2,0)`, wfID, defID)
	must(err, "compose workflow")

	// The whole point: the cascade must not be blocked by the composition FK.
	if _, err := sqldb.Exec(`DELETE FROM tenants WHERE id = $1`, tenantID); err != nil {
		t.Fatalf("hard-delete tenant with a library workflow: %v", err)
	}

	assertGone := func(table, col string) {
		t.Helper()
		var n int
		if err := sqldb.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+col+` = $1`, tenantID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s still has %d rows for the deleted tenant", table, n)
		}
	}
	assertGone("step_definitions", "tenant_id")
	assertGone("workflows", "tenant_id")
	var wds int
	if err := sqldb.QueryRow(`SELECT count(*) FROM workflow_definition_steps WHERE workflow_id = $1`, wfID).Scan(&wds); err != nil {
		t.Fatal(err)
	}
	if wds != 0 {
		t.Errorf("workflow_definition_steps not cascaded: %d rows remain", wds)
	}
}

// TestMigrationDropPromptAdditionSourceLanguages covers the destructive step in
// migration 20260924095415: an addition that named only a source language has nothing
// left to constrain once the column goes, so the migration deletes it rather
// than letting it widen to every job. The unseeded Up/Down tests run against an
// empty table and prove neither the DELETE nor the new CHECK.
func TestMigrationDropPromptAdditionSourceLanguages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration data test (-short)")
	}
	sqldb := setupDB(t)
	dir := migrationsDir()

	// Everything up to (but not including) the drop.
	if err := goose.UpTo(sqldb, dir, 20260923120140); err != nil {
		t.Fatalf("up to the migration before the drop: %v", err)
	}

	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	exec := func(q string, args ...any) error { _, err := sqldb.Exec(q, args...); return err }

	var tenantID, promptID int64
	must(sqldb.QueryRow(`INSERT INTO tenants (name, slug) VALUES ('T','t') RETURNING id`).Scan(&tenantID), "insert tenant")
	must(sqldb.QueryRow(
		`INSERT INTO ai_prompts (tenant_id, name, prompt_text, out_status) VALUES ($1,'P','x',true) RETURNING id`,
		tenantID).Scan(&promptID), "insert prompt")

	// Three shapes the old CHECK allowed: target-only, both sides, source-only.
	must(exec(`INSERT INTO ai_prompt_language_additions (prompt_id, source_languages, target_languages, addition_text, position)
	           VALUES ($1, '{}', '{de,de-at}', 'into German', 0)`, promptID), "seed target-only")
	must(exec(`INSERT INTO ai_prompt_language_additions (prompt_id, source_languages, target_languages, addition_text, position)
	           VALUES ($1, '{en}', '{fr}', 'en to fr', 1)`, promptID), "seed pair")
	must(exec(`INSERT INTO ai_prompt_language_additions (prompt_id, source_languages, target_languages, addition_text, position)
	           VALUES ($1, '{de}', '{}', 'from German', 2)`, promptID), "seed source-only")

	if err := goose.UpTo(sqldb, dir, 20260924095415); err != nil {
		t.Fatalf("run the drop migration: %v", err)
	}

	// The source-only row is gone; the other two survive with their targets.
	rows, err := sqldb.Query(`SELECT addition_text, target_languages::text FROM ai_prompt_language_additions ORDER BY position`)
	must(err, "read back")
	defer rows.Close()
	var got []string
	for rows.Next() {
		var text, targets string
		must(rows.Scan(&text, &targets), "scan")
		got = append(got, text+" -> "+targets)
	}
	must(rows.Err(), "rows")

	want := []string{"into German -> {de,de-at}", "en to fr -> {fr}"}
	if len(got) != len(want) {
		t.Fatalf("got %d additions %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("addition %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The new CHECK refuses what the DELETE cleaned out, so the state cannot
	// come back through an ordinary insert.
	if err := exec(`INSERT INTO ai_prompt_language_additions (prompt_id, target_languages, addition_text, position)
	                VALUES ($1, '{}', 'no target', 3)`, promptID); err == nil {
		t.Error("an addition with no target language must be refused by the CHECK")
	}
}

// TestMigrationPromptFieldMeaningsBackfill covers the all-tenant append: each
// custom prompt gets the block its output flags imply, a prompt that already
// names its reply fields is skipped, and built-ins are never touched. The Down
// matches the same literal block, so a wrong block would also fail to reverse.
func TestMigrationPromptFieldMeaningsBackfill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration data test (-short)")
	}
	sqldb := setupDB(t)
	dir := migrationsDir()

	if err := goose.UpTo(sqldb, dir, 20260922085003); err != nil {
		t.Fatalf("up to the migration before the backfill: %v", err)
	}

	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	var tenantID int64
	must(sqldb.QueryRow(`INSERT INTO tenants (name, slug) VALUES ('T','t') RETURNING id`).Scan(&tenantID), "insert tenant")

	// One prompt per mode, plus a hand-written one that already names a field
	// and a built-in — neither may be touched.
	seed := []struct {
		name                              string
		status, text, comments, isDefault bool
		promptText, wantContains          string
	}{
		{name: "qe_ape", status: true, text: true, promptText: "Review.", wantContains: `"postedit"`},
		{name: "qe", status: true, promptText: "Judge.", wantContains: `"approved"`},
		{name: "ape", text: true, comments: true, promptText: "Fix.", wantContains: `"postedit"`},
		{name: "mt", text: true, promptText: "Translate.", wantContains: `"target"`},
		{name: "handwritten", status: true, promptText: `Judge and return "approved".`, wantContains: ""},
		{name: "builtin", status: true, isDefault: true, promptText: "Stock.", wantContains: ""},
	}
	for _, p := range seed {
		must(func() error {
			_, err := sqldb.Exec(
				`INSERT INTO ai_prompts (tenant_id, name, prompt_text, out_status, out_text, out_comments, is_default)
				 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				tenantID, p.name, p.promptText, p.status, p.text, p.comments, p.isDefault)
			return err
		}(), "insert prompt "+p.name)
	}

	if err := goose.UpTo(sqldb, dir, 20260923120140); err != nil {
		t.Fatalf("run the backfill: %v", err)
	}

	read := func(name string) string {
		t.Helper()
		var got string
		must(sqldb.QueryRow(`SELECT prompt_text FROM ai_prompts WHERE tenant_id=$1 AND name=$2`, tenantID, name).Scan(&got), "read "+name)
		return got
	}
	for _, p := range seed {
		got := read(p.name)
		if !strings.HasPrefix(got, p.promptText) {
			t.Errorf("%s: the tenant's own text must survive at the front, got %q", p.name, got)
		}
		switch {
		case p.wantContains == "":
			if got != p.promptText {
				t.Errorf("%s must be left alone, got %q", p.name, got)
			}
		case !strings.Contains(got, p.wantContains):
			t.Errorf("%s must gain the %s meaning, got %q", p.name, p.wantContains, got)
		}
	}
	// The mt prompt names "target" and must NOT have been given the QE wording.
	if got := read("mt"); strings.Contains(got, `"approved"`) {
		t.Errorf("mt got the QE block: %q", got)
	}

	// Down removes exactly what Up appended.
	if err := goose.DownTo(sqldb, dir, 20260922085003); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, p := range seed {
		if got := read(p.name); got != p.promptText {
			t.Errorf("%s not restored by Down:\n got  %q\n want %q", p.name, got, p.promptText)
		}
	}
}

// TestMigrationAutoApprovedChartSegmentType covers the all-tenant chart
// rewrite: only a chart in exactly the default "Auto-approved words by segment
// type" shape moves from kind to segment_type. A customized auto_approved chart
// and another measure grouped the same way are left alone, the rest of the
// definition survives, and Down restores the default shape.
func TestMigrationAutoApprovedChartSegmentType(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration data test (-short)")
	}
	sqldb := setupDB(t)
	dir := migrationsDir()

	if err := goose.UpTo(sqldb, dir, 20261002151630); err != nil {
		t.Fatalf("up to the migration before the rewrite: %v", err)
	}

	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	var tenantID, dashboardID int64
	must(sqldb.QueryRow(`INSERT INTO tenants (name, slug) VALUES ('T','t') RETURNING id`).Scan(&tenantID), "insert tenant")
	must(sqldb.QueryRow(`INSERT INTO dashboards (tenant_id, name) VALUES ($1, 'AI') RETURNING id`, tenantID).Scan(&dashboardID), "insert dashboard")

	const (
		defaultDef = `{"measure":"auto_approved","dimensions":["week","kind"],"last_days":90}`
		defaultViz = `{"type":"bar","x_key":"week","series":["words"],"series_key":"kind"}`
		customDef  = `{"measure":"auto_approved","dimensions":["kind"],"last_days":30}`
		customViz  = `{"type":"bar","x_key":"kind","series":["words"]}`
		otherDef   = `{"measure":"segments_by_origin","dimensions":["week","kind"],"last_days":90}`
		otherViz   = `{"type":"bar","x_key":"week","series":["segments"],"series_key":"kind"}`
	)
	charts := map[string][2]string{
		"default": {defaultDef, defaultViz},
		"custom":  {customDef, customViz},
		"other":   {otherDef, otherViz},
	}
	for title, c := range charts {
		_, err := sqldb.Exec(
			`INSERT INTO dashboard_charts (tenant_id, dashboard_id, title, definition, viz) VALUES ($1, $2, $3, $4, $5)`,
			tenantID, dashboardID, title, c[0], c[1])
		must(err, "insert chart "+title)
	}

	// is reports whether the stored chart equals the given JSON, compared as
	// jsonb so key order does not matter.
	is := func(title, def, viz string) bool {
		t.Helper()
		var same bool
		must(sqldb.QueryRow(
			`SELECT definition = $2::jsonb AND viz = $3::jsonb FROM dashboard_charts WHERE dashboard_id = $1 AND title = $4`,
			dashboardID, def, viz, title).Scan(&same), "read chart "+title)
		return same
	}

	if err := goose.UpTo(sqldb, dir, 20261006143123); err != nil {
		t.Fatalf("run the rewrite: %v", err)
	}
	if !is("default",
		`{"measure":"auto_approved","dimensions":["week","segment_type"],"last_days":90}`,
		`{"type":"bar","x_key":"week","series":["words"],"series_key":"segment_type"}`) {
		t.Error("the default-shape chart must move to segment_type and keep the rest of its definition")
	}
	if !is("custom", customDef, customViz) {
		t.Error("a customized auto_approved chart must be left alone")
	}
	if !is("other", otherDef, otherViz) {
		t.Error("another measure grouped by week and kind must be left alone")
	}

	if err := goose.DownTo(sqldb, dir, 20261002151630); err != nil {
		t.Fatalf("down: %v", err)
	}
	for title, c := range charts {
		if !is(title, c[0], c[1]) {
			t.Errorf("%s not in its original shape after Down", title)
		}
	}
}
