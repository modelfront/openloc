package database_test

import (
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	verSegmentationRuleSets = int64(20261021110103)
	verSegmentationProfiles = int64(20261021181028)
	verProfileLanguages     = int64(20261022104313)
	verDeletedProjects      = int64(20261022123633)
	verTemplateProfiles     = int64(20261022132147)
)

// TestMigrationSegmentationProfiles verifies migration 20261021181028: every
// project and template gets the profile its uploads used until then (the
// org's Default for the language, then for its base language, then the
// built-in for the language, then for its base language, then the
// any-language built-in), and the down migration makes those profiles the
// Defaults again.
func TestMigrationSegmentationProfiles(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verSegmentationRuleSets); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}
	builtin := func(key string) int64 {
		return f.scanID("builtin "+key, `SELECT id FROM segmentation_builtin_rule_sets WHERE builtin_key = $1`, key)
	}
	german, french, anyLang := builtin("builtin:de@1"), builtin("builtin:fr@1"), builtin("builtin:default@1")

	alpha := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	beta := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Beta','beta') RETURNING id`)
	ruleSet := func(tenant int64, name, lang string, isDefault bool) int64 {
		return f.scanID("insert rule set",
			`INSERT INTO segmentation_rule_sets (tenant_id, name, source_language, is_default, definition)
			 VALUES ($1,$2,$3,$4,'{"mode":"sentence","rules":[],"abbreviations":[],"number_abbreviations":[]}') RETURNING id`,
			tenant, name, lang, isDefault)
	}
	alphaFrench := ruleSet(alpha, "Alpha French", "fr", true)
	ruleSet(alpha, "Alpha German, not Default", "de", false)
	deletedDefault := ruleSet(alpha, "Alpha Swahili", "sw", true)
	f.exec("delete rule set", `UPDATE segmentation_rule_sets SET deleted_at = now() WHERE id = $1`, deletedDefault)

	project := func(tenant int64, lang string) int64 {
		profile := f.scanID("insert profile", `INSERT INTO settings_profiles (tenant_id) VALUES ($1) RETURNING id`, tenant)
		f.scanID("insert project",
			`INSERT INTO projects (tenant_id, settings_profile_id, name, source_language, target_languages)
			 VALUES ($1,$2,'P',$3,'{en}') RETURNING id`, tenant, profile, lang)
		return profile
	}
	template := func(tenant int64, lang any) int64 {
		profile := f.scanID("insert profile", `INSERT INTO settings_profiles (tenant_id) VALUES ($1) RETURNING id`, tenant)
		f.scanID("insert template",
			`INSERT INTO templates (tenant_id, settings_profile_id, name, source_language) VALUES ($1,$2,'T',$3) RETURNING id`,
			tenant, profile, lang)
		return profile
	}
	want := map[int64]int64{
		project(alpha, "fr"):    alphaFrench,
		project(alpha, "fr-ca"): alphaFrench,
		project(alpha, "de"):    german,
		project(alpha, "de-at"): german,
		project(alpha, "sw"):    anyLang,
		project(beta, "fr"):     french,
		template(alpha, "fr"):   alphaFrench,
		template(alpha, nil):    anyLang,
	}

	if err := goose.UpTo(sqldb, dir, verSegmentationProfiles); err != nil {
		t.Fatalf("up to segmentation profiles: %v", err)
	}
	for profile, id := range want {
		f.count(1, `SELECT count(*) FROM settings_profiles WHERE id = $1 AND segmentation_profile_id = $2`, profile, id)
	}
	f.count(1, `SELECT count(*) FROM segmentation_profile_catalog WHERE id = $1 AND tenant_id = $2`, alphaFrench, alpha)

	if err := goose.DownTo(sqldb, dir, verSegmentationRuleSets); err != nil {
		t.Fatalf("down: %v", err)
	}
	f.count(1, `SELECT count(*) FROM segmentation_rule_sets WHERE id = $1 AND is_default`, alphaFrench)
	f.count(1, `SELECT count(*) FROM segmentation_rule_sets WHERE is_default`)
	if err := goose.UpTo(sqldb, dir, verSegmentationProfiles); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// TestMigrationSegmentationProfileLanguages verifies migration
// 20261022104313: an org profile's language becomes a one-language list, the
// catalog shows a built-in's language as a list (empty for any language), and
// the down migration keeps a profile's first language, 'und' for an empty
// list.
func TestMigrationSegmentationProfileLanguages(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verSegmentationProfiles); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}
	tenant := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	french := f.scanID("insert profile",
		`INSERT INTO segmentation_profiles (tenant_id, name, source_language, definition)
		 VALUES ($1,'French','fr-ca','{"mode":"sentence","rules":[],"abbreviations":[],"number_abbreviations":[]}') RETURNING id`, tenant)

	if err := goose.UpTo(sqldb, dir, verProfileLanguages); err != nil {
		t.Fatalf("up: %v", err)
	}
	f.count(1, `SELECT count(*) FROM segmentation_profile_catalog WHERE id = $1 AND source_languages = '{fr-ca}'`, french)
	f.count(1, `SELECT count(*) FROM segmentation_profile_catalog WHERE builtin_key = 'builtin:de@1' AND source_languages = '{de}'`)
	f.count(1, `SELECT count(*) FROM segmentation_profile_catalog WHERE builtin_key = 'builtin:default@1' AND source_languages = '{}'`)
	anyLang := f.scanID("insert any-language profile",
		`INSERT INTO segmentation_profiles (tenant_id, name, source_languages, definition)
		 VALUES ($1,'Mixed','{}','{"mode":"sentence","rules":[],"abbreviations":[],"number_abbreviations":[]}') RETURNING id`, tenant)
	f.exec("multi-language profile", `UPDATE segmentation_profiles SET source_languages = '{fr-ca,de}' WHERE id = $1`, french)
	if _, err := sqldb.Exec(`UPDATE segmentation_profiles SET source_languages = '{fr,""}' WHERE id = $1`, french); err == nil {
		t.Error("an empty language code was stored")
	}

	if err := goose.DownTo(sqldb, dir, verSegmentationProfiles); err != nil {
		t.Fatalf("down: %v", err)
	}
	f.count(1, `SELECT count(*) FROM segmentation_profile_catalog WHERE id = $1 AND source_language = 'fr-ca'`, french)
	f.count(1, `SELECT count(*) FROM segmentation_profile_catalog WHERE id = $1 AND source_language = 'und'`, anyLang)
	if err := goose.UpTo(sqldb, dir, verProfileLanguages); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// TestMigrationSegmentationProfileDeletedProjects verifies migration
