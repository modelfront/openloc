package template_test

import (
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

	create := func(name string, tmID int64) int {
		t.Helper()
		return testutil.DoRequest(t, router, http.MethodPost, "/api/templates", map[string]any{
			"name":           name,
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
