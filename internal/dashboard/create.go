package dashboard

// Creating dashboards and charts over HTTP: the endpoints
// behind "New dashboard" and the chart builder. They share validation with the
// chat's create_chart tool, so both paths store the same row.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/metrics"
)

var chartTypes = []string{"line", "bar", "area", "pie"}

// maxSeries matches the chat tool's cap: past five bands a chart is a table.
const maxSeries = 5

// HandleCreateDashboard makes an EMPTY dashboard.
//
// Empty on purpose: a dashboard is a folder, and the charts arrive afterwards.
// Creating one shared is publishing to the org, so that half is owner/admin;
// making one of your own is not.
func (h *Handler) HandleCreateDashboard(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var body struct {
		Name     string `json:"name"`
		Personal bool   `json:"personal"`
	}
	if err := httputil.Decode(r, &body); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len([]rune(name)) > maxNameLen {
		httputil.BadRequest(w, fmt.Sprintf("name must be 1-%d characters", maxNameLen))
		return
	}
	if !body.Personal && !auth.IsAdmin(r) {
		httputil.Error(w, http.StatusForbidden, "Only an owner or admin can create a shared dashboard.")
		return
	}
	var owner *int64
	if body.Personal {
		owner = &claims.UserID
	}
	d, err := h.q.CreateDashboard(r.Context(), db.CreateDashboardParams{
		TenantID: claims.TenantID, Name: name, CreatedBy: &claims.UserID, OwnerID: owner,
	})
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			httputil.Error(w, http.StatusConflict, "A dashboard with this name already exists.")
			return
		}
		logging.Error(r.Context(), err, "creating a dashboard failed", "dashboard-create-failed")
		httputil.ServerError(w, "create", "dashboard")
		return
	}
	httputil.Created(w, map[string]any{"id": d.ID, "name": d.Name, "personal": owner != nil})
}

// ChartSpec is what the builder sends, for a new chart and for an edited one
// alike. Editing is re-stating the whole chart rather than patching it: a
// measure change invalidates every other field, so a partial update would have
// to answer what a leftover dimension means.
type ChartSpec struct {
	Title      string   `json:"title"`
	Measure    string   `json:"measure"`
	Dimensions []string `json:"dimensions"`
	ChartType  string   `json:"chart_type"`
	XKey       string   `json:"x_key"`
	Series     []string `json:"series"`
	SeriesKey  string   `json:"series_key"`
	// Controls the chart carries itself, on top of the dashboard's.
	Controls []string `json:"controls"`
}

// BuiltChart is a validated spec, ready to store.
type BuiltChart struct {
	Title      string
	Definition []byte
	Viz        []byte
	Controls   []byte
}

// HandleCreateChart adds a chart to a dashboard from a definition the caller
// assembled — the same shape the chat tool sends, validated the same way, so a
// chart built in the dialog and one built in conversation are the same object.
//
// last_days is NOT asked for: the date-range control owns the window, and a
// chart carrying its own would be overridden the moment a dashboard has one.
// It is stored at the default so the definition stays complete.
func (h *Handler) HandleCreateChart(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	dashboardID, err := httputil.ParseID(r, "dashboardID")
	if err != nil {
		httputil.BadRequest(w, "invalid dashboard id")
		return
	}
	var body ChartSpec
	if err := httputil.Decode(r, &body); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	d, err := h.dashboardFor(r, dashboardID)
	if httputil.DBError(w, err, "dashboard") {
		return
	}
	if !mayWrite(r, d) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	built, ok := buildChart(w, body, nil)
	if !ok {
		return
	}

	c, err := h.q.CreateDashboardChart(r.Context(), db.CreateDashboardChartParams{
		TenantID: claims.TenantID, DashboardID: dashboardID, Title: built.Title,
		Definition: built.Definition, Viz: built.Viz, Controls: built.Controls, CreatedBy: &claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "creating a chart failed", "dashboard-chart-create-failed", logging.DashboardID(dashboardID))
		httputil.ServerError(w, "create", "chart")
		return
	}
	httputil.Created(w, map[string]any{"id": c.ID, "title": c.Title})
}

