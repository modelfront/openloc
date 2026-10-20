package dashboard

// Removing a filter control.
//
// The rule: taking a control away must not change what a chart SHOWS. So the
// control's current value is frozen into the definitions it was reaching, and
// only then is the control removed. A dashboard that loses its customer
// dropdown keeps answering for Acme; it just stops being able to answer for
// anyone else.
//
// The value comes from the CLIENT, because selections live in the URL rather
// than the database — that is what makes a filtered view a shareable link. The
// consequence is worth stating plainly: whoever removes a control freezes the
// selection THEY were looking at, for everyone.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

type removeFilterBody struct {
	Control string `json:"control"`
	// Value is what the control is set to right now. Empty means "All", which
	// freezes nothing — there is no narrowing to preserve.
	Value string `json:"value"`
}

// HandleRemoveDashboardFilter drops one control from the dashboard's row,
// freezing its value into every chart the control was actually reaching.
//
// Charts whose measure never declared that dimension are left alone: the
// control was already a no-op for them, so writing a filter now would change a
// card the removal was not supposed to touch.
func (h *Handler) HandleRemoveDashboardFilter(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "dashboardID")
	if err != nil {
		httputil.BadRequest(w, "invalid dashboard id")
		return
	}
	var body removeFilterBody
	if err := httputil.Decode(r, &body); err != nil || body.Control == "" {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	d, err := h.dashboardFor(r, id)
	if httputil.DBError(w, err, "dashboard") {
		return
	}
	if !mayWrite(r, d) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	var cfg FilterConfig
	if err := json.Unmarshal(d.Filters, &cfg); err != nil {
		httputil.ServerError(w, "read", "dashboard filters")
		return
	}
	if !slices.Contains(cfg.Controls, body.Control) {
		httputil.Error(w, http.StatusNotFound, "this dashboard has no such control")
		return
	}

	charts, err := h.q.ListDashboardCharts(r.Context(), db.ListDashboardChartsParams{
		DashboardID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if err != nil {
		httputil.ServerError(w, "remove", "filter")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "remove", "filter")
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)

	frozen := 0
	for _, c := range charts {
		// A chart with its OWN control for this dimension is already answering
		// its own selection; the dashboard's was never reaching it.
		if chartOwns(c.Controls, body.Control) {
			continue
		}
		changed, err := freezeInto(r.Context(), q, claims, c.ID, c.Definition, c.Viz, body.Control, body.Value)
		if err != nil {
			logging.Error(r.Context(), err, "freezing a filter into a chart failed", "dashboard-filter-freeze-failed", logging.ChartID(c.ID))
			httputil.ServerError(w, "remove", "filter")
			return
		}
		if changed {
			frozen++
		}
	}

	cfg.Controls = slices.DeleteFunc(cfg.Controls, func(s string) bool { return s == body.Control })
	raw, err := json.Marshal(cfg)
	if err != nil {
		httputil.ServerError(w, "remove", "filter")
		return
	}
	if _, err := q.UpdateDashboardFilters(r.Context(), db.UpdateDashboardFiltersParams{
		ID: id, TenantID: claims.TenantID, Filters: raw,
	}); err != nil {
		httputil.ServerError(w, "remove", "filter")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "remove", "filter")
		return
	}
	httputil.Data(w, map[string]any{"controls": cfg.Controls, "charts_frozen": frozen})
}

// HandleRemoveChartFilter drops one control from a chart's own row.
//
// When the DASHBOARD offers the same control, nothing is frozen: the chart
// simply goes back to following the dashboard, which is what "tied to the
// dashboard's filter" means. Only when the control existed nowhere else does
// its value have to be preserved in the definition.
func (h *Handler) HandleRemoveChartFilter(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := h.writableChart(w, r)
	if !ok {
		return
	}
	var body removeFilterBody
	if err := httputil.Decode(r, &body); err != nil || body.Control == "" {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	chart, err := h.q.GetDashboardChartTenant(r.Context(), db.GetDashboardChartTenantParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if httputil.DBError(w, err, "chart") {
		return
	}
	var own FilterConfig
	if len(chart.Controls) > 0 {
		_ = json.Unmarshal(chart.Controls, &own)
	}
	if !slices.Contains(own.Controls, body.Control) {
		httputil.Error(w, http.StatusNotFound, "this chart has no such control")
		return
	}
	d, err := h.dashboardFor(r, chart.DashboardID)
	if httputil.DBError(w, err, "chart") {
		return
	}
	var dashCfg FilterConfig
	_ = json.Unmarshal(d.Filters, &dashCfg)
	inherits := slices.Contains(dashCfg.Controls, body.Control)

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "remove", "filter")
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)

	frozen := false
	if !inherits {
		frozen, err = freezeInto(r.Context(), q, claims, id, chart.Definition, chart.Viz, body.Control, body.Value)
		if err != nil {
			logging.Error(r.Context(), err, "freezing a filter into a chart failed", "chart-filter-freeze-failed", logging.ChartID(id))
			httputil.ServerError(w, "remove", "filter")
			return
		}
	}

	own.Controls = slices.DeleteFunc(own.Controls, func(s string) bool { return s == body.Control })
	raw, err := json.Marshal(own)
	if err != nil {
		httputil.ServerError(w, "remove", "filter")
		return
	}
	if _, err := q.UpdateChartControls(r.Context(), db.UpdateChartControlsParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID, Controls: raw,
	}); err != nil {
		httputil.ServerError(w, "remove", "filter")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "remove", "filter")
		return
	}
	httputil.Data(w, map[string]any{
		"controls": own.Controls, "frozen": frozen, "inherits_dashboard": inherits,
	})
}

// chartOwns reports whether a chart declares this control itself.
func chartOwns(raw []byte, control string) bool {
	if len(raw) == 0 {
		return false
	}
	var cfg FilterConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return false
	}
	return slices.Contains(cfg.Controls, control)
}

// freezeInto writes a control's value into one chart's stored definition.
// Returns false when there was nothing to freeze, in which case the row is left
// untouched rather than rewritten identically.
func freezeInto(ctx context.Context, q db.Querier, claims *auth.Claims, chartID int64, defRaw, vizRaw []byte, control, value string) (bool, error) {
	var def ChartDefinition
	if err := json.Unmarshal(defRaw, &def); err != nil {
		return false, fmt.Errorf("decode definition: %w", err)
	}
	var viz ChartViz
	if err := json.Unmarshal(vizRaw, &viz); err != nil {
		return false, fmt.Errorf("decode viz: %w", err)
	}
	def, viz, changed := bakeControl(def, viz, control, value)
	if !changed {
		return false, nil
	}
	newDef, err := json.Marshal(def)
	if err != nil {
		return false, err
	}
	newViz, err := json.Marshal(viz)
	if err != nil {
		return false, err
	}
	_, err = q.UpdateChartDefinition(ctx, db.UpdateChartDefinitionParams{
		ID: chartID, TenantID: claims.TenantID, UserID: &claims.UserID,
		Definition: newDef, Viz: newViz,
	})
	return err == nil, err
}
