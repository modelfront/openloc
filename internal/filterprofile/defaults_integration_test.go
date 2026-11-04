package filterprofile_test

import (
	"errors"
	"testing"

	"openloc/internal/filterprofile"
	"openloc/internal/httputil"
	"openloc/internal/testutil"
)

// The org default per format: only a built-in or a live profile of the
// tenant, of the format, is accepted; it belongs to one tenant, so a shared
// built-in is marked only for the tenant that chose it; and it cannot be
// deleted until another default is set.
func TestIntegrationFilterOrgDefaults(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := testutil.Ctx()
	tenant := testutil.SeedTenant(t, q, "FilterOrgDefaults")
	other := testutil.SeedTenant(t, q, "FilterOrgDefaultsOther")
	svc := filterprofile.NewService(q, pool)
	plainPO := testutil.BuiltinFilterProfile(t, q, "builtin:po-plain@1")
	mine, err := svc.Create(ctx, tenant.ID, 0, filterprofile.Input{Name: "Mine", Format: "json_i18n", Options: &filterprofile.Options{}})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := svc.Create(ctx, other.ID, 0, filterprofile.Input{Name: "Theirs", Format: "json_i18n", Options: &filterprofile.Options{}})
	if err != nil {
		t.Fatal(err)
	}

	refused := func(t *testing.T, format string, id int64) {
		t.Helper()
		err := filterprofile.SetOrgDefault(ctx, q, tenant.ID, 0, format, &id)
		var ce *httputil.ClientError
		if !errors.As(err, &ce) {
			t.Errorf("SetOrgDefault(%s, %d) = %v, want a client error", format, id, err)
		}
	}
	refused(t, "json_i18n", theirs.ID) // another tenant's
	refused(t, "xlsx", mine.ID)        // another format
	refused(t, "docx", mine.ID)        // no filter profiles

	for format, id := range map[string]int64{"json_i18n": mine.ID, "po": plainPO} {
		if err := filterprofile.SetOrgDefault(ctx, q, tenant.ID, 0, format, &id); err != nil {
			t.Fatalf("SetOrgDefault(%s) = %v", format, err)
		}
	}
	got, err := filterprofile.OrgDefaults(ctx, q, tenant.ID)
	if err != nil || len(got) != 2 {
		t.Fatalf("OrgDefaults = %+v, %v", got, err)
	}
	if theirs, err := filterprofile.OrgDefaults(ctx, q, other.ID); err != nil || len(theirs) != 0 {
		t.Errorf("the other tenant's defaults = %+v, %v; want none", theirs, err)
	}

	marked := func(t *testing.T, tenantID int64) map[int64]bool {
		t.Helper()
		list, err := svc.List(ctx, tenantID, "")
		if err != nil {
			t.Fatal(err)
		}
		out := map[int64]bool{}
		for _, p := range list {
			if p.OrgDefault {
				out[p.ID] = true
			}
		}
		return out
	}
	if m := marked(t, tenant.ID); len(m) != 2 || !m[mine.ID] || !m[plainPO] {
		t.Errorf("marked for the tenant = %v, want %d and %d", m, mine.ID, plainPO)
	}
	if m := marked(t, other.ID); len(m) != 0 {
		t.Errorf("marked for the other tenant = %v, want none", m)
	}

	// A nil pick stays the built-in Default; a left-out format takes the default.
	choices, err := filterprofile.WithOrgDefaults(ctx, q, tenant.ID, map[string]*int64{"json_i18n": nil})
	if err != nil {
		t.Fatal(err)
	}
	if choices["json_i18n"] != nil || choices["po"] == nil || *choices["po"] != plainPO {
		t.Errorf("WithOrgDefaults = %v", choices)
	}

	err = svc.Delete(ctx, tenant.ID, mine.ID)
	var conflict *httputil.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("deleting the default = %v, want a conflict", err)
	}
	if err := filterprofile.SetOrgDefault(ctx, q, tenant.ID, 0, "json_i18n", nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, tenant.ID, mine.ID); err != nil {
		t.Errorf("deleting after clearing the default = %v", err)
	}
}
