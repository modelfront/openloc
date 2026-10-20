// Package dashboard is the HTTP surface for dashboards.
// Dashboards and charts are created either here (create.go)
// or through the chat's create_chart MCP tool; both land on the same row shape
// and the same validation.
//
// A dashboard is either SHARED with the org (owner_id NULL) or PERSONAL to one
// user. Every read carries the owner predicate, so a
// colleague's personal dashboard 404s exactly like one that doesn't exist.
//
// A chart row stores a query definition ({measure, dimensions, filters,
// last_days}) — never data. The data endpoint resolves the relative range and
// re-runs the definition through the metrics semantic layer on every call, so
// charts stay live and inherit the layer's structural tenant pin. Role gating:
// reads are manager-tier; writes to a SHARED dashboard are owner/admin, writes
// to a personal one are its owner's alone (see mayWrite). Wired in api.go.
package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	"openloc/internal/dashboarddefaults"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/metrics"
)

// ChartDefinition is the persisted query definition. It is validated by the
// metrics compiler at creation (create_chart) AND at every render — the
// whitelist can shrink between those moments, and a stale chart must degrade
// to a visible error, not a 500.
type ChartDefinition struct {
	Measure    string            `json:"measure"`
	Dimensions []string          `json:"dimensions,omitempty"`
	Filters    map[string]string `json:"filters,omitempty"`
	LastDays   int               `json:"last_days"`
}

// ChartViz is how the FE draws the rows — same vocabulary as the chat's
// render_chart tool.
type ChartViz struct {
	Type   string   `json:"type"`
	XKey   string   `json:"x_key"`
	Series []string `json:"series"`
	// SeriesKey names a dimension whose values become separate bands. The card
	// widens the rows client-side; the query is unaffected.
	SeriesKey string `json:"series_key,omitempty"`
}

type Handler struct {
	q    db.Querier
	pool *pgxpool.Pool
}

func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{q: q, pool: pool}
}

type chartOut struct {
	ID         int64           `json:"id"`
	Title      string          `json:"title"`
	Definition ChartDefinition `json:"definition"`
	Viz        ChartViz        `json:"viz"`
	// Controls this chart shows on top of the dashboard's, for a question the
	// rest of the dashboard does not ask.
	Controls FilterConfig `json:"controls"`
	// Available lists the controls that would actually do something for this
	// chart — what the "add filter" menu offers. Computed rather than stored:
	// it follows the measure, and a stored copy would drift from the registry.
	Available []string `json:"available_controls"`
	Position  int32    `json:"position"`
}

// HandleGet returns one dashboard and its chart definitions (not data — each
// card fetches its own data endpoint so one broken chart can't blank the page).
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
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
	rows, err := h.q.ListDashboardCharts(r.Context(), db.ListDashboardChartsParams{
		DashboardID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "listing dashboard charts failed", "dashboard-charts-list-failed", logging.DashboardID(id))
		httputil.ServerError(w, "list", "dashboard charts")
		return
	}
	charts := make([]chartOut, 0, len(rows))
	for _, c := range rows {
		out, err := toChartOut(c.ID, c.Title, c.Position, c.Definition, c.Viz, c.Controls)
		if err != nil {
			logging.Error(r.Context(), err, "dashboard chart row is corrupt", "dashboard-chart-corrupt", logging.ChartID(c.ID))
			continue // one corrupt row must not take down the dashboard
		}
		charts = append(charts, out)
	}
	// A config that fails to parse degrades to "no controls" — the charts still
	// render, which is the point of keeping the selections out of the row.
	var cfg FilterConfig
	if err := json.Unmarshal(d.Filters, &cfg); err != nil {
		logging.Warn(r.Context(), err, "dashboard filter config is corrupt", "dashboard-filters-corrupt", logging.DashboardID(id))
	}
	if cfg.Controls == nil {
		cfg.Controls = []string{}
	}
	// What the dashboard's own "add filter" menu offers: the union across its
	// charts, since a control belongs on the row if ONE chart can use it.
	defs := make([]ChartDefinition, 0, len(charts))
	for _, c := range charts {
		defs = append(defs, c.Definition)
	}
	httputil.Data(w, map[string]any{
		"id": d.ID, "name": d.Name, "created_at": d.CreatedAt.Time,
		"personal": d.OwnerID != nil, "filters": cfg, "charts": charts,
		"available_controls": AvailableForCharts(defs),
	})
}

