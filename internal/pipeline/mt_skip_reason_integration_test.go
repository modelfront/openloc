package pipeline_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// The MT phase tells "the router has no engine for this language" (no_route)
// apart from "no MT router at all" (no_assignment): only the first is a project
// that asked for MT and got none.
func TestIntegrationMTPhase_SkipReasonNoRouteVsNoRouter(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, _ := seedTranslateContext(t, q, []string{"Hello world"})
	file, err := q.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		t.Fatal(err)
	}
	proj, err := q.GetProjectByID(ctx, file.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	mtOnly := pipeline.TranslatePhases{UseMT: true}
	setRouter := func(id *int64) { testutil.SetProfileMTRouter(t, q, proj.SettingsProfileID, id) }

	// A router with no default whose only rule is for es; the job is en → fr.
	engine := testutil.SeedMTEngine(t, q, tenantID, "Mock ES", "mock")
	esOnly := testutil.SeedMTRouter(t, q, tenantID, "es only", nil, nil)
	testutil.SeedMTRouterRule(t, q, esOnly, "es", &engine.ID, nil)
	setRouter(&esOnly)
	outcome, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, mtOnly)
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	if outcome.MTSkipReason != pipeline.PhaseSkipNoRoute {
		t.Errorf("router without a route: MTSkipReason = %q, want %q", outcome.MTSkipReason, pipeline.PhaseSkipNoRoute)
	}
	if outcome.MTMissing() != pipeline.PhaseSkipNoRoute {
		t.Errorf("router without a route: MTMissing = %q, want %q", outcome.MTMissing(), pipeline.PhaseSkipNoRoute)
	}

	setRouter(nil)
	outcome, err = svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, mtOnly)
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	if outcome.MTSkipReason != pipeline.PhaseSkipNoAssignment {
		t.Errorf("no router: MTSkipReason = %q, want %q", outcome.MTSkipReason, pipeline.PhaseSkipNoAssignment)
	}
	if outcome.MTMissing() != "" {
		t.Errorf("no router: MTMissing = %q, want empty (MT not set up is not missing MT)", outcome.MTMissing())
	}
}
