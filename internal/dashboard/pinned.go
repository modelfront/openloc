package dashboard

// HTTP side of the Pinned view (rules in pinned_service.go). Every route sits
// in the manager-tier /api/dashboards group, like the rest of dashboards, and
// works only on the caller's own pinned dashboards: there is no per-route role
// gate because nothing here changes what anyone else sees. Pinning reads the
// chart or dashboard through the same visibility as the rest of the package,
// so only what the caller can open can be pinned.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// MountPinned adds the Pinned view's routes, under prefix, to the router that
// serves /api/dashboards (prefix "" when r is that router itself).
func (h *Handler) MountPinned(r chi.Router, prefix string) {
	r.Get(prefix+"/pins", h.HandlePinState)
	r.Get(prefix+"/pinned", h.HandleListPinned)
	r.Put(prefix+"/pinned/order", h.HandleReorderPinned)
	r.Get(prefix+"/pinned/{pinnedID}", h.HandleGetPinned)
	r.Delete(prefix+"/pinned/{pinnedID}", h.HandleDeletePinned)
	r.Put(prefix+"/pinned/{pinnedID}/name", h.HandleRenamePinned)
	r.Put(prefix+"/pinned/{pinnedID}/filters", h.HandleUpdatePinnedFilters)
	r.Get(prefix+"/pinned/{pinnedID}/filter-options", h.HandlePinnedFilterOptions)
	r.Put(prefix+"/pinned/{pinnedID}/chart-order", h.HandleReorderPinnedCharts)
	r.Put(prefix+"/pinned/charts/{chartID}/dashboard", h.HandleMovePinnedChart)
	r.Post(prefix+"/charts/{chartID}/pin", h.HandlePinChart)
	r.Delete(prefix+"/charts/{chartID}/pin", h.HandleUnpinChart)
	r.Post(prefix+"/{dashboardID}/pin", h.HandlePinDashboard)
	r.Delete(prefix+"/{dashboardID}/pin", h.HandleUnpinDashboard)
}

// pinnedChart is a chart as a pinned dashboard shows it: the chart itself,
// plus where it lives, for the link to its page.
type pinnedChart struct {
	chartOut
	DashboardID       int64  `json:"dashboard_id"`
	DashboardName     string `json:"dashboard_name"`
	DashboardPersonal bool   `json:"dashboard_personal"`
}

type pinnedListItem struct {
	ID            int64          `json:"id"`
	Name          string         `json:"name"`
	ChartCount    int            `json:"chart_count"`
	PreviewCharts []previewChart `json:"preview_charts"`
}

func scopeOf(r *http.Request) (int64, int64) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	return claims.TenantID, claims.UserID
}

func (h *Handler) scope(r *http.Request) pinScope {
	tenantID, userID := scopeOf(r)
	return pinScope{q: h.q, tenantID: tenantID, userID: userID}
}

// HandlePinState lists what the pin icons show as pinned.
func (h *Handler) HandlePinState(w http.ResponseWriter, r *http.Request) {
	charts, dashboards, err := h.scope(r).pinState(r.Context())
	if err != nil {
		logging.Error(r.Context(), err, "reading pin state failed", "pinned-state-failed")
		httputil.ServerError(w, "read", "pins")
		return
	}
	httputil.Data(w, map[string]any{"chart_ids": charts, "dashboard_ids": dashboards})
}