// dashboardFor loads a dashboard the caller may see: shared, or their own.
// Someone else's personal dashboard reads as ErrNoRows — the same 404 as one
// that doesn't exist, so the list can't be probed by id.
func (h *Handler) dashboardFor(r *http.Request, id int64) (db.GetDashboardTenantRow, error) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	return h.q.GetDashboardTenant(r.Context(), db.GetDashboardTenantParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
}

// mayWrite reports whether the caller can change this dashboard's
// configuration or delete it.
//
// Shared: owner/admin, because it changes what the whole org sees. Personal:
// its owner, and nobody else — an admin has no business editing a colleague's
// private view, and the visibility predicate means they can't even read it.
func mayWrite(r *http.Request, d db.GetDashboardTenantRow) bool {
	claims, _ := auth.ClaimsFromContext(r.Context())
	return MayWrite(d.OwnerID, claims.UserID, claims.Role)
}

// MayWrite is the dashboard write rule without a request: a personal
// dashboard is its owner's, a shared one is owner/admin's. The MCP tools use
// it too, so both surfaces answer the same.
func MayWrite(ownerID *int64, userID int64, role string) bool {
	if ownerID != nil {
		return *ownerID == userID
	}
	return role == auth.RoleOwner || role == auth.RoleAdmin
}

// HandleSetVisibility moves a dashboard between Shared and the caller's own.
func (h *Handler) HandleSetVisibility(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "dashboardID")
	if err != nil {
		httputil.BadRequest(w, "invalid dashboard id")
		return
	}
	var body struct {
		Personal bool `json:"personal"`
	}
	if err := httputil.Decode(r, &body); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	d, err := h.dashboardFor(r, id)
	if httputil.DBError(w, err, "dashboard") {
		return
	}
	// mayWrite decides both directions. Sharing your OWN dashboard is yours to
	// do — a dashboard is a saved view of data the caller can already read, and
	// requiring an admin would deadlock: they cannot see a personal dashboard in
	// order to publish it. Taking a SHARED one private is owner/admin, because
	// it removes something the whole org was using.
	if !mayWrite(r, d) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	var owner *int64
	if body.Personal {
		owner = &claims.UserID
	}
	n, err := h.q.SetDashboardOwner(r.Context(), db.SetDashboardOwnerParams{
		ID: id, TenantID: claims.TenantID, OwnerID: owner,
	})
	if err != nil {
		// The partial unique indexes reject a name already taken on the side
		// it's moving to — a real conflict the user resolves by renaming.
		if httputil.IsUniqueViolation(err) {
			httputil.Error(w, http.StatusConflict,
				"A dashboard with this name already exists there. Rename it first.")
			return
		}
		logging.Error(r.Context(), err, "changing dashboard visibility failed", "dashboard-visibility-failed", logging.DashboardID(id))
		httputil.ServerError(w, "update", "dashboard")
		return
	}
	if n == 0 {
		httputil.Error(w, http.StatusNotFound, "dashboard not found")
		return
	}
	httputil.NoContent(w)
}

// HandleMoveChart moves a chart to another dashboard.
//
// Writable on BOTH sides: taking a chart off a shared dashboard needs the same
// right as deleting it there, and putting one on needs the right to change what
// the org sees. Nothing about the destination can break the chart — the merge
// drops whatever its measure can't take — with one surprising case, which the
// caller confirms rather than the server forbids.
func (h *Handler) HandleMoveChart(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "chartID")
	if err != nil {
		httputil.BadRequest(w, "invalid chart id")
		return
	}
	var body struct {
		DashboardID int64 `json:"dashboard_id"`
		// ConfirmOverride acknowledges that the destination's controls will take
		// over filters the chart carries of its own.
		ConfirmOverride bool `json:"confirm_override"`
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
	if chart.DashboardID == body.DashboardID {
		httputil.NoContent(w) // already there; a drop on its own dashboard is a no-op
		return
	}
	from, err := h.dashboardFor(r, chart.DashboardID)
	if httputil.DBError(w, err, "chart") {
		return
	}
	to, err := h.dashboardFor(r, body.DashboardID)
	if httputil.DBError(w, err, "dashboard") {
		return
	}
	if !mayWrite(r, from) || !mayWrite(r, to) {
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

	n, err := h.q.MoveDashboardChart(r.Context(), db.MoveDashboardChartParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID, DashboardID: body.DashboardID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "moving chart failed", "dashboard-chart-move-failed", logging.ChartID(id))
		httputil.ServerError(w, "move", "chart")
		return
	}
	if n == 0 {
		httputil.NotFound(w, "chart")
		return
	}
	httputil.NoContent(w)
}