// HandleUpdateChart re-states an existing chart from the builder, so a chart
// can be corrected instead of deleted and rebuilt. Same body, same rules — the
// only difference is which row it lands on.
func (h *Handler) HandleUpdateChart(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var body ChartSpec
	if err := httputil.Decode(r, &body); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	// writableChart resolves the chart through its dashboard's owner predicate
	// and applies mayWrite, so a colleague's personal chart is a 404 and a
	// shared one a 403 — the same two answers every other chart write gives.
	chartID, ok := h.writableChart(w, r)
	if !ok {
		return
	}
	// Baked filter VALUES survive an edit. They were frozen by an earlier
	// filter removal, the builder has no field for them, and silently dropping
	// one would change what the chart counts while the user was renaming it.
	// Ticking a control off only takes away the dropdown.
	var frozen map[string]string
	if cur, err := h.q.GetDashboardChartTenant(r.Context(), db.GetDashboardChartTenantParams{
		ID: chartID, TenantID: claims.TenantID, UserID: &claims.UserID,
	}); err == nil {
		var def ChartDefinition
		if json.Unmarshal(cur.Definition, &def) == nil {
			frozen = def.Filters
		}
	}
	built, valid := buildChart(w, body, frozen)
	if !valid {
		return
	}

	n, err := h.q.UpdateDashboardChart(r.Context(), db.UpdateDashboardChartParams{
		ID: chartID, TenantID: claims.TenantID, UserID: &claims.UserID, Title: built.Title,
		Definition: built.Definition, Viz: built.Viz, Controls: built.Controls,
	})
	if err != nil {
		logging.Error(r.Context(), err, "updating a chart failed", "dashboard-chart-update-failed", logging.ChartID(chartID))
		httputil.ServerError(w, "update", "chart")
		return
	}
	if n == 0 {
		httputil.NotFound(w, "chart")
		return
	}
	httputil.NoContent(w)
}

// BuildChart validates a spec and marshals it. The REST handlers and the MCP
// tools enforce exactly these rules, which is the point of it being one
// function. A caller mistake is a httputil.ClientError; a definition the
// metrics compiler rejects is an UnprocessableError.
func BuildChart(body ChartSpec, frozen map[string]string) (BuiltChart, error) {
	title := strings.TrimSpace(body.Title)
	if title == "" || len([]rune(title)) > maxNameLen {
		return BuiltChart{}, httputil.NewClientError(fmt.Sprintf("title must be 1-%d characters", maxNameLen))
	}
	if !slices.Contains(chartTypes, body.ChartType) {
		return BuiltChart{}, httputil.NewClientError(fmt.Sprintf("unknown chart type %q; use line, bar, area or pie", body.ChartType))
	}
	if len(body.Series) == 0 || len(body.Series) > maxSeries {
		return BuiltChart{}, httputil.NewClientError(fmt.Sprintf("pick 1-%d values to plot", maxSeries))
	}
	cols := metrics.ValueColumnsWithUnits(body.Measure)
	unit := ""
	for _, s := range body.Series {
		i := slices.IndexFunc(cols, func(c metrics.ValueColumn) bool { return c.Name == s })
		if i < 0 {
			return BuiltChart{}, httputil.NewClientError(fmt.Sprintf("%q is not a value this measure returns", s))
		}
		// Two columns share an axis only when they count the same thing. Words
		// and segments added together are a number of nothing.
		if unit != "" && cols[i].Unit != unit {
			return BuiltChart{}, httputil.NewClientError("values plotted together must count the same thing")
		}
		unit = cols[i].Unit
	}
	if !slices.Contains(body.Dimensions, body.XKey) {
		return BuiltChart{}, httputil.NewClientError("the axis must be one of the chosen groupings")
	}
	if body.SeriesKey != "" {
		if !slices.Contains(body.Dimensions, body.SeriesKey) {
			return BuiltChart{}, httputil.NewClientError("the split must be one of the chosen groupings")
		}
		if body.SeriesKey == body.XKey {
			return BuiltChart{}, httputil.NewClientError("the split and the axis must be different groupings")
		}
		// The split already produces one series per category value, reading ONE
		// value column; a second would be silently dropped by the pivot.
		if len(body.Series) != 1 {
			return BuiltChart{}, httputil.NewClientError("a chart split into series plots exactly one value")
		}
		if metrics.IsTimeGrain(body.SeriesKey) {
			return BuiltChart{}, httputil.NewClientError("a chart cannot be split by time; use the axis for that")
		}
		// A pie is one ring of slices. There is no second axis to split along.
		if body.ChartType == "pie" {
			return BuiltChart{}, httputil.NewClientError("a pie chart cannot be split into series")
		}
	}

	def := ChartDefinition{
		Measure: body.Measure, Dimensions: body.Dimensions, LastDays: DefaultLastDays,
	}
	// A frozen value the new measure cannot take is dropped: switching measures
	// is redefining the chart, and a filter on a dimension it no longer has
	// would fail to compile.
	for k, v := range frozen {
		if slices.Contains(metrics.DimensionsFor(body.Measure), k) {
			if def.Filters == nil {
				def.Filters = map[string]string{}
			}
			def.Filters[k] = v
		}
	}
	// The compiler is the authority on measure and dimension names, so a bad
	// one fails here with its message rather than at first render.
	from, to, err := metrics.RangeFromLastDays(def.LastDays, time.Now())
	if err != nil {
		return BuiltChart{}, fmt.Errorf("chart window: %w", err)
	}
	if err := metrics.Validate(metrics.Request{
		Measure: def.Measure, Dimensions: def.Dimensions, From: from, To: to,
	}); err != nil {
		return BuiltChart{}, UnprocessableError{Msg: trimValidation(err)}
	}

	// Controls are intersected with what this definition can actually use, so a
	// control that would do nothing is dropped rather than shown and ignored —
	// the same rule the add-filter menu applies (AvailableControls).
	usable := AvailableControls(def)
	controls := []string{}
	for _, c := range body.Controls {
		if slices.Contains(usable, c) && !slices.Contains(controls, c) {
			controls = append(controls, c)
		}
	}

	viz := ChartViz{Type: body.ChartType, XKey: body.XKey, Series: body.Series, SeriesKey: body.SeriesKey}
	defRaw, err := json.Marshal(def)
	if err == nil {
		var vizRaw, controlsRaw []byte
		if vizRaw, err = json.Marshal(viz); err == nil {
			if controlsRaw, err = json.Marshal(FilterConfig{Controls: controls}); err == nil {
				return BuiltChart{Title: title, Definition: defRaw, Viz: vizRaw, Controls: controlsRaw}, nil
			}
		}
	}
	return BuiltChart{}, fmt.Errorf("marshal chart: %w", err)
}

