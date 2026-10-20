// Who may change what: every dashboard write is tenant-scoped, and the ones a
// shared dashboard gates to owner/admin refuse a project manager — checked
// against the stored rows, not only the status code.
package dashboard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// snapshot is every column a dashboard write could change, for tenant A's
// fixture dashboard and chart, as one comparable string.
func (f *fixture) snapshot(t *testing.T) string {
	t.Helper()
	var s string
	if err := f.pool.QueryRow(context.Background(), `
		SELECT concat_ws('|', d.name, d.owner_id, d.position, d.filters::text, d.deleted_at,
		                 c.dashboard_id, c.title, c.position, c.definition::text, c.controls::text,
		                 c.share_token, c.deleted_at)
		FROM dashboards d JOIN dashboard_charts c ON c.dashboard_id = d.id
		WHERE d.id = $1 AND c.id = $2`, f.dashID, f.chartID).Scan(&s); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return s
}

// A request from another organisation reaches nothing: the same 404 as an id
// that does not exist, and tenant A's rows are exactly as they were.
func TestIntegrationDashboardWritesAreTenantScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	intruder := testutil.SeedUserWithRole(t, f.q, f.tenantB.ID, "dash-b@test.com", "pass", "owner")
	own, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: f.tenantB.ID, Name: "B ops"})
	if err != nil {
		t.Fatalf("seed tenant B dashboard: %v", err)
	}
	ownChart := f.addChart(t, f.tenantB.ID, own.ID, "B chart")
	f.shareToken(t, f.chartID) // so revoking has something to take away

	dash, chart := itoa(f.dashID), itoa(f.chartID)
	for _, tc := range []struct{ name, method, path, body string }{
		{"visibility", http.MethodPut, "/api/dashboards/" + dash + "/visibility", `{"personal":true}`},
		{"move A's chart to B", http.MethodPut, "/api/dashboards/charts/" + chart + "/dashboard",
			fmt.Sprintf(`{"dashboard_id":%d,"confirm_override":true}`, own.ID)},
		{"move B's chart onto A", http.MethodPut, "/api/dashboards/charts/" + itoa(ownChart) + "/dashboard",
			fmt.Sprintf(`{"dashboard_id":%d,"confirm_override":true}`, f.dashID)},
		{"chart order", http.MethodPut, "/api/dashboards/" + dash + "/chart-order", `{"chart_ids":[` + chart + `]}`},
		{"share", http.MethodPost, "/api/dashboards/charts/" + chart + "/share", ""},
		{"revoke share", http.MethodDelete, "/api/dashboards/charts/" + chart + "/share", ""},
		{"remove dashboard filter", http.MethodPost, "/api/dashboards/" + dash + "/filters/remove",
			`{"control":"target_language","value":"de"}`},
		{"remove chart filter", http.MethodPost, "/api/dashboards/charts/" + chart + "/filters/remove",
			`{"control":"target_language","value":"de"}`},
		{"delete dashboard", http.MethodDelete, "/api/dashboards/" + dash, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.snapshot(t)
			rec := f.doBodyAs(t, tc.method, tc.path, f.tenantB.ID, intruder.ID, tc.body)
			if rec.Code != http.StatusNotFound {
				t.Errorf("tenant B %s = %d, want 404 (%s)", tc.name, rec.Code, rec.Body.String())
			}
			if after := f.snapshot(t); after != before {
				t.Errorf("tenant B %s changed tenant A's rows:\n before %s\n after  %s", tc.name, before, after)
			}
		})
	}

	// The reset's count is tenant B's own: tenant A's shared dashboard is not in it.
	rec := f.doAs(t, http.MethodGet, "/api/dashboards/shared-count", f.tenantB.ID, intruder.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared-count = %d", rec.Code)
	}
	var count struct {
		Data struct {
			Shared int64 `json:"shared"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &count); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if count.Data.Shared != 1 {
		t.Errorf("tenant B shared-count = %d, want 1 (its own dashboard only)", count.Data.Shared)
	}
}

// Deleting on a SHARED dashboard is owner/admin — the gate lives in the handler
// (dashboard.mayWrite), so the router cannot be what enforces it.
func TestIntegrationDashboardDeletesOnSharedRefuseAManager(t *testing.T) {
	f := newFixture(t)
	f.shareToken(t, f.chartID)
	for _, tc := range []struct{ name, method, path string }{
		{"delete dashboard", http.MethodDelete, "/api/dashboards/" + itoa(f.dashID)},
		{"delete chart", http.MethodDelete, "/api/dashboards/charts/" + itoa(f.chartID)},
		{"revoke share", http.MethodDelete, "/api/dashboards/charts/" + itoa(f.chartID) + "/share"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.snapshot(t)
			rec := f.doBodyAsRole(t, tc.method, tc.path, f.tenantA.ID, f.otherA.ID, "project_manager", "")
			if rec.Code != http.StatusForbidden {
				t.Errorf("project_manager %s on a shared dashboard = %d, want 403", tc.name, rec.Code)
			}
			if after := f.snapshot(t); after != before {
				t.Errorf("project_manager %s changed the rows:\n before %s\n after  %s", tc.name, before, after)
			}
		})
	}
}