// HandleReorderCharts sets the card order on one dashboard.
//
// The whole order arrives at once and is written in ONE transaction: a
// half-applied reorder would leave two cards claiming the same slot, and
// "ORDER BY position, id" would then silently pick by id — a shuffle the user
// never asked for.
func (h *Handler) HandleReorderCharts(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "dashboardID")
	if err != nil {
		httputil.BadRequest(w, "invalid dashboard id")
		return
	}
	var body struct {
		ChartIDs []int64 `json:"chart_ids"`
	}
	if err := httputil.Decode(r, &body); err != nil || len(body.ChartIDs) == 0 {
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

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "reorder", "charts")
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)
	for i, chartID := range body.ChartIDs {
		// Scoped to THIS dashboard, so an id from another one silently affects
		// nothing rather than being dragged in by a crafted request.
		if _, err := q.ReorderDashboardChart(r.Context(), db.ReorderDashboardChartParams{
			ID: chartID, TenantID: claims.TenantID, UserID: &claims.UserID,
			DashboardID: id, Position: int32(i + 1),
		}); err != nil {
			logging.Error(r.Context(), err, "reordering charts failed", "dashboard-chart-reorder-failed", logging.DashboardID(id))
			httputil.ServerError(w, "reorder", "charts")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		logging.Error(r.Context(), err, "reordering charts failed", "dashboard-chart-reorder-failed", logging.DashboardID(id))
		httputil.ServerError(w, "reorder", "charts")
		return
	}
	httputil.NoContent(w)
}

// HandleUpdateFilters replaces a dashboard's control list. Shared dashboards are
// owner/admin — the config decides what every member of the org sees; a personal
// one is its owner's to configure.
func (h *Handler) HandleUpdateFilters(w http.ResponseWriter, r *http.Request) {
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
	if !mayWrite(r, d) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
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
		httputil.ServerError(w, "save", "dashboard filters")
		return
	}
	n, err := h.q.UpdateDashboardFilters(r.Context(), db.UpdateDashboardFiltersParams{
		ID: id, TenantID: claims.TenantID, Filters: raw,
	})
	if err != nil {
		logging.Error(r.Context(), err, "updating dashboard filters failed", "dashboard-filters-update-failed", logging.DashboardID(id))
		httputil.ServerError(w, "save", "dashboard filters")
		return
	}
	if n == 0 {
		httputil.Error(w, http.StatusNotFound, "dashboard not found")
		return
	}
	httputil.NoContent(w)
}

// maxNameLen matches the CHECK on dashboards.name and dashboard_charts.title.
// Validated here so an over-long name is a 400 naming the limit rather than a
// constraint violation surfacing as a 500.
const maxNameLen = 120

// decodeName reads and trims a {"name": …} body. Empty after trimming is a
// rejection, not a clear: a nameless dashboard cannot be found again, and
// create_chart resolves dashboards BY name.
func decodeName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Name string `json:"name"`
	}
	if err := httputil.Decode(r, &body); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return "", false
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httputil.BadRequest(w, "name cannot be empty")
		return "", false
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		httputil.BadRequest(w, fmt.Sprintf("name cannot be longer than %d characters", maxNameLen))
		return "", false
	}
	return name, true
}