// UnprocessableError is a chart definition the metrics compiler rejects (422).
type UnprocessableError struct{ Msg string }

func (e UnprocessableError) Error() string { return e.Msg }

// buildChart is BuildChart for a handler: it writes the error response itself
// and reports whether the caller may carry on.
func buildChart(w http.ResponseWriter, body ChartSpec, frozen map[string]string) (BuiltChart, bool) {
	built, err := BuildChart(body, frozen)
	if err == nil {
		return built, true
	}
	var unproc UnprocessableError
	switch {
	case errors.As(err, &unproc):
		httputil.Error(w, http.StatusUnprocessableEntity, unproc.Msg)
	default:
		if msg, ok := httputil.AsClientError(err); ok {
			httputil.BadRequest(w, msg)
		} else {
			httputil.ServerError(w, "save", "chart")
		}
	}
	return BuiltChart{}, false
}

// HandleMetricsCatalog is the vocabulary the chart builder needs: every measure
// with what it means, how it can be split, what it returns, and which controls
// it can carry.
//
// Generated from the registry rather than mirrored in the client, for the same
// reason the chat's tool description is: a hand-kept copy drifts the moment a
// measure is added, and the drift is silent.
func (h *Handler) HandleMetricsCatalog(w http.ResponseWriter, r *http.Request) {
	defs := metrics.Definitions()
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	slices.Sort(names)

	type measureOut struct {
		Name       string                `json:"name"`
		Definition string                `json:"definition"`
		Dimensions []string              `json:"dimensions"`
		Values     []metrics.ValueColumn `json:"values"`
		Filters    []string              `json:"filters"`
	}
	out := make([]measureOut, 0, len(names))
	for _, name := range names {
		// project_id is an internal join key, never something to group by in a
		// picker — it would render a chart of database ids. Time grains are sent
		// separately, as `grains`.
		dims := slices.DeleteFunc(metrics.CategoricalDimensionsFor(name), func(d string) bool {
			return d == "project_id"
		})
		// The widest control set this measure can carry. Granularity is in it
		// only because the axis MIGHT be a time grain; the create endpoint
		// intersects against the definition it actually receives, so a control
		// the chart cannot use is dropped there rather than trusted from here.
		filters := AvailableControls(ChartDefinition{Measure: name, Dimensions: []string{"week"}})
		out = append(out, measureOut{
			Name: name, Definition: defs[name], Dimensions: dims,
			Values: metrics.ValueColumnsWithUnits(name), Filters: filters,
		})
	}
	httputil.Data(w, map[string]any{"measures": out, "grains": []string{"day", "week", "month"}})
}