// 20261022123633: a project with no profile gets the built-in for its
// language, its base language or Default, and a project that names a deleted
// profile loses it.
func TestMigrationSegmentationProfileDeletedProjects(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verDeletedProjects-1); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}
	builtin := func(key string) int64 {
		return f.scanID("builtin "+key, `SELECT id FROM segmentation_builtin_profiles WHERE builtin_key = $1`, key)
	}
	german, anyLang := builtin("builtin:de@1"), builtin("builtin:default@1")
	tenant := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	profile := func(deleted bool) int64 {
		id := f.scanID("insert profile",
			`INSERT INTO segmentation_profiles (tenant_id, name, source_languages, definition)
			 VALUES ($1, 'P' || nextval('segmentation_profile_id_seq'), '{en}', '{"mode":"sentence","rules":[],"abbreviations":[],"number_abbreviations":[]}') RETURNING id`, tenant)
		if deleted {
			f.exec("delete profile", `UPDATE segmentation_profiles SET deleted_at = now() WHERE id = $1`, id)
		}
		return id
	}
	// A project without a language is a draft.
	project := func(lang string, segProfile any) int64 {
		sp := f.scanID("insert settings", `INSERT INTO settings_profiles (tenant_id, segmentation_profile_id) VALUES ($1, $2) RETURNING id`, tenant, segProfile)
		status := "in_progress"
		if lang == "" {
			status = "draft"
		}
		f.scanID("insert project",
			`INSERT INTO projects (tenant_id, settings_profile_id, name, source_language, target_languages, status)
			 VALUES ($1,$2,'P',$3,'{fr}',$4) RETURNING id`, tenant, sp, lang, status)
		return sp
	}
	live := profile(false)
	want := map[int64]any{
		project("de", nil):           german,
		project("de-at", nil):        german,
		project("sw", nil):           anyLang,
		project("", nil):             anyLang,
		project("en", live):          live,
		project("en", profile(true)): nil,
	}

	if err := goose.UpTo(sqldb, dir, verDeletedProjects); err != nil {
		t.Fatalf("up: %v", err)
	}
	for sp, id := range want {
		if id == nil {
			f.count(1, `SELECT count(*) FROM settings_profiles WHERE id = $1 AND segmentation_profile_id IS NULL`, sp)
		} else {
			f.count(1, `SELECT count(*) FROM settings_profiles WHERE id = $1 AND segmentation_profile_id = $2`, sp, id)
		}
	}
	if err := goose.DownTo(sqldb, dir, verDeletedProjects-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := goose.UpTo(sqldb, dir, verDeletedProjects); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// TestMigrationSegmentationProfileTemplates verifies migration
// 20261022132147: a template with no profile gets the built-in for its
// language, its base language or Default; one with a profile keeps it.
func TestMigrationSegmentationProfileTemplates(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verTemplateProfiles-1); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}
	builtin := func(key string) int64 {
		return f.scanID("builtin "+key, `SELECT id FROM segmentation_builtin_profiles WHERE builtin_key = $1`, key)
	}
	german, english, anyLang := builtin("builtin:de@1"), builtin("builtin:en@1"), builtin("builtin:default@1")
	tenant := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	template := func(lang, segProfile any) int64 {
		sp := f.scanID("insert settings", `INSERT INTO settings_profiles (tenant_id, segmentation_profile_id) VALUES ($1, $2) RETURNING id`, tenant, segProfile)
		f.scanID("insert template", `INSERT INTO templates (tenant_id, settings_profile_id, name, source_language) VALUES ($1,$2,'T',$3) RETURNING id`, tenant, sp, lang)
		return sp
	}
	want := map[int64]int64{
		template("de-at", nil):  german,
		template(nil, nil):      anyLang,
		template("sw", nil):     anyLang,
		template("de", english): english,
	}

	if err := goose.UpTo(sqldb, dir, verTemplateProfiles); err != nil {
		t.Fatalf("up: %v", err)
	}
	for sp, id := range want {
		var got *int64
		if err := sqldb.QueryRow(`SELECT segmentation_profile_id FROM settings_profiles WHERE id = $1`, sp).Scan(&got); err != nil || got == nil || *got != id {
			t.Errorf("settings %d profile = %v (%v), want %d", sp, got, err, id)
		}
	}
	if err := goose.DownTo(sqldb, dir, verTemplateProfiles-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := goose.UpTo(sqldb, dir, verTemplateProfiles); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
