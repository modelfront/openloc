package database_test

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
)

// Goose versions bracketing the two router migrations.
const (
	verRenameAIEngines = int64(20260916092110) // last version before ai_routers
	verAIRouters       = int64(20260916100703)
	verMTRouters       = int64(20260916205135)
)

// routerFixture bundles the tiny query helpers the two backfill tests share.
type routerFixture struct {
	t  *testing.T
	db *sql.DB
}

func (f *routerFixture) exec(what, query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		f.t.Fatalf("%s: %v", what, err)
	}
}

func (f *routerFixture) scanID(what, query string, args ...any) int64 {
	f.t.Helper()
	var id int64
	if err := f.db.QueryRow(query, args...).Scan(&id); err != nil {
		f.t.Fatalf("%s: %v", what, err)
	}
	return id
}

func (f *routerFixture) count(want int, query string, args ...any) {
	f.t.Helper()
	var got int
	if err := f.db.QueryRow(query, args...).Scan(&got); err != nil {
		f.t.Fatalf("query %q: %v", query, err)
	}
	if got != want {
		f.t.Errorf("query %q = %d, want %d", query, got, want)
	}
}

// nullID reads a nullable bigint column.
func (f *routerFixture) nullID(query string, args ...any) (int64, bool) {
	f.t.Helper()
	var v sql.NullInt64
	if err := f.db.QueryRow(query, args...).Scan(&v); err != nil {
		f.t.Fatalf("query %q: %v", query, err)
	}
	return v.Int64, v.Valid
}

