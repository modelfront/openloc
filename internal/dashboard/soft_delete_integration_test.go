// Deleting a dashboard or a chart soft-deletes it: the row
// is kept and marked, and nothing reads it any more — list, detail, chart data,
// share link — while its name becomes free again.
package dashboard_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func (f *fixture) deletedAtSet(t *testing.T, table string, id int64) bool {
	t.Helper()
	var set bool
	if err := f.pool.QueryRow(context.Background(),
		`SELECT deleted_at IS NOT NULL FROM `+table+` WHERE id = $1`, id).Scan(&set); err != nil {
		t.Fatalf("read %s %d: %v (a soft delete must keep the row)", table, id, err)
	}
	return set
}

func (f *fixture) shareToken(t *testing.T, chartID int64) string {
	t.Helper()
	rec := f.do(t, http.MethodPost, "/api/dashboards/charts/"+itoa(chartID)+"/share", f.tenantA.ID)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("share: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Data.Token == "" {
		t.Fatalf("decode token: %v (%s)", err, rec.Body.String())
	}
	return body.Data.Token
}

func TestIntegrationDeletingADashboardSoftDeletesIt(t *testing.T) {
	f := newFixture(t)
	token := f.shareToken(t, f.chartID)

	if rec := f.do(t, http.MethodDelete, "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if !f.deletedAtSet(t, "dashboards", f.dashID) {
		t.Fatal("the dashboard row is not marked deleted")
	}

	// Gone from every way in: detail, the list, its charts, and a link to one
	// of them.
	for name, path := range map[string]string{
		"detail":     "/api/dashboards/" + itoa(f.dashID),
		"chart data": "/api/dashboards/charts/" + itoa(f.chartID) + "/data",
		"share link": "/api/shared/charts/" + token,
	} {
		if rec := f.do(t, http.MethodGet, path, f.tenantA.ID); rec.Code != http.StatusNotFound {
			t.Errorf("%s after delete = %d, want 404", name, rec.Code)
		}
	}
	for _, n := range f.names(t, f.userA.ID) {
		if n == "Ops" {
			t.Error("the deleted dashboard is still listed")
		}
	}
	// Deleting it again is the same 404 as a dashboard that never existed.
	if rec := f.do(t, http.MethodDelete, "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", rec.Code)
	}
}

func TestIntegrationDeletingAChartSoftDeletesItAndKillsItsLink(t *testing.T) {
	f := newFixture(t)
	token := f.shareToken(t, f.chartID)

	if rec := f.do(t, http.MethodDelete, "/api/dashboards/charts/"+itoa(f.chartID), f.tenantA.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if !f.deletedAtSet(t, "dashboard_charts", f.chartID) {
		t.Fatal("the chart row is not marked deleted")
	}
	// The token is revoked with the delete, not merely hidden behind it.
	var stored *string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT share_token FROM dashboard_charts WHERE id = $1`, f.chartID).Scan(&stored); err != nil {
		t.Fatalf("read token: %v", err)
	}
	if stored != nil {
		t.Error("the deleted chart still holds its share token")
	}
	if rec := f.do(t, http.MethodGet, "/api/shared/charts/"+token, f.tenantA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("share link after delete = %d, want 404", rec.Code)
	}
	if rec := f.do(t, http.MethodGet, "/api/dashboards/charts/"+itoa(f.chartID)+"/data", f.tenantA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("chart data after delete = %d, want 404", rec.Code)
	}

	// Its dashboard lives on without it.
	for _, c := range f.readDashboard(t, f.dashID, f.userA.ID).Charts {
		if c.ID == f.chartID {
			t.Error("the deleted chart is still on its dashboard")
		}
	}
}

// Names are unique among LIVE dashboards only; deleting "Ops" must not forbid
// making another "Ops".
func TestIntegrationADeletedDashboardsNameIsFreeAgain(t *testing.T) {
	f := newFixture(t)

	if rec := f.do(t, http.MethodDelete, "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := f.doBody(t, http.MethodPost, "/api/dashboards", f.tenantA.ID, `{"name":"Ops"}`); rec.Code != http.StatusCreated {
		t.Fatalf("re-create with the deleted name = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

// "Reset to defaults" is a delete reached from the UI, so it marks the shared
// dashboards it replaces rather than removing them — and their links go dark
// with them.
func TestIntegrationResetSoftDeletesTheSharedDashboards(t *testing.T) {
	f := newFixture(t)
	token := f.shareToken(t, f.chartID)

	if rec := f.doBody(t, http.MethodPost, "/api/dashboards/reset-defaults", f.tenantA.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	if !f.deletedAtSet(t, "dashboards", f.dashID) {
		t.Fatal("the replaced shared dashboard is not marked deleted")
	}
	if rec := f.do(t, http.MethodGet, "/api/shared/charts/"+token, f.tenantA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("share link after reset = %d, want 404", rec.Code)
	}
}
