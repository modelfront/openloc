// The list's chart previews: the first charts of each dashboard, in the order
// the dashboard shows them, and only charts the caller could open anyway.
package dashboard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"openloc/internal/dashboard"
	db "openloc/internal/database/sqlcgen"
)

type previewItem struct {
	ID            int64 `json:"id"`
	PreviewCharts []struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
		Viz   struct {
			Type string `json:"type"`
		} `json:"viz"`
	} `json:"preview_charts"`
}

func (f *fixture) previews(t *testing.T, tenantID, userID int64) map[int64]previewItem {
	t.Helper()
	rec := f.doAs(t, http.MethodGet, "/api/dashboards", tenantID, userID)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []previewItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := map[int64]previewItem{}
	for _, d := range body.Data {
		out[d.ID] = d
	}
	return out
}

func (f *fixture) addChart(t *testing.T, tenantID, dashID int64, title string) int64 {
	t.Helper()
	c, err := f.q.CreateDashboardChart(context.Background(), db.CreateDashboardChartParams{
		TenantID: tenantID, DashboardID: dashID, Title: title,
		Definition: []byte(`{"measure":"qa_issues","dimensions":["severity"],"last_days":30}`),
		Viz:        []byte(`{"type":"pie","x_key":"severity","series":["issues"]}`),
	})
	if err != nil {
		t.Fatalf("seed chart %q: %v", title, err)
	}
	return c.ID
}

func previewIDs(d previewItem) []int64 {
	ids := make([]int64, 0, len(d.PreviewCharts))
	for _, c := range d.PreviewCharts {
		ids = append(ids, c.ID)
	}
	return ids
}

// Six charts, one deleted, the order changed by hand: the preview is the first
// four LIVE charts in the dashboard's current order, not in creation order.
func TestIntegrationListPreviewsFirstChartsInDashboardOrder(t *testing.T) {
	f := newFixture(t)
	ids := []int64{f.chartID}
	for i := 2; i <= 6; i++ {
		ids = append(ids, f.addChart(t, f.tenantA.ID, f.dashID, fmt.Sprintf("Chart %d", i)))
	}
	if rec := f.do(t, http.MethodDelete, "/api/dashboards/charts/"+itoa(ids[1]), f.tenantA.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	// Last chart first; the rest keep their places.
	order := []int64{ids[5], ids[0], ids[2], ids[3], ids[4]}
	parts := make([]string, len(order))
	for i, id := range order {
		parts[i] = itoa(id)
	}
	body := `{"chart_ids":[` + strings.Join(parts, ",") + `]}`
	if rec := f.doBody(t, http.MethodPut, "/api/dashboards/"+itoa(f.dashID)+"/chart-order", f.tenantA.ID, body); rec.Code != http.StatusNoContent {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body.String())
	}

	got := f.previews(t, f.tenantA.ID, f.userA.ID)[f.dashID]
	if want := order[:dashboard.PreviewCharts]; !slices.Equal(previewIDs(got), want) {
		t.Errorf("preview = %v, want %v", previewIDs(got), want)
	}
	// Enough to draw: the card needs the chart type, and the title for its label.
	if c := got.PreviewCharts[1]; c.Title != "QA by severity" || c.Viz.Type != "bar" {
		t.Errorf("preview chart = %+v, want the seeded bar chart", c)
	}
}

// An empty dashboard previews nothing, and says so with an empty list rather
// than null.
func TestIntegrationListPreviewOfEmptyDashboardIsEmptyList(t *testing.T) {
	f := newFixture(t)
	d, err := f.q.CreateDashboard(context.Background(), db.CreateDashboardParams{TenantID: f.tenantA.ID, Name: "Empty"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec := f.do(t, http.MethodGet, "/api/dashboards", f.tenantA.ID)
	if !strings.Contains(rec.Body.String(), fmt.Sprintf(`{"id":%d,"name":"Empty","chart_count":0,`, d.ID)) {
		t.Fatalf("empty dashboard missing from list: %s", rec.Body.String())
	}
	if got := f.previews(t, f.tenantA.ID, f.userA.ID)[d.ID]; got.PreviewCharts == nil || len(got.PreviewCharts) != 0 {
		t.Errorf("preview_charts = %#v, want []", got.PreviewCharts)
	}
}

// A preview is a read of the chart, so it follows the same boundary: a
// colleague's personal dashboard and another tenant's charts never appear.
// Checked on the query itself as well as the endpoint: the handler only
// attaches previews to dashboards the list already returned, which would hide
// a leak in the preview query.
func TestIntegrationListPreviewsStayInsideVisibility(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	private, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Mine", CreatedBy: &f.otherA.ID, OwnerID: &f.otherA.ID,
	})
	if err != nil {
		t.Fatalf("seed personal: %v", err)
	}
	secret := f.addChart(t, f.tenantA.ID, private.ID, "Private chart")
	other, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: f.tenantB.ID, Name: "B ops"})
	if err != nil {
		t.Fatalf("seed tenant B: %v", err)
	}
	foreign := f.addChart(t, f.tenantB.ID, other.ID, "Tenant B chart")

	seen := f.previews(t, f.tenantA.ID, f.userA.ID)
	if _, ok := seen[private.ID]; ok {
		t.Error("a colleague's personal dashboard is listed")
	}
	if _, ok := seen[other.ID]; ok {
		t.Error("another tenant's dashboard is listed")
	}
	for _, d := range seen {
		for _, id := range previewIDs(d) {
			if id == secret || id == foreign {
				t.Errorf("dashboard %d previews chart %d the caller cannot open", d.ID, id)
			}
		}
	}
	rows, err := f.q.ListDashboardPreviewCharts(ctx, db.ListDashboardPreviewChartsParams{
		TenantID: f.tenantA.ID, UserID: &f.userA.ID, PerDashboard: dashboard.PreviewCharts,
	})
	if err != nil {
		t.Fatalf("preview query: %v", err)
	}
	for _, r := range rows {
		if r.ID == secret || r.ID == foreign {
			t.Errorf("the preview query returned chart %d, which the caller cannot open", r.ID)
		}
	}
	// Its owner does see it — the predicate is ownership, not "no personal".
	if got := previewIDs(f.previews(t, f.tenantA.ID, f.otherA.ID)[private.ID]); !slices.Equal(got, []int64{secret}) {
		t.Errorf("owner's preview = %v, want [%d]", got, secret)
	}
}