// TestMigrationMTRoutersBackfill verifies migration 20260916205135: every
// settings profile that had settings_mt_assignments rows gets its own MT
// router. The (*, *) wildcard row becomes the router's default engine, every
// other row a per-target-language rule (classic MT engine or AI engine), the
// router is named after the owning project, and two profiles whose projects
// share a name inside one tenant get "… MT" and "… MT (2)". A profile with no
// assignments gets no router, and the name collision does not cross tenants.
func TestMigrationMTRoutersBackfill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration data test (-short)")
	}
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	// Everything up to (but not including) the MT-router migration, so the
	// pre-migration settings_mt_assignments table still exists.
	if err := goose.UpTo(sqldb, dir, verAIRouters); err != nil {
		t.Fatalf("up to ai-router migration: %v", err)
	}

	newProfile := func(tenantID int64) int64 {
		return f.scanID("insert profile",
			`INSERT INTO settings_profiles (tenant_id) VALUES ($1) RETURNING id`, tenantID)
	}
	newProject := func(tenantID, profileID int64, name string) {
		f.exec("insert project "+name,
			`INSERT INTO projects (tenant_id, settings_profile_id, name, source_language, target_languages)
			 VALUES ($1,$2,$3,'en','{de,fr}')`, tenantID, profileID, name)
	}
	assign := func(profileID int64, src, tgt string, mtEngine, aiEngine any) {
		f.exec("insert mt assignment",
			`INSERT INTO settings_mt_assignments (settings_profile_id, mt_engine_id, ai_engine_id, source_language, target_language)
			 VALUES ($1,$2,$3,$4,$5)`, profileID, mtEngine, aiEngine, src, tgt)
	}

	tenantA := f.scanID("insert tenant A", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	tenantB := f.scanID("insert tenant B", `INSERT INTO tenants (name, slug) VALUES ('Beta','beta') RETURNING id`)

	mtA := f.scanID("insert mt engine A",
		`INSERT INTO mt_engines (tenant_id, name, engine_type) VALUES ($1,'Classic MT','libretranslate') RETURNING id`, tenantA)
	aiA := f.scanID("insert ai engine A",
		`INSERT INTO ai_engines (tenant_id, name, purpose) VALUES ($1,'LLM MT','mt') RETURNING id`, tenantA)
	mtB := f.scanID("insert mt engine B",
		`INSERT INTO mt_engines (tenant_id, name, engine_type) VALUES ($1,'Classic MT','libretranslate') RETURNING id`, tenantB)

	// Tenant A, profile 1: wildcard default + two per-target rows, one of them
	// pointing at an AI engine instead of a classic MT engine.
	profile1 := newProfile(tenantA)
	newProject(tenantA, profile1, "Handbook")
	assign(profile1, "*", "*", mtA, nil)
	assign(profile1, "en", "de", mtA, nil)
	assign(profile1, "en", "fr", nil, aiA)

	// Tenant A, profile 2: same project name → the " (2)" suffix must fire.
	// No wildcard row, so the router carries no default engine.
	profile2 := newProfile(tenantA)
	newProject(tenantA, profile2, "Handbook")
	assign(profile2, "en", "de", nil, aiA)

	// Tenant A, profile 3: no assignments at all → no router.
	profile3 := newProfile(tenantA)
	newProject(tenantA, profile3, "Untouched")

	// Tenant B: same project name again — the collision counter partitions by
	// tenant, so this one is "Handbook MT", not "Handbook MT (3)".
	profileB := newProfile(tenantB)
	newProject(tenantB, profileB, "Handbook")
	assign(profileB, "*", "*", mtB, nil)

	if err := goose.UpTo(sqldb, dir, verMTRouters); err != nil {
		t.Fatalf("run mt-router migration: %v", err)
	}

	// One router per profile that had assignments — three, not four.
	f.count(3, `SELECT count(*) FROM mt_routers`)

	routerOf := func(profileID int64) int64 {
		t.Helper()
		id, ok := f.nullID(`SELECT mt_router_id FROM settings_profiles WHERE id = $1`, profileID)
		if !ok {
			t.Fatalf("profile %d has no mt_router_id", profileID)
		}
		return id
	}

	// Profile 1: named after its project, wildcard row became the default.
	router1 := routerOf(profile1)
	f.count(1, `SELECT count(*) FROM mt_routers
		WHERE id=$1 AND tenant_id=$2 AND name='Handbook MT'
		  AND default_mt_engine_id=$3 AND default_ai_engine_id IS NULL`, router1, tenantA, mtA)
	// Its two non-wildcard rows became rules, each keeping its engine kind.
	f.count(2, `SELECT count(*) FROM mt_router_rules WHERE router_id=$1`, router1)
	f.count(1, `SELECT count(*) FROM mt_router_rules
		WHERE router_id=$1 AND target_lang='de' AND mt_engine_id=$2 AND ai_engine_id IS NULL`, router1, mtA)
	f.count(1, `SELECT count(*) FROM mt_router_rules
		WHERE router_id=$1 AND target_lang='fr' AND ai_engine_id=$2 AND mt_engine_id IS NULL`, router1, aiA)

	// Profile 2: suffixed name, no default, one AI rule.
	router2 := routerOf(profile2)
	if router2 == router1 {
		t.Fatalf("profiles 1 and 2 share router %d — each profile needs its own", router1)
	}
	f.count(1, `SELECT count(*) FROM mt_routers
		WHERE id=$1 AND tenant_id=$2 AND name='Handbook MT (2)'
		  AND default_mt_engine_id IS NULL AND default_ai_engine_id IS NULL`, router2, tenantA)
	f.count(1, `SELECT count(*) FROM mt_router_rules WHERE router_id=$1`, router2)
	f.count(1, `SELECT count(*) FROM mt_router_rules
		WHERE router_id=$1 AND target_lang='de' AND ai_engine_id=$2`, router2, aiA)

	// Profile 3 had nothing to migrate.
	if id, ok := f.nullID(`SELECT mt_router_id FROM settings_profiles WHERE id=$1`, profile3); ok {
		t.Errorf("profile without assignments got router %d, want NULL", id)
	}

	// Tenant B keeps the unsuffixed name and its own engine.
	routerB := routerOf(profileB)
	f.count(1, `SELECT count(*) FROM mt_routers
		WHERE id=$1 AND tenant_id=$2 AND name='Handbook MT' AND default_mt_engine_id=$3`, routerB, tenantB, mtB)
	f.count(0, `SELECT count(*) FROM mt_router_rules WHERE router_id=$1`, routerB)

	// The old table is gone.
	var exists bool
	if err := sqldb.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name='settings_mt_assignments')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("settings_mt_assignments should be dropped by the mt-router migration")
	}
}