// HandleRenameDashboard renames one dashboard. Gated by mayWrite, like every
// other write: shared needs owner/admin, a personal one belongs to its owner.
func (h *Handler) HandleRenameDashboard(w http.ResponseWriter, r *http.Request) {
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
	if !mayWrite(r, d) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	n, err := h.q.RenameDashboard(r.Context(), db.RenameDashboardParams{
		ID: id, TenantID: claims.TenantID, Name: name,
	})
	if err != nil {
		// Names are unique per side (shared, or one owner's), because
		// create_chart resolves dashboards by name and must land on one row.
		if httputil.IsUniqueViolation(err) {
			httputil.Error(w, http.StatusConflict, "A dashboard with this name already exists.")
			return
		}
		logging.Error(r.Context(), err, "renaming dashboard failed", "dashboard-rename-failed", logging.DashboardID(id))
		httputil.ServerError(w, "rename", "dashboard")
		return
	}
	if n == 0 {
		httputil.Error(w, http.StatusNotFound, "dashboard not found")
		return
	}
	httputil.NoContent(w)
}

// HandleRenameChart retitles one chart. The gate is its DASHBOARD's: retitling
// a chart on a shared dashboard changes what the whole org reads.
//
// Chart titles are not unique — two dashboards may both hold a "Volume" — so
// there is no conflict case here.
func (h *Handler) HandleRenameChart(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "chartID")
	if err != nil {
		httputil.BadRequest(w, "invalid chart id")
		return
	}
	chart, err := h.q.GetDashboardChartTenant(r.Context(), db.GetDashboardChartTenantParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if httputil.DBError(w, err, "chart") {
		return
	}
	d, err := h.dashboardFor(r, chart.DashboardID)
	if httputil.DBError(w, err, "chart") {
		return
	}
	if !mayWrite(r, d) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	title, ok := decodeName(w, r)
	if !ok {
		return
	}
	n, err := h.q.RenameDashboardChart(r.Context(), db.RenameDashboardChartParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID, Title: title,
	})
	if err != nil {
		logging.Error(r.Context(), err, "renaming chart failed", "dashboard-chart-rename-failed", logging.ChartID(id))
		httputil.ServerError(w, "rename", "chart")
		return
	}
	if n == 0 {
		httputil.Error(w, http.StatusNotFound, "chart not found")
		return
	}
	httputil.NoContent(w)
}

// HandleUpdateChartControls sets a chart's OWN control list — the dropdowns it
// shows in addition to the dashboard's.
//
// Overlap with a dashboard control is allowed: a dashboard can gain one later,
// so the rule has to hold at render time, where the chart's own selection wins
// as the more specific of the two.
func (h *Handler) HandleUpdateChartControls(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := h.writableChart(w, r)
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
	// Any control the dashboard can offer, a chart can offer too — including
	// date range, unit and granularity. A card answering over a different
	// window than its neighbours does not mislead, because its controls render
	// INLINE on the card: a chart set to Monthly beside a dashboard set to
	// Weekly shows its own dropdown saying Monthly.
	//
	// Validate() has already rejected anything that is not a real control.
	raw, err := json.Marshal(cfg)
	if err != nil {
		httputil.ServerError(w, "save", "chart filters")
		return
	}
	n, err := h.q.UpdateChartControls(r.Context(), db.UpdateChartControlsParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID, Controls: raw,
	})
	if err != nil {
		logging.Error(r.Context(), err, "updating chart controls failed", "chart-controls-update-failed", logging.ChartID(id))
		httputil.ServerError(w, "save", "chart filters")
		return
	}
	if n == 0 {
		httputil.Error(w, http.StatusNotFound, "chart not found")
		return
	}
	httputil.NoContent(w)
}

