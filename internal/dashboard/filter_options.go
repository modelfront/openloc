package dashboard

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/metrics"
)

// HandleFilterOptions lists the values each enabled dimension control has data
// for in the requested window, so the dropdowns offer what exists rather than
// every value the tenant has ever produced. One query per control (at most
// five); the special controls have fixed option sets and need none.
func (h *Handler) HandleFilterOptions(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "dashboardID")
	if err != nil {
		httputil.BadRequest(w, "invalid dashboard id")
		return
	}
	d, err := h.dashboardFor(r, id)
	if httputil.DBError(w, err, "dashboard") {
		return
	}
	var cfg FilterConfig
	if err := json.Unmarshal(d.Filters, &cfg); err != nil {
		logging.Warn(r.Context(), err, "dashboard filter config is corrupt", "dashboard-filters-corrupt", logging.DashboardID(id))
	}
	charts, err := h.q.ListDashboardCharts(r.Context(), db.ListDashboardChartsParams{
		DashboardID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if err != nil {
		logging.Warn(r.Context(), err, "listing charts for filter options failed", "dashboard-filter-options-charts-failed", logging.DashboardID(id))
	}
	controls := make([][]byte, 0, len(charts))
	for _, c := range charts {
		controls = append(controls, c.Controls)
	}
	h.serveFilterOptions(w, r, cfg.Controls, controls, logging.DashboardID(id))
}

// serveFilterOptions answers a filter-options request for a page whose filter
// row shows rowControls and whose charts carry chartControls of their own.
func (h *Handler) serveFilterOptions(w http.ResponseWriter, r *http.Request, rowControls []string, chartControls [][]byte, subject slog.Attr) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	lastDays := DefaultLastDays
	if n := parseOverrides(r).lastDays; n > 0 {
		lastDays = n
	}
	from, to, err := metrics.RangeFromLastDays(lastDays, time.Now())
	if err != nil {
		httputil.Error(w, http.StatusUnprocessableEntity, trimValidation(err))
		return
	}

	// The union of the row's controls and every chart's own, because a chart
	// can offer a dimension the row does not — an AI-engine dropdown on one
	// card and nowhere else. Without this its options list arrives empty and
	// the control looks broken.
	wanted := append([]string{}, rowControls...)
	for _, raw := range chartControls {
		var cc FilterConfig
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &cc)
		}
		for _, name := range cc.Controls {
			if !slices.Contains(wanted, name) {
				wanted = append(wanted, name)
			}
		}
	}

	out := make(map[string][]string, len(wanted))
	for _, name := range wanted {
		if !slices.Contains(filterableDims, name) {
			continue // date_range / unit / granularity carry fixed options
		}
		vals, err := dimensionOptions(r.Context(), h.pool, claims.TenantID, name, from, to)
		if err != nil {
			// One unavailable list must not blank the whole bar — that control
			// falls back to an empty dropdown, the others still work.
			logging.Warn(r.Context(), err, "dashboard filter options query failed", "dashboard-filter-options-failed", subject)
			vals = []string{}
		}
		out[name] = vals
	}
	httputil.Data(w, out)
}
