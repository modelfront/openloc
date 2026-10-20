package mcp

// CreateChartIn is a chart definition + viz for a named dashboard. The
// definition vocabulary is the same as openloc_query_metrics; it is
// dry-compiled at creation so a bad measure fails HERE, model-visibly,
// not at first dashboard render.
type CreateChartIn struct {
	Dashboard  string            `json:"dashboard" jsonschema:"dashboard name. Found case-insensitively or created if absent — reuse an existing dashboard's name unless the user asks for a new one."`
	Title      string            `json:"title" jsonschema:"short chart title shown on the card. Name WHAT it shows, not the window or the grouping interval — 'Words processed' or 'MT volume by engine', never 'Words processed per week (last 90 days)'. The dashboard's own date-range and granularity controls set those and can change them, which would make such a title wrong."`
	Measure    string            `json:"measure" jsonschema:"same vocabulary as openloc_query_metrics"`
	Dimensions []string          `json:"dimensions,omitempty" jsonschema:"group-by keys, max 3, same vocabulary as openloc_query_metrics. For a time series include exactly one of day, week, month."`
	Filters    map[string]string `json:"filters,omitempty" jsonschema:"equality filters, same vocabulary as openloc_query_metrics"`
	LastDays   int               `json:"last_days" jsonschema:"relative window in days (1-365), resolved at every render so the chart stays live — e.g. 60 for 'last 2 months'"`
	ChartType  string            `json:"chart_type" jsonschema:"one of line, bar, area, pie. line/area for time series, bar for category comparisons, pie only for shares of a small whole."`
	XKey       string            `json:"x_key" jsonschema:"which dimension is the x axis (or pie label); must be one of dimensions"`
	Shared     bool              `json:"shared,omitempty" jsonschema:"true to put the chart on a dashboard the WHOLE ORGANIZATION sees. Owner/admin only — for anyone else the call fails, so ask before setting it. Default false: a new dashboard is the caller's own until they share it."`
	Series     []string          `json:"series" jsonschema:"which value columns to plot (e.g. words, segments, characters, minutes, issues), 1-5. With series_key set, give exactly one."`
	SeriesKey  string            `json:"series_key,omitempty" jsonschema:"optional: a DIMENSION whose values each become a band (kind, mt_engine, target_language, customer...). Use it to break one number down by category — e.g. dimensions=[week,kind], x_key=week, series=[words], series_key=kind gives one stacked band per origin. Must be one of dimensions and differ from x_key."`
}

type CreateChartResult struct {
	ChartID       int64  `json:"chart_id"`
	DashboardID   int64  `json:"dashboard_id"`
	DashboardName string `json:"dashboard_name"`
	// DashboardCreated tells the model whether it landed on an existing
	// dashboard or made a new one — it should say which.
	DashboardCreated bool `json:"dashboard_created"`
	// Personal is true when only this user can see the dashboard. A new one is
	// always personal, so the model should say so and mention that sharing it
	// with the org is an action on the dashboards page.
	Personal bool `json:"personal"`
}

// QueryMetricsIn feeds the semantic metrics layer (internal/metrics).
// Everything here is vocabulary, not SQL: measure,
// dimension and filter keys are validated against a whitelist and values are
// bind-parameterized, so nothing in this struct can address another tenant.
type QueryMetricsIn struct {
	Measure    string            `json:"measure" jsonschema:"which metric to aggregate; see the tool description for the full list of measures. Each has a precise definition returned with the result — relay it rather than inventing one."`
	Dimensions []string          `json:"dimensions,omitempty" jsonschema:"group-by keys, max 3. Measure-specific keys (e.g. mt_engine, target_language, user, project, customer, workflow_step, segment_type, check_type, severity) plus time grains day, week, month. For a chart over time include exactly one grain."`
	Filters    map[string]string `json:"filters,omitempty" jsonschema:"equality filters on the same keys (except time grains). Values are matched case-insensitively; language codes are normalized (DE and de-DE both work); project_id takes a numeric id."`
	From       string            `json:"from" jsonschema:"start of the period, inclusive, as YYYY-MM-DD"`
	To         string            `json:"to" jsonschema:"end of the period, exclusive, as YYYY-MM-DD. Max range 366 days."`
}

// SetDashboardFiltersIn replaces the interactive control row on an existing
// dashboard. Controls are a closed set validated by internal/dashboard, so a
// wrong name fails here where the model can correct it.
type SetDashboardFiltersIn struct {
	Dashboard string   `json:"dashboard" jsonschema:"name of an EXISTING dashboard, matched case-insensitively. This tool never creates one."`
	Controls  []string `json:"controls" jsonschema:"which controls the dashboard shows. Allowed: date_range, unit, granularity, target_language, source_language, customer, project, user. Pass an empty list to remove the control row."`
}

// SetChartFiltersIn replaces ONE chart's own control row. The chart is named by
// title within a dashboard, because titles are not unique across dashboards.
type SetChartFiltersIn struct {
	Dashboard string   `json:"dashboard" jsonschema:"name of the EXISTING dashboard the chart is on, matched case-insensitively."`
	Chart     string   `json:"chart" jsonschema:"title of the chart on that dashboard, matched case-insensitively."`
	Controls  []string `json:"controls" jsonschema:"which controls THIS CHART shows, in addition to the dashboard's. Allowed: date_range, unit, granularity, target_language, source_language, customer, project, user, ai_engine. Pass an empty list to remove the chart's own row."`
}

type SetChartFiltersResult struct {
	ChartID       int64    `json:"chart_id"`
	ChartTitle    string   `json:"chart_title"`
	DashboardName string   `json:"dashboard_name"`
	Controls      []string `json:"controls"`
}

type SetDashboardFiltersResult struct {
	DashboardID   int64    `json:"dashboard_id"`
	DashboardName string   `json:"dashboard_name"`
	Controls      []string `json:"controls"`
}
