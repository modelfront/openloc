package dashboard

import (
	"encoding/json"
	"net/http"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// PreviewCharts is how many charts a dashboard card previews: the dashboard's
// first two rows, which is what fits a card at two cards per row.
const PreviewCharts = 4

// previewChart is what a card needs to draw a chart small: its data comes from
// the chart's own data endpoint, like on the dashboard page.
type previewChart struct {
	ID    int64    `json:"id"`
	Title string   `json:"title"`
	Viz   ChartViz `json:"viz"`
}

type listItem struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ChartCount int64  `json:"chart_count"`
	// Personal marks a dashboard only this user can see; the list page tabs
	// on it rather than making a second request.
	Personal  bool      `json:"personal"`
	CreatedAt time.Time `json:"created_at"`
	// The first PreviewCharts charts, in dashboard order.
	PreviewCharts []previewChart `json:"preview_charts"`
}

// HandleList returns the dashboards the caller may see, with chart counts and
// each one's first PreviewCharts charts for the list page's previews.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	rows, err := h.q.ListDashboards(r.Context(), db.ListDashboardsParams{
		TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "listing dashboards failed", "dashboard-list-failed")
		httputil.ServerError(w, "list", "dashboards")
		return
	}
	previews, err := h.q.ListDashboardPreviewCharts(r.Context(), db.ListDashboardPreviewChartsParams{
		TenantID: claims.TenantID, UserID: &claims.UserID, PerDashboard: PreviewCharts,
	})
	if err != nil {
		logging.Error(r.Context(), err, "listing dashboard previews failed", "dashboard-previews-failed")
		httputil.ServerError(w, "list", "dashboards")
		return
	}
	byDashboard := make(map[int64][]previewChart, len(rows))
	for _, c := range previews {
		var viz ChartViz
		if err := json.Unmarshal(c.Viz, &viz); err != nil {
			logging.Error(r.Context(), err, "dashboard chart row is corrupt", "dashboard-chart-corrupt", logging.ChartID(c.ID))
			continue // one corrupt row must not take down the list
		}
		byDashboard[c.DashboardID] = append(byDashboard[c.DashboardID], previewChart{ID: c.ID, Title: c.Title, Viz: viz})
	}
	out := make([]listItem, 0, len(rows))
	for _, d := range rows {
		charts := byDashboard[d.ID]
		if charts == nil {
			charts = []previewChart{}
		}
		out = append(out, listItem{
			ID: d.ID, Name: d.Name, ChartCount: d.ChartCount,
			Personal: d.OwnerID != nil, CreatedAt: d.CreatedAt.Time,
			PreviewCharts: charts,
		})
	}
	httputil.Data(w, out)
}
