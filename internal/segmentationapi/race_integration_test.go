package segmentationapi_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/segmentationapi"
	"openloc/internal/testutil"
)

// segmentationOf reads the profile a settings row names.
func segmentationOf(t *testing.T, q db.Querier, settingsID int64) *int64 {
	t.Helper()
	sp, err := q.GetSettingsProfile(context.Background(), settingsID)
	if err != nil {
		t.Fatal(err)
	}
	return sp.SegmentationProfileID
}

// A template that picks a profile while it is being deleted stops the
// delete: the template's save holds the profile row, the delete waits for it
// and then counts the template, so no template is left on a deleted profile.
func TestIntegrationProfiles_DeleteRacingATemplateSave(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegRaceTemplate")
	tpl := testutil.SeedTemplate(t, q, tenant.ID, "T")
	set := create(t, svc, tenant.ID, "Legal", "en")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if _, err := db.New(tx).SetSettingsProfileSegmentationProfile(ctx, db.SetSettingsProfileSegmentationProfileParams{ID: tpl.SettingsProfileID, SegmentationProfileID: &set.ID}); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() { deleted <- svc.Delete(ctx, tenant.ID, set.ID) }()
	testutil.WaitFor(t, func() bool { return testutil.LockWaiters(t, pool) == 1 }, "the delete to wait on the template's save")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if err := <-deleted; err == nil {
		t.Fatal("deleted a profile a template picked during the delete")
	} else if _, ok := httputil.AsConflictError(err); !ok {
		t.Fatalf("delete: err = %v, want a conflict", err)
	}
	if _, err := svc.Get(ctx, tenant.ID, set.ID); err != nil {
		t.Errorf("the refused delete still removed the profile: %v", err)
	}
	if got := segmentationOf(t, q, tpl.SettingsProfileID); got == nil || *got != set.ID {
		t.Errorf("template profile = %s, want %d", testutil.Show(got), set.ID)
	}
}

// A project save that names a profile while it is being deleted waits for
// the delete and then stores nothing: it never names a deleted profile.
func TestIntegrationProfiles_SaveRacingADelete(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegRaceSave")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"fr"})
	before := segmentationOf(t, q, project.SettingsProfileID)
	set := create(t, svc, tenant.ID, "Legal", "en")

	// The delete's own statements, in a transaction the test holds open.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	txq := db.New(tx)
	if n, err := txq.SoftDeleteSegmentationProfile(ctx, db.SoftDeleteSegmentationProfileParams{ID: set.ID, TenantID: tenant.ID}); err != nil || n != 1 {
		t.Fatalf("soft delete: %d rows, %v", n, err)
	}
	if _, err := txq.ClearSegmentationProfileFromProjects(ctx, db.ClearSegmentationProfileFromProjectsParams{TenantID: tenant.ID, ID: set.ID}); err != nil {
		t.Fatal(err)
	}
	saved := make(chan error, 1)
	go func() {
		_, err := q.SetSettingsProfileSegmentationProfile(ctx, db.SetSettingsProfileSegmentationProfileParams{ID: project.SettingsProfileID, SegmentationProfileID: &set.ID})
		saved <- err
	}()
	testutil.WaitFor(t, func() bool { return len(saved) == 1 || testutil.LockWaiters(t, pool) == 1 }, "the save to finish or wait on the delete")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if err := <-saved; !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("save racing a delete: err = %v, want no row", err)
	}
	if got := segmentationOf(t, q, project.SettingsProfileID); got == nil || before == nil || *got != *before {
		t.Errorf("project profile = %s, want the one it had (%s)", testutil.Show(got), testutil.Show(before))
	}
}

// A project that picks a profile while it is being deleted loses it: the
// delete waits for the project's save and then clears it, so no project is
// left on a deleted profile.
func TestIntegrationProfiles_DeleteRacingAProjectSave(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegRaceProject")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"fr"})
	set := create(t, svc, tenant.ID, "Legal", "en")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if _, err := db.New(tx).SetSettingsProfileSegmentationProfile(ctx, db.SetSettingsProfileSegmentationProfileParams{ID: project.SettingsProfileID, SegmentationProfileID: &set.ID}); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() { deleted <- svc.Delete(ctx, tenant.ID, set.ID) }()
	testutil.WaitFor(t, func() bool { return testutil.LockWaiters(t, pool) == 1 }, "the delete to wait on the project's save")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if err := <-deleted; err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := segmentationOf(t, q, project.SettingsProfileID); got != nil {
		t.Errorf("project profile = %d after the delete, want none", *got)
	}
}