// HandleListPinned lists the caller's pinned dashboards, each with its first
// PreviewCharts charts. One that shows nothing (its charts were deleted or are
// no longer visible) is left out unless it follows a dashboard.
func (h *Handler) HandleListPinned(w http.ResponseWriter, r *http.Request) {
	ps := h.scope(r)
	pinned, err := ps.q.ListPinnedDashboards(r.Context(), db.ListPinnedDashboardsParams{TenantID: ps.tenantID, UserID: ps.userID})
	if err != nil {
		logging.Error(r.Context(), err, "listing pinned dashboards failed", "pinned-list-failed")
		httputil.ServerError(w, "list", "pinned dashboards")
		return
	}
	rows, err := ps.charts(r.Context(), 0)
	if err != nil {
		logging.Error(r.Context(), err, "listing pinned charts failed", "pinned-charts-list-failed")
		httputil.ServerError(w, "list", "pinned dashboards")
		return
	}
	counts := map[int64]int{}
	previews := map[int64][]previewChart{}
	for _, c := range rows {
		counts[c.PinnedDashboardID]++
		if len(previews[c.PinnedDashboardID]) >= PreviewCharts {
			continue
		}
		var viz ChartViz
		if err := json.Unmarshal(c.Viz, &viz); err != nil {
			logging.Error(r.Context(), err, "dashboard chart row is corrupt", "dashboard-chart-corrupt", logging.ChartID(c.ID))
			continue
		}
		previews[c.PinnedDashboardID] = append(previews[c.PinnedDashboardID], previewChart{ID: c.ID, Title: c.Title, Viz: viz})
	}
	out := make([]pinnedListItem, 0, len(pinned))
	for _, p := range pinned {
		if counts[p.ID] == 0 && !p.FollowsSource {
			continue
		}
		charts := previews[p.ID]
		if charts == nil {
			charts = []previewChart{}
		}
		out = append(out, pinnedListItem{ID: p.ID, Name: p.Name, ChartCount: counts[p.ID], PreviewCharts: charts})
	}
	httputil.Data(w, out)
}

// pinnedFromPath loads the pinned dashboard named in the URL, the caller's own.
func (h *Handler) pinnedFromPath(w http.ResponseWriter, r *http.Request) (db.GetPinnedDashboardRow, bool) {
	id, err := httputil.ParseID(r, "pinnedID")
	if err != nil {
		httputil.BadRequest(w, "invalid pinned dashboard id")
		return db.GetPinnedDashboardRow{}, false
	}
	tenantID, userID := scopeOf(r)
	p, err := h.q.GetPinnedDashboard(r.Context(), db.GetPinnedDashboardParams{ID: id, TenantID: tenantID, UserID: userID})
	if httputil.DBError(w, err, "pinned dashboard") {
		return db.GetPinnedDashboardRow{}, false
	}
	return p, true
}

// HandleGetPinned returns one pinned dashboard: its filter row and its charts,
// each with the dashboard it lives on.
func (h *Handler) HandleGetPinned(w http.ResponseWriter, r *http.Request) {
	p, ok := h.pinnedFromPath(w, r)
	if !ok {
		return
	}
	rows, err := h.scope(r).charts(r.Context(), p.ID)
	if err != nil {
		logging.Error(r.Context(), err, "listing pinned charts failed", "pinned-charts-list-failed")
		httputil.ServerError(w, "list", "pinned charts")
		return
	}
	charts := make([]pinnedChart, 0, len(rows))
	defs := make([]ChartDefinition, 0, len(rows))
	for i, c := range rows {
		out, err := toChartOut(c.ID, c.Title, int32(i+1), c.Definition, c.Viz, c.Controls)
		if err != nil {
			logging.Error(r.Context(), err, "dashboard chart row is corrupt", "dashboard-chart-corrupt", logging.ChartID(c.ID))
			continue
		}
		charts = append(charts, pinnedChart{chartOut: out, DashboardID: c.DashboardID, DashboardName: c.DashboardName, DashboardPersonal: c.DashboardPersonal})
		defs = append(defs, out.Definition)
	}
	var cfg FilterConfig
	if err := json.Unmarshal(p.Filters, &cfg); err != nil {
		logging.Warn(r.Context(), err, "pinned dashboard filter config is corrupt", "pinned-filters-corrupt")
	}
	if cfg.Controls == nil {
		cfg.Controls = []string{}
	}
	httputil.Data(w, map[string]any{
		"id": p.ID, "name": p.Name, "created_at": p.CreatedAt.Time, "filters": cfg,
		"charts": charts, "available_controls": AvailableForCharts(defs),
	})
}

