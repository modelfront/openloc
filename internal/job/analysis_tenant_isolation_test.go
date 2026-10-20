package job_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Tenant-isolation tests for V1 + V2 project analysis. The analysis SQL
// (`GetProjectAnalysis`, `GetProjectAnalysisByFile`, `GetProjectRepetitions`)
// filters by project_id only, NOT tenant_id, so the `GetProject(id, tenantID)`
// check is the tenant boundary: V1 does it inline at the top of the handler,
// V2 in the shared `computeProjectAnalysis` helper. These tests pin that a
// cross-tenant request is a 404 (the CSV cross-tenant test covers V2's
// helper transitively, but NOT V1's inline check).
//
// Anti-tautology RED proof:
//   Mutation:   Remove the GetProject check from HandleProjectAnalysis
//               (V1) → V1 test fails with 200 + leaked body.
//   Mutation:   Remove the GetProject check from computeProjectAnalysis
//               (V2) → V2 test fails the same way.
//   Both proven 2026-05-07.

// TestIntegrationProjectAnalysisV1_CrossTenantReturns404 — tenant B requests tenant A's analysis → 404.
func TestIntegrationProjectAnalysisV1_CrossTenantReturns404(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "TenantA-V1")
	userA := testutil.SeedUser(t, q, tenantA.ID, "av1@test.com", "pass")
	projectA := testutil.SeedProject(t, q, tenantA.ID, "Project A V1", "en", []string{"es"})
	fileA := testutil.SeedFile(t, q, projectA.ID, "secret-v1.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, fileA.ID, "es")
	_ = userA

	tenantB := testutil.SeedTenant(t, q, "TenantB-V1")
	userB := testutil.SeedUser(t, q, tenantB.ID, "bv1@test.com", "pass")

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/analysis", projectA.ID),
		nil, userB.ID, tenantB.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// Belt-and-suspenders: even if a regression returns 200, body must
	// not contain tenant A's secret data. Pins the leak shape directly.
	if strings.Contains(rec.Body.String(), "secret-v1") {
		t.Errorf("V1 analysis leaked tenant A data across tenant boundary")
	}
}

// TestIntegrationProjectAnalysisV2_CrossTenantReturns404 — tenant B requests tenant A's V2 analysis → 404.
//
// Note: the CSV cross-tenant test transitively covers
// computeProjectAnalysis's tenant check (CSV calls the same helper),
// so a regression there would be caught twice. The duplication is
// deliberate — V2 is the production surface most FE callers hit, and
// it deserves its own pin.
func TestIntegrationProjectAnalysisV2_CrossTenantReturns404(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "TenantA-V2")
	userA := testutil.SeedUser(t, q, tenantA.ID, "av2@test.com", "pass")
	projectA := testutil.SeedProject(t, q, tenantA.ID, "Project A V2", "en", []string{"es"})
	fileA := testutil.SeedFile(t, q, projectA.ID, "secret-v2.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, fileA.ID, "es")
	_ = userA

	tenantB := testutil.SeedTenant(t, q, "TenantB-V2")
	userB := testutil.SeedUser(t, q, tenantB.ID, "bv2@test.com", "pass")

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/analysis/v2", projectA.ID),
		nil, userB.ID, tenantB.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	if strings.Contains(rec.Body.String(), "secret-v2") {
		t.Errorf("V2 analysis leaked tenant A data across tenant boundary")
	}
}