// TestMigrationAIRoutersBackfill verifies migration 20260916100703: every AI
// engine referenced by a library step definition or a profile step snapshot
// gets one router named after the engine, and both step tables are re-pointed
// at the router wrapping the engine they started on. Two engines sharing a
// name inside a tenant produce "Name" and "Name (2)"; the same name in another
// tenant is unsuffixed, and a human definition keeps a NULL router.
func TestMigrationAIRoutersBackfill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration data test (-short)")
	}
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	// Everything up to (but not including) the AI-router migration, so the
	// pre-migration ai_engine_id columns still exist.
	if err := goose.UpTo(sqldb, dir, verRenameAIEngines); err != nil {
		t.Fatalf("up to ai-engine rename migration: %v", err)
	}

	newEngine := func(tenantID int64, name string) int64 {
		return f.scanID("insert ai engine "+name,
			`INSERT INTO ai_engines (tenant_id, name) VALUES ($1,$2) RETURNING id`, tenantID, name)
	}
	newDef := func(tenantID int64, name, stepType string, engine any) int64 {
		return f.scanID("insert step definition "+name,
			`INSERT INTO step_definitions (tenant_id, name, step_type, role_required, ai_engine_id)
			 VALUES ($1,$2,$3,'linguist',$4) RETURNING id`, tenantID, name, stepType, engine)
	}
	newStep := func(profileID int64, name, stepType string, order int, engine int64) int64 {
		return f.scanID("insert workflow step "+name,
			`INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required, step_type, ai_engine_id)
			 VALUES ($1,$2,$3,'linguist',$4,$5) RETURNING id`, profileID, name, order, stepType, engine)
	}

	tenantA := f.scanID("insert tenant A", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	tenantB := f.scanID("insert tenant B", `INSERT INTO tenants (name, slug) VALUES ('Beta','beta') RETURNING id`)
	profileA := f.scanID("insert profile A", `INSERT INTO settings_profiles (tenant_id) VALUES ($1) RETURNING id`, tenantA)
	profileB := f.scanID("insert profile B", `INSERT INTO settings_profiles (tenant_id) VALUES ($1) RETURNING id`, tenantB)

	// Two engines with the SAME name in one tenant, plus a same-named engine
	// in a second tenant, plus one engine nothing points at.
	engineA1 := newEngine(tenantA, "Nova")
	engineA2 := newEngine(tenantA, "Nova")
	engineB := newEngine(tenantB, "Nova")
	newEngine(tenantA, "Unused")

	defA1 := newDef(tenantA, "Library APE", "ai", engineA1)
	defA2 := newDef(tenantA, "Library QA", "hybrid", engineA2)
	defHuman := newDef(tenantA, "Library Review", "human", nil)
	defB := newDef(tenantB, "Library APE", "ai", engineB)

	stepA1 := newStep(profileA, "Snapshot APE", "ai", 0, engineA1)
	stepA2 := newStep(profileA, "Snapshot QA", "hybrid", 1, engineA2)
	stepB := newStep(profileB, "Snapshot APE", "ai", 0, engineB)

	if err := goose.UpTo(sqldb, dir, verAIRouters); err != nil {
		t.Fatalf("run ai-router migration: %v", err)
	}

	// One router per REFERENCED engine — "Unused" gets none.
	f.count(3, `SELECT count(*) FROM ai_routers`)
	f.count(0, `SELECT count(*) FROM ai_routers WHERE name LIKE 'Unused%'`)

	// Names: lower engine id keeps the bare name, the twin gets " (2)", and
	// tenant B's same-named engine is unsuffixed.
	f.count(1, `SELECT count(*) FROM ai_routers WHERE tenant_id=$1 AND name='Nova' AND default_engine_id=$2`, tenantA, engineA1)
	f.count(1, `SELECT count(*) FROM ai_routers WHERE tenant_id=$1 AND name='Nova (2)' AND default_engine_id=$2`, tenantA, engineA2)
	f.count(1, `SELECT count(*) FROM ai_routers WHERE tenant_id=$1 AND name='Nova' AND default_engine_id=$2`, tenantB, engineB)

	// Every step resolves through its router back to the engine it started on,
	// and the router belongs to the step's own tenant.
	assertDef := func(defID, wantEngine, wantTenant int64) {
		t.Helper()
		f.count(1, `SELECT count(*) FROM step_definitions sd
			JOIN ai_routers r ON r.id = sd.ai_router_id
			WHERE sd.id=$1 AND r.default_engine_id=$2 AND r.tenant_id=$3`, defID, wantEngine, wantTenant)
	}
	assertStep := func(stepID, wantEngine, wantTenant int64) {
		t.Helper()
		f.count(1, `SELECT count(*) FROM workflow_steps ws
			JOIN ai_routers r ON r.id = ws.ai_router_id
			JOIN settings_profiles sp ON sp.id = ws.settings_profile_id
			WHERE ws.id=$1 AND r.default_engine_id=$2 AND r.tenant_id=$3 AND sp.tenant_id=r.tenant_id`,
			stepID, wantEngine, wantTenant)
	}

	assertDef(defA1, engineA1, tenantA)
	assertDef(defA2, engineA2, tenantA)
	assertDef(defB, engineB, tenantB)
	assertStep(stepA1, engineA1, tenantA)
	assertStep(stepA2, engineA2, tenantA)
	assertStep(stepB, engineB, tenantB)

	// The two "Nova" routers must not be the same row, or the twin engines
	// collapsed into one.
	f.count(2, `SELECT count(DISTINCT ai_router_id) FROM step_definitions WHERE id IN ($1,$2)`, defA1, defA2)

	// A human definition carries no router (and the new CHECK enforces it).
	if id, ok := f.nullID(`SELECT ai_router_id FROM step_definitions WHERE id=$1`, defHuman); ok {
		t.Errorf("human step definition got router %d, want NULL", id)
	}

	// The engine columns are gone from both step tables.
	for _, table := range []string{"step_definitions", "workflow_steps"} {
		var exists bool
		if err := sqldb.QueryRow(
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name='ai_engine_id')`,
			table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("%s.ai_engine_id should be dropped by the ai-router migration", table)
		}
	}
}