// HandlePinnedFilterOptions is HandleFilterOptions for a pinned dashboard.
func (h *Handler) HandlePinnedFilterOptions(w http.ResponseWriter, r *http.Request) {
	p, ok := h.pinnedFromPath(w, r)
	if !ok {
		return
	}
	var cfg FilterConfig
	if err := json.Unmarshal(p.Filters, &cfg); err != nil {
		logging.Warn(r.Context(), err, "pinned dashboard filter config is corrupt", "pinned-filters-corrupt")
	}
	rows, err := h.scope(r).charts(r.Context(), p.ID)
	if err != nil {
		logging.Warn(r.Context(), err, "listing pinned charts for filter options failed", "pinned-filter-options-charts-failed")
	}
	controls := make([][]byte, 0, len(rows))
	for _, c := range rows {
		controls = append(controls, c.Controls)
	}
	h.serveFilterOptions(w, r, cfg.Controls, controls, logging.Label("pinned_dashboard_id", strconv.FormatInt(p.ID, 10)))
}

// visibleChart loads a chart the caller can see, and its dashboard.
func (h *Handler) visibleChart(w http.ResponseWriter, r *http.Request) (db.GetDashboardChartTenantRow, db.GetDashboardTenantRow, bool) {
	id, err := httputil.ParseID(r, "chartID")
	if err != nil {
		httputil.BadRequest(w, "invalid chart id")
		return db.GetDashboardChartTenantRow{}, db.GetDashboardTenantRow{}, false
	}
	tenantID, userID := scopeOf(r)
	c, err := h.q.GetDashboardChartTenant(r.Context(), db.GetDashboardChartTenantParams{ID: id, TenantID: tenantID, UserID: &userID})
	if httputil.DBError(w, err, "chart") {
		return db.GetDashboardChartTenantRow{}, db.GetDashboardTenantRow{}, false
	}
	d, err := h.dashboardFor(r, c.DashboardID)
	if httputil.DBError(w, err, "chart") {
		return db.GetDashboardChartTenantRow{}, db.GetDashboardTenantRow{}, false
	}
	return c, d, true
}

// runPin runs one rule and answers 204, or the error's status.
func (h *Handler) runPin(w http.ResponseWriter, r *http.Request, action string, fn func(ps pinScope) error) {
	tenantID, userID := scopeOf(r)
	err := h.pinTx(r.Context(), tenantID, userID, fn)
	switch {
	case err == nil:
		httputil.NoContent(w)
	case errors.Is(err, pgx.ErrNoRows):
		httputil.NotFound(w, "pinned dashboard")
	case errors.Is(err, errNotPinned):
		httputil.NotFound(w, "pinned chart")
	default:
		httputil.ServiceError(w, err, action, "pinned dashboards")
	}
}

func (h *Handler) HandlePinChart(w http.ResponseWriter, r *http.Request) {
	c, d, ok := h.visibleChart(w, r)
	if !ok {
		return
	}
	h.runPin(w, r, "pin", func(ps pinScope) error { return ps.pinChart(r.Context(), c, d) })
}

func (h *Handler) HandleUnpinChart(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "chartID")
	if err != nil {
		httputil.BadRequest(w, "invalid chart id")
		return
	}
	h.runPin(w, r, "unpin", func(ps pinScope) error { return ps.unpinChart(r.Context(), id) })
}

func (h *Handler) HandlePinDashboard(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "dashboardID")
	if err != nil {
		httputil.BadRequest(w, "invalid dashboard id")
		return
	}
	d, err := h.dashboardFor(r, id)
	if httputil.DBError(w, err, "dashboard") {
		return
	}
	h.runPin(w, r, "pin", func(ps pinScope) error { return ps.pinDashboard(r.Context(), d) })
}

func (h *Handler) HandleUnpinDashboard(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "dashboardID")
	if err != nil {
		httputil.BadRequest(w, "invalid dashboard id")
		return
	}
	h.runPin(w, r, "unpin", func(ps pinScope) error { return ps.unpinDashboard(r.Context(), id) })
}

