package auth_test

import (
	"context"
	"net/http"
	"testing"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationVendorClientInactiveMe: while a vendor org's client is not
// approved, /me reports the vendor org as blocked, so the app shows the
// blocked screen instead of pages that all answer 403.
func TestIntegrationVendorClientInactiveMe(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	client := testutil.SeedTenant(t, q, "Client Corp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	ling := testutil.SeedUserWithRole(t, q, vendor.ID, "lina@acme.test", "pw", auth.RoleLinguist)

	me := func() map[string]any {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, http.MethodGet, "/api/auth/me", nil, ling.ID, vendor.ID, auth.RoleLinguist)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var body struct {
			Data map[string]any `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &body)
		return body.Data
	}

	if m := me(); m["org_pending"] != false {
		t.Fatalf("active client: /me = %v, want org_pending false", m)
	}
	if _, err := q.SetTenantStatus(ctx, db.SetTenantStatusParams{ID: client.ID, Status: "suspended"}); err != nil {
		t.Fatalf("suspend client: %v", err)
	}
	if m := me(); m["org_pending"] != true || m["tenant_status"] != "client_inactive" {
		t.Errorf("suspended client: /me = %v, want org_pending true and tenant_status client_inactive", m)
	}
}
