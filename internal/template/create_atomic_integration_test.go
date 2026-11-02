package template_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// A template create is one transaction: an assignment list naming another
// org's TM is refused by the assignment write, after the settings profile is
// inserted, and nothing is left behind. The caller's own TM is the positive
// control.
func TestIntegrationTemplateCreate_RefusedSetupLeavesNothing(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "AtomicCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	ownTM := testutil.SeedTM(t, q, tenant.ID, "Own TM")
	other := testutil.SeedTenant(t, q, "OtherCorp")
	foreignTM := testutil.SeedTM(t, q, other.ID, "Foreign TM")
	standard := testutil.LibraryWorkflow(t, q, tenant.ID)

	create := func(name string, tmID int64) int {
		t.Helper()
		return testutil.DoRequest(t, router, http.MethodPost, "/api/templates", map[string]any{
			"name":           name,
			"workflow_id":    standard,
			"tm_assignments": []map[string]any{{"tm_id": tmID, "mode": "read", "priority": 1}},
		}, user.ID, tenant.ID).Code
	}
	counts := func() (templates, profiles int64) {
		t.Helper()
		templates = testutil.CountRows(t, pool, `SELECT count(*) FROM templates WHERE tenant_id = $1`, tenant.ID)
		profiles = testutil.CountRows(t, pool, `SELECT count(*) FROM settings_profiles WHERE tenant_id = $1`, tenant.ID)
		return templates, profiles
	}

	t0, sp0 := counts()
	if code := create("Foreign", foreignTM.ID); code != http.StatusBadRequest {
		t.Errorf("foreign TM: status = %d, want 400", code)
	}
	if tm, sp := counts(); tm != t0 || sp != sp0 {
		t.Errorf("a refused create left rows behind: templates %d→%d, settings profiles %d→%d", t0, tm, sp0, sp)
	}

	if code := create("Own", ownTM.ID); code != http.StatusCreated {
		t.Fatalf("own TM: status = %d, want 201", code)
	}
	if tm, sp := counts(); tm != t0+1 || sp != sp0+1 {
		t.Errorf("own create: templates %d→%d, settings profiles %d→%d, want one more of each", t0, tm, sp0, sp)
	}
}

// A template edit is one transaction: when its last write fails, the settings
// written before it in the same PATCH are rolled back. A trigger fails the
// template row update; without it the same PATCH saves both.
func TestIntegrationTemplateUpdate_FailedWriteLeavesSettings(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "AtomicUpdateCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "T")
	path := fmt.Sprintf("/api/templates/%d", tmpl.ID)
	inlineHTML := func() bool {
		t.Helper()
		sp, err := q.GetSettingsProfile(ctx, tmpl.SettingsProfileID)
		if err != nil {
			t.Fatal(err)
		}
		return sp.InlineHtml
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}

	before := inlineHTML()
	// The package's tests share one database: the trigger and its function go
	// with the test, also when it fails before dropping the trigger itself.
	// Deferred after the pool's own cleanup, so it runs first.
	defer func() {
		if _, err := pool.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS test_fail_template_update ON templates;
			DROP FUNCTION IF EXISTS test_fail_template_update();`); err != nil {
			t.Errorf("drop the test trigger: %v", err)
		}
	}()
	exec(`CREATE OR REPLACE FUNCTION test_fail_template_update() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'template update fails'; END $$`)
	exec(`CREATE TRIGGER test_fail_template_update BEFORE UPDATE ON templates
		FOR EACH ROW EXECUTE FUNCTION test_fail_template_update()`)
	rec := testutil.DoRequest(t, router, http.MethodPatch, path, map[string]any{"name": "Renamed", "inline_html": !before}, user.ID, tenant.ID)
	if rec.Code < http.StatusInternalServerError {
		t.Errorf("PATCH with the template write failing = %d, want a server error", rec.Code)
	}
	if got := inlineHTML(); got != before {
		t.Errorf("inline_html after the failed PATCH = %v, want still %v", got, before)
	}

	exec(`DROP TRIGGER test_fail_template_update ON templates`)
	testutil.AssertStatus(t, testutil.DoRequest(t, router, http.MethodPatch, path, map[string]any{"name": "Renamed", "inline_html": !before}, user.ID, tenant.ID), http.StatusOK)
	if got := inlineHTML(); got == before {
		t.Errorf("inline_html after the PATCH = %v, want %v", got, !before)
	}
}