// HandleDeletePinned removes a pinned dashboard from the view. The charts it
// referenced are untouched; they are only unpinned.
func (h *Handler) HandleDeletePinned(w http.ResponseWriter, r *http.Request) {
	p, ok := h.pinnedFromPath(w, r)
	if !ok {
		return
	}
	h.runPin(w, r, "delete", func(ps pinScope) error { return ps.remove(r.Context(), p.ID) })
}

func (h *Handler) HandleRenamePinned(w http.ResponseWriter, r *http.Request) {
	p, ok := h.pinnedFromPath(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	tenantID, userID := scopeOf(r)
	if _, err := h.q.RenamePinnedDashboard(r.Context(), db.RenamePinnedDashboardParams{
		ID: p.ID, TenantID: tenantID, UserID: userID, Name: name,
	}); err != nil {
		logging.Error(r.Context(), err, "renaming pinned dashboard failed", "pinned-rename-failed")
		httputil.ServerError(w, "rename", "pinned dashboard")
		return
	}
	httputil.NoContent(w)
}

func (h *Handler) HandleUpdatePinnedFilters(w http.ResponseWriter, r *http.Request) {
	p, ok := h.pinnedFromPath(w, r)
	if !ok {
		return
	}
	var cfg FilterConfig
	if err := httputil.Decode(r, &cfg); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := cfg.Validate(); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		httputil.ServerError(w, "save", "pinned dashboard filters")
		return
	}
	tenantID, userID := scopeOf(r)
	if _, err := h.q.UpdatePinnedDashboardFilters(r.Context(), db.UpdatePinnedDashboardFiltersParams{
		ID: p.ID, TenantID: tenantID, UserID: userID, Filters: raw,
	}); err != nil {
		logging.Error(r.Context(), err, "updating pinned dashboard filters failed", "pinned-filters-update-failed")
		httputil.ServerError(w, "save", "pinned dashboard filters")
		return
	}
	httputil.NoContent(w)
}

// HandleReorderPinned saves the order of the caller's pinned dashboards. Ids
// that are not theirs update nothing.
func (h *Handler) HandleReorderPinned(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PinnedIDs []int64 `json:"pinned_ids"`
	}
	if err := httputil.Decode(r, &body); err != nil || len(body.PinnedIDs) == 0 {
		httputil.BadRequest(w, "pinned_ids is required")
		return
	}
	h.runPin(w, r, "reorder", func(ps pinScope) error {
		for i, id := range body.PinnedIDs {
			if _, err := ps.q.ReorderPinnedDashboard(r.Context(), db.ReorderPinnedDashboardParams{
				ID: id, TenantID: ps.tenantID, UserID: ps.userID, Position: int32(i + 1),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (h *Handler) HandleReorderPinnedCharts(w http.ResponseWriter, r *http.Request) {
	p, ok := h.pinnedFromPath(w, r)
	if !ok {
		return
	}
	var body struct {
		ChartIDs []int64 `json:"chart_ids"`
	}
	if err := httputil.Decode(r, &body); err != nil || len(body.ChartIDs) == 0 {
		httputil.BadRequest(w, "chart_ids is required")
		return
	}
	h.runPin(w, r, "reorder", func(ps pinScope) error { return ps.reorderCharts(r.Context(), p.ID, body.ChartIDs) })
}

// HandleMovePinnedChart moves a pinned chart to another of the caller's
// pinned dashboards. Only the reference moves; the chart stays where it lives.
func (h *Handler) HandleMovePinnedChart(w http.ResponseWriter, r *http.Request) {
	chartID, err := httputil.ParseID(r, "chartID")
	if err != nil {
		httputil.BadRequest(w, "invalid chart id")
		return
	}
	var body struct {
		PinnedDashboardID int64 `json:"pinned_dashboard_id"`
	}
	if err := httputil.Decode(r, &body); err != nil || body.PinnedDashboardID <= 0 {
		httputil.BadRequest(w, "pinned_dashboard_id is required")
		return
	}
	h.runPin(w, r, "move", func(ps pinScope) error { return ps.moveChart(r.Context(), chartID, body.PinnedDashboardID) })
}
