// Customer CRUD + tenant isolation + role gating.
// Mirrors the invite/integration_test.go pattern (admin-gated CRUD with
// cross-tenant attack coverage).
package customer_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/testutil"
)

func TestIntegrationCustomerCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")
	admin := testutil.SeedUserWithRole(t, q, tenant.ID, "admin@test.com", "pass", "admin")
	member := testutil.SeedUserWithRole(t, q, tenant.ID, "member@test.com", "pass", "linguist")

	// Owner creates customer
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{
		"name": "Acme Corp",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var createResp struct {
		Data struct {
			ID       int64  `json:"id"`
			Name     string `json:"name"`
			TenantID int64  `json:"tenant_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &createResp)
	if createResp.Data.Name != "Acme Corp" {
		t.Errorf("expected name Acme Corp, got %q", createResp.Data.Name)
	}
	if createResp.Data.TenantID != tenant.ID {
		t.Errorf("customer leaked into wrong tenant: got %d, want %d", createResp.Data.TenantID, tenant.ID)
	}
	customerID := createResp.Data.ID

	// Admin can also create (admin or owner allowed)
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{
		"name": "Beta Inc",
	}, admin.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	// Member CANNOT create — admin-gated
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{
		"name": "Forbidden Co",
	}, member.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Linguist CANNOT list — customers are a manager-only surface.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/customers", nil, member.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Admin lists — sees both customers.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/customers", nil, admin.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listResp struct {
		Data []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 2 {
		t.Errorf("expected 2 customers (Acme, Beta), got %d", len(listResp.Data))
	}

	// Owner gets by ID
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/customers/%d", customerID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Empty name → 400
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{
		"name": "   ",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Length cap → 400
	tooLong := strings.Repeat("x", 201)
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{
		"name": tooLong,
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Duplicate name (case-insensitive) → 409
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{
		"name": "ACME corp", // different case, same name
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// Update (PATCH) — rename
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/customers/%d", customerID), map[string]string{
		"name": "Acme Corporation",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var renamed struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &renamed)
	if renamed.Data.Name != "Acme Corporation" {
		t.Errorf("expected renamed customer Acme Corporation, got %q", renamed.Data.Name)
	}

	// Member CANNOT update
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/customers/%d", customerID), map[string]string{
		"name": "Hacked Inc",
	}, member.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Project count for the customer — manager-only surface.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/projects", map[string]any{
		"name": "Acme launch", "source_language": "en", "target_languages": []string{"de"},
		"customer_id": customerID,
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/customers/%d/project-count", customerID), nil, admin.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var countResp struct {
		Data struct {
			ProjectCount int64 `json:"project_count"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &countResp)
	if countResp.Data.ProjectCount != 1 {
		t.Errorf("expected 1 project for the customer, got %d", countResp.Data.ProjectCount)
	}

	// Soft delete
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/customers/%d", customerID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// After delete, GET returns 404 (soft-deleted is filtered)
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/customers/%d", customerID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// LIST after delete excludes the soft-deleted row
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/customers", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 1 {
		t.Errorf("expected 1 customer after soft-delete, got %d", len(listResp.Data))
	}

	// Re-create with the same name should now succeed (partial unique index on
	// `WHERE deleted_at IS NULL` allows it)
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{
		"name": "Acme Corporation",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
}

// Cross-tenant isolation: a customer created in tenant A must not be visible
// or mutable from tenant B. Critical security test.
func TestIntegrationCustomerCrossTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	tenantB := testutil.SeedTenant(t, q, "TenantB")
	ownerA := testutil.SeedUserWithRole(t, q, tenantA.ID, "ownerA@test.com", "pass", "owner")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "ownerB@test.com", "pass", "owner")

	// Owner A creates a customer in tenant A
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{
		"name": "Tenant A's Customer",
	}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	customerAID := resp.Data.ID

	// Owner B cannot GET A's customer
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/customers/%d", customerAID), nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// Owner B cannot UPDATE A's customer
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/customers/%d", customerAID), map[string]string{
		"name": "Hijacked",
	}, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// Owner B's DELETE for A's customer ID returns 204 (silent no-op via the
	// `WHERE tenant_id = $2` predicate). This is a deliberate design choice:
	// not 404 (would confirm existence), not 403 (would confirm pattern). The
	// 204 is indistinguishable from deleting a nonexistent ID — no information
	// leak. Pinning the assertion here so a future regression to 500 is caught.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/customers/%d", customerAID), nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	// Confirm A's customer is still alive by re-fetching as owner A.
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/customers/%d", customerAID), nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK) // still alive in tenant A

	// Owner B's list does NOT include A's customer
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/customers", nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listResp struct {
		Data []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 0 {
		t.Errorf("tenant B should see 0 customers, got %d", len(listResp.Data))
	}

	// Same name in two tenants is OK
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{
		"name": "Tenant A's Customer", // duplicate name across tenants
	}, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
}
