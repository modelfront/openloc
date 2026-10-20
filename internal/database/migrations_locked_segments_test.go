package database_test

import (
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	verBeforeApproveLocked = int64(20261014133951)
	verApproveLocked       = int64(20261015125238)
)

// TestMigrationApproveLockedSegments verifies migration 20261015125238: every
// locked segment with text ends up Approved or Auto-approved, a locked empty
// segment is unlocked, and unlocked segments are untouched.
func TestMigrationApproveLockedSegments(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verBeforeApproveLocked); err != nil {
		t.Fatalf("up to pre-migration: %v", err)
	}

	tenant := f.scanID("insert tenant", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	profile := f.scanID("insert profile", `INSERT INTO settings_profiles (tenant_id) VALUES ($1) RETURNING id`, tenant)
	project := f.scanID("insert project",
		`INSERT INTO projects (tenant_id, settings_profile_id, name, source_language, target_languages)
		 VALUES ($1,$2,'P','en','{de}') RETURNING id`, tenant, profile)
	file := f.scanID("insert file",
		`INSERT INTO files (project_id, name, format, storage_path) VALUES ($1,'f.txt','txt','x') RETURNING id`, project)
	pos := 0
	row := func(status, via string, text any, locked bool) int64 {
		pos++
		seg := f.scanID("insert segment",
			`INSERT INTO segments (file_id, source_text, position) VALUES ($1,'s',$2) RETURNING id`, file, pos)
		return f.scanID("insert translation",
			`INSERT INTO translations (segment_id, target_language, status, confirmed_via, active_text, locked)
			 VALUES ($1,'de',$2,$3,$4,$5) RETURNING id`, seg, status, via, text, locked)
	}
	lockedDraft := row("needs_review", "user", "Entwurf", true)
	lockedAssistant := row("approved", "assistant", "Assistent", true)
	lockedAuto := row("auto_approved", "user", "Auto", true)
	lockedEmpty := row("needs_review", "user", nil, true)
	lockedBlank := row("needs_review", "user", "", true)
	openDraft := row("needs_review", "user", "Offen", false)

	if err := goose.UpTo(sqldb, dir, verApproveLocked); err != nil {
		t.Fatalf("up to approve-locked migration: %v", err)
	}

	want := func(id int64, status, via string, locked bool) {
		t.Helper()
		f.count(1, `SELECT count(*) FROM translations
			WHERE id = $1 AND status = $2 AND confirmed_via = $3 AND locked = $4`, id, status, via, locked)
	}
	want(lockedDraft, "approved", "user", true)
	want(lockedAssistant, "approved", "assistant", true)
	want(lockedAuto, "auto_approved", "user", true)
	want(lockedEmpty, "needs_review", "user", false)
	want(lockedBlank, "needs_review", "user", false)
	want(openDraft, "needs_review", "user", false)
}
