package auth_test

import (
	"slices"
	"testing"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// A freshly-provisioned org gets the default step definitions and a "Default"
// workflow composed of them, so its Workflows and Steps pages are never empty.
// The seeder is idempotent.
func TestEnsureDefaultWorkflow_SeedsAndIsIdempotent(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := testutil.Ctx()
	tenant := testutil.SeedTenant(t, q, "FreshCo")

	// A brand-new tenant (created after migrations) starts empty.
	if wfs, err := q.ListWorkflows(ctx, tenant.ID); err != nil || len(wfs) != 0 {
		t.Fatalf("expected no workflows before seeding, got %d (err=%v)", len(wfs), err)
	}

	// Twice — the second call must be a no-op, not a duplicate or an error.
	for i := 0; i < 2; i++ {
		if err := auth.EnsureDefaultWorkflowForTest(ctx, q, tenant.ID); err != nil {
			t.Fatalf("EnsureDefaultWorkflow call %d: %v", i+1, err)
		}
	}

	wfs, err := q.ListWorkflows(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wfs) != 1 || wfs[0].Name != "Default" {
		t.Fatalf("expected exactly one 'Default' workflow, got %+v", wfs)
	}

	steps, err := q.ListWorkflowDefinitionSteps(ctx, db.ListWorkflowDefinitionStepsParams{WorkflowID: wfs[0].ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(steps))
	for _, s := range steps {
		got = append(got, s.Name)
	}
	if want := []string{"Translation", "Review"}; !slices.Equal(got, want) {
		t.Fatalf("expected workflow steps %v, got %v", want, got)
	}

	// The Steps page is non-empty too — the two human definitions exist.
	defs, err := q.ListStepDefinitions(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected the two default step definitions, got %d", len(defs))
	}
}