// HandleReorderDashboards writes the order of the dashboards in ONE tab.
//
// The caller sends the ids of the tab they dragged in, so shared and personal
// orders never interleave — they are separate lists on screen and separate
// `owner_id` groups in the read.
//
// Every id is checked with mayWrite before anything is written: a shared
// dashboard's order is what the whole org sees, so it is an admin change, while
// a personal one belongs to its owner. One unwritable id fails the whole
// request rather than silently reordering the rest — a half-applied drag is
// worse than a refused one.
func (h *Handler) HandleReorderDashboards(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var body struct {
		DashboardIDs []int64 `json:"dashboard_ids"`
	}
	if err := httputil.Decode(r, &body); err != nil || len(body.DashboardIDs) == 0 {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	for _, id := range body.DashboardIDs {
		d, err := h.dashboardFor(r, id)
		if httputil.DBError(w, err, "dashboard") {
			return
		}
		if !mayWrite(r, d) {
			httputil.Error(w, http.StatusForbidden, "insufficient permissions")
			return
		}
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "reorder", "dashboards")
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)
	for i, id := range body.DashboardIDs {
		if _, err := q.ReorderDashboard(r.Context(), db.ReorderDashboardParams{
			ID: id, TenantID: claims.TenantID, UserID: &claims.UserID, Position: int32(i + 1),
		}); err != nil {
			logging.Error(r.Context(), err, "reordering dashboards failed", "dashboard-reorder-failed", logging.DashboardID(id))
			httputil.ServerError(w, "reorder", "dashboards")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		logging.Error(r.Context(), err, "reordering dashboards failed", "dashboard-reorder-failed")
		httputil.ServerError(w, "reorder", "dashboards")
		return
	}
	httputil.NoContent(w)
}

// HandleResetDefaults replaces the org's SHARED dashboards with the built-in
// set. Owner/admin: it changes what everyone sees, and it deletes work.
//
// Runs in ONE transaction. A partial reset — old dashboards gone, defaults half
// installed — is the worst possible outcome, and the failure mode is a mid-loop
// error rather than anything exotic.
func (h *Handler) HandleResetDefaults(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	if !auth.IsAdmin(r) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "reset", "dashboards")
		return
	}
	defer tx.Rollback(r.Context())
	if err := dashboarddefaults.ResetToDefaults(r.Context(), db.New(tx), claims.TenantID, &claims.UserID); err != nil {
		logging.Error(r.Context(), err, "resetting shared dashboards failed", "dashboard-reset-failed")
		httputil.ServerError(w, "reset", "dashboards")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		logging.Error(r.Context(), err, "resetting shared dashboards failed", "dashboard-reset-failed")
		httputil.ServerError(w, "reset", "dashboards")
		return
	}
	httputil.NoContent(w)
}

// HandleSharedCount reports how many shared dashboards a reset would destroy,
// so the confirmation can name a number rather than a vague warning.
func (h *Handler) HandleSharedCount(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	n, err := h.q.CountSharedDashboards(r.Context(), claims.TenantID)
	if err != nil {
		logging.Error(r.Context(), err, "counting shared dashboards failed", "dashboard-count-failed")
		httputil.ServerError(w, "count", "dashboards")
		return
	}
	// People whose Pinned view holds a shared dashboard's charts: a reset
	// takes those out of it too.
	pinnedUsers, err := h.q.CountPinnedUsersOfSharedDashboards(r.Context(), claims.TenantID)
	if err != nil {
		logging.Error(r.Context(), err, "counting pinned users failed", "dashboard-count-failed")
		httputil.ServerError(w, "count", "dashboards")
		return
	}
	httputil.Data(w, map[string]any{"shared": n, "defaults": len(dashboarddefaults.Defaults), "pinned_users": pinnedUsers})
}

func toChartOut(id int64, title string, position int32, defRaw, vizRaw, controlsRaw []byte) (chartOut, error) {
	var def ChartDefinition
	if err := json.Unmarshal(defRaw, &def); err != nil {
		return chartOut{}, err
	}
	var viz ChartViz
	if err := json.Unmarshal(vizRaw, &viz); err != nil {
		return chartOut{}, err
	}
	// A corrupt control list degrades to "no extra controls" rather than
	// taking the card down: the chart still answers its question.
	var controls FilterConfig
	if len(controlsRaw) > 0 {
		_ = json.Unmarshal(controlsRaw, &controls)
	}
	if controls.Controls == nil {
		controls.Controls = []string{}
	}
	return chartOut{
		ID: id, Title: title, Definition: def, Viz: viz,
		Controls: controls, Available: AvailableControls(def), Position: position,
	}, nil
}

