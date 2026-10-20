package demodata_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demodata"
	"openloc/internal/testutil"
)

// The builders `make seed` shares run twice on every re-seed, so the second
// call must add nothing.
func TestIntegrationSharedBuildersAreIdempotent(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "Seeded Org")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")

	t.Run("operations dashboard", func(t *testing.T) {
		id, added, err := demodata.EnsureOperationsDashboard(ctx, q, tenant.ID, nil)
		if err != nil || added != len(demodata.OperationsCharts) {
			t.Fatalf("first call: added %d, err %v; want %d", added, err, len(demodata.OperationsCharts))
		}
		again, added, err := demodata.EnsureOperationsDashboard(ctx, q, tenant.ID, nil)
		if err != nil || added != 0 || again != id {
			t.Fatalf("second call: id %d added %d err %v; want %d, 0", again, added, err, id)
		}
		d, err := q.GetDashboardTenant(ctx, db.GetDashboardTenantParams{ID: id, TenantID: tenant.ID})
		if err != nil || len(d.Filters) == 0 || string(d.Filters) == "{}" {
			t.Errorf("controls not set: %s (err %v)", d.Filters, err)
		}
	})

	t.Run("personal dashboard", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			if _, err := demodata.EnsurePersonalDashboard(ctx, q, tenant.ID, owner.ID); err != nil {
				t.Fatalf("call %d: %v", i+1, err)
			}
		}
		var dashboards, charts int
		if err := pool.QueryRow(ctx, `
			SELECT count(DISTINCT d.id), count(c.id) FROM dashboards d
			LEFT JOIN dashboard_charts c ON c.dashboard_id = d.id
			WHERE d.tenant_id = $1 AND d.owner_id = $2`, tenant.ID, owner.ID).Scan(&dashboards, &charts); err != nil {
			t.Fatalf("count: %v", err)
		}
		if dashboards != 1 || charts != 1 {
			t.Errorf("after two calls: %d personal dashboards, %d charts; want 1 and 1", dashboards, charts)
		}
	})

	t.Run("workflow library", func(t *testing.T) {
		engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "Review", Purpose: "qe_ape"})
		if err != nil {
			t.Fatalf("engine: %v", err)
		}
		first, err := demodata.EnsureWorkflowLibrary(ctx, q, tenant.ID, &engine.ID)
		if err != nil || !first.HasAI {
			t.Fatalf("first call: HasAI %v, err %v", first.HasAI, err)
		}
		second, err := demodata.EnsureWorkflowLibrary(ctx, q, tenant.ID, &engine.ID)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if second.HumanAI.ID != first.HumanAI.ID || second.AIReview.ID != first.AIReview.ID || second.AIRouter.ID != first.AIRouter.ID {
			t.Errorf("second call created new rows: %+v vs %+v", second, first)
		}
		if first.AIReview.StepType != "hybrid" || first.AIReview.AiRouterID == nil || *first.AIReview.AiRouterID != first.AIRouter.ID {
			t.Errorf("AI review step: %+v, want hybrid on router %d", first.AIReview, first.AIRouter.ID)
		}
	})

	t.Run("notification rules", func(t *testing.T) {
		created, err := demodata.EnsureNotificationRules(ctx, q, tenant.ID)
		if err != nil || !created {
			t.Fatalf("first call: created %v err %v", created, err)
		}
		created, err = demodata.EnsureNotificationRules(ctx, q, tenant.ID)
		if err != nil || created {
			t.Fatalf("second call: created %v err %v", created, err)
		}
	})

	t.Run("job timings", func(t *testing.T) {
		project := testutil.SeedProject(t, q, tenant.ID, "Timed", "en", []string{"de"})
		file := testutil.SeedFile(t, q, project.ID, "a.txt", db.FileFormatTxt)
		job := testutil.SeedJob(t, q, file.ID, "de")
		if err := demodata.WriteJobTimings(ctx, q, tenant.ID, []int64{job.ID}); err != nil {
			t.Fatalf("WriteJobTimings: %v", err)
		}
		var compute, selfSum int64
		if err := pool.QueryRow(ctx, `
			SELECT compute_ms, (SELECT COALESCE(SUM((v ->> 'self_ms')::bigint), 0) FROM jsonb_each(stages) AS e(k, v))
			FROM job_timings WHERE job_id = $1`, job.ID).Scan(&compute, &selfSum); err != nil {
			t.Fatalf("read timings: %v", err)
		}
		if compute == 0 || compute != selfSum {
			t.Errorf("compute_ms = %d, want the sum of self times %d (> 0)", compute, selfSum)
		}
	})
}
