package dashboard

// Copying one chart to a dashboard.
//
// Same rule as copying a whole dashboard (duplicate.go): the gate is on the
// DESTINATION. You need to be able to see the chart — the owner predicate on
// the read already decides that — and the right to write where it is going.
// Copying a chart off the org's dashboard into your own is therefore open to
// anyone who can see it, which is how you experiment on it without touching it.
// MOVING is stricter, because it takes the chart away from its source: that
// needs a write on both ends (HandleMoveChart).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// HandleCopyChart copies a chart — definition, drawing and its own filters —
// onto a dashboard the caller may write to. The share token is NOT copied: the
// copy is a chart nobody has shared yet, and inheriting a live link would hand
// it to whoever holds the original's URL.
//
// POST /api/dashboards/charts/{chartID}/copy  {"dashboard_id": N, "confirm_override": bool}
func (h *Handler) HandleCopyChart(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "chartID")
	if err != nil {
		httputil.BadRequest(w, "invalid chart id")
		return
	}
	var body struct {
		DashboardID     int64 `json:"dashboard_id"`
		ConfirmOverride bool  `json:"confirm_override"`
	}
	if err := httputil.Decode(r, &body); err != nil || body.DashboardID <= 0 {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	chart, err := h.q.GetDashboardChartTenant(r.Context(), db.GetDashboardChartTenantParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if httputil.DBError(w, err, "chart") {
		return
	}
	to, err := h.dashboardFor(r, body.DashboardID)
	if httputil.DBError(w, err, "dashboard") {
		return
	}
	if !mayWrite(r, to) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	var def ChartDefinition
	if err := json.Unmarshal(chart.Definition, &def); err != nil {
		logging.Error(r.Context(), err, "dashboard chart row is corrupt", "dashboard-chart-corrupt", logging.ChartID(id))
		httputil.ServerError(w, "read", "chart definition")
		return
	}
	if msg := overrideWarning(chart.Title, def, to); msg != "" && !body.ConfirmOverride {
		httputil.ErrorCode(w, http.StatusConflict, "filter_override", msg)
		return
	}

	c, err := h.q.CreateDashboardChart(r.Context(), db.CreateDashboardChartParams{
		TenantID: claims.TenantID, DashboardID: to.ID, Title: copyTitle(chart.Title, chart.DashboardID == to.ID),
		Definition: chart.Definition, Viz: chart.Viz, Controls: chart.Controls, CreatedBy: &claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "copying chart failed", "dashboard-chart-copy-failed", logging.ChartID(id))
		httputil.ServerError(w, "copy", "chart")
		return
	}
	httputil.Created(w, map[string]any{"id": c.ID, "title": c.Title, "dashboard_id": to.ID})
}

// copyTitle names the copy. Beside its original it needs a different name, or
// the dashboard shows two identical cards; anywhere else the title is still
// right, and "Copy of" would only be noise to delete.
func copyTitle(title string, sameDashboard bool) string {
	if !sameDashboard {
		return title
	}
	named := []rune(copyPrefix + title)
	if len(named) > maxNameLen {
		named = named[:maxNameLen]
	}
	return string(named)
}

// overrideWarning is the message for a chart whose own fixed filters the
// destination's dashboard controls would take over, or "" when none would.
// Both move and copy land a chart under someone else's control row, so both
// warn before the numbers silently change.
func overrideWarning(title string, def ChartDefinition, to db.GetDashboardTenantRow) string {
	var cfg FilterConfig
	_ = json.Unmarshal(to.Filters, &cfg) // absent/corrupt config = no controls
	clashes := OverriddenFilters(def.Filters, cfg.Controls)
	if len(clashes) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"%q filters by %s. On %q that is a dashboard control, so the dashboard's choice will win and the chart will show different numbers.",
		title, strings.Join(clashes, " and "), to.Name)
}