// HandleChartData resolves the chart's relative range and runs its definition
// through the semantic layer. Validation errors (e.g. a measure renamed after
// the chart was created) come back as 422 with the message — the FE renders
// them on the card.
func (h *Handler) HandleChartData(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "chartID")
	if err != nil {
		httputil.BadRequest(w, "invalid chart id")
		return
	}
	row, err := h.q.GetDashboardChartTenant(r.Context(), db.GetDashboardChartTenantParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if httputil.DBError(w, err, "chart") {
		return
	}
	var def ChartDefinition
	var viz ChartViz
	if err := json.Unmarshal(row.Definition, &def); err != nil {
		logging.Error(r.Context(), err, "dashboard chart row is corrupt", "dashboard-chart-corrupt", logging.ChartID(id))
		httputil.ServerError(w, "read", "chart definition")
		return
	}
	if err := json.Unmarshal(row.Viz, &viz); err != nil {
		logging.Error(r.Context(), err, "dashboard chart row is corrupt", "dashboard-chart-corrupt", logging.ChartID(id))
		httputil.ServerError(w, "read", "chart viz")
		return
	}
	// Dashboard-level controls (internal/dashboard/filters.go) override the
	// chart's own definition for whatever this measure supports.
	def, viz = parseOverrides(r).apply(def, viz)

	from, to, err := metrics.RangeFromLastDays(def.LastDays, time.Now())
	if err != nil {
		httputil.Error(w, http.StatusUnprocessableEntity, trimValidation(err))
		return
	}
	res, err := metrics.Run(r.Context(), h.pool, claims.TenantID, metrics.Request{
		Measure:    def.Measure,
		Dimensions: def.Dimensions,
		Filters:    def.Filters,
		From:       from,
		To:         to,
		// A browser renders far more points than a chat reply can carry.
		RowBudget: metrics.ChartRowCap,
	})
	if err != nil {
		if errors.Is(err, metrics.ErrValidation) {
			// Stale definition (whitelist changed) — visible on the card.
			httputil.Error(w, http.StatusUnprocessableEntity, trimValidation(err))
			return
		}
		logging.Error(r.Context(), err, "running chart query failed", "dashboard-chart-data-failed", logging.ChartID(id))
		httputil.ServerError(w, "run", "chart query")
		return
	}
	httputil.Data(w, chartData{Result: res, XKey: viz.XKey})
}

// chartData is the render response: the metrics result, plus the x axis the
// granularity control resolved to. The card needs it separately because a grain
// swap renames the time column (day → week), and the stored viz still names the
// old one.
type chartData struct {
	*metrics.Result
	XKey string `json:"x_key"`
}

// HandleDeleteChart soft-deletes one chart and revokes its share link. The
// dashboard it sits on decides who may (mayWrite).
func (h *Handler) HandleDeleteChart(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "chartID")
	if err != nil {
		httputil.BadRequest(w, "invalid chart id")
		return
	}
	// Load the chart first so its dashboard decides the gate: shared charts are
	// owner/admin, a personal dashboard's are its owner's.
	chart, err := h.q.GetDashboardChartTenant(r.Context(), db.GetDashboardChartTenantParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if httputil.DBError(w, err, "chart") {
		return
	}
	d, err := h.dashboardFor(r, chart.DashboardID)
	if httputil.DBError(w, err, "chart") {
		return
	}
	if !mayWrite(r, d) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	n, err := h.q.SoftDeleteDashboardChart(r.Context(), db.SoftDeleteDashboardChartParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "deleting chart failed", "dashboard-chart-delete-failed", logging.ChartID(id))
		httputil.ServerError(w, "delete", "chart")
		return
	}
	if n == 0 {
		httputil.NotFound(w, "chart")
		return
	}
	httputil.NoContent(w)
}

// HandleDeleteDashboard soft-deletes a dashboard. Its charts go dark with it:
// every chart read joins through a live dashboard.
func (h *Handler) HandleDeleteDashboard(w http.ResponseWriter, r *http.Request) {
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
	if !mayWrite(r, d) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	n, err := h.q.SoftDeleteDashboard(r.Context(), db.SoftDeleteDashboardParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "deleting dashboard failed", "dashboard-delete-failed", logging.DashboardID(id))
		httputil.ServerError(w, "delete", "dashboard")
		return
	}
	if n == 0 {
		httputil.NotFound(w, "dashboard")
		return
	}
	httputil.NoContent(w)
}

func trimValidation(err error) string {
	const prefix = "metrics validation: "
	s := err.Error()
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}
