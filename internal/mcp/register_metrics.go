package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

// registerMetrics registers the metrics and dashboard tools.
func registerMetrics(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:           toolQueryMetrics,
		title:          "Query analytics metrics",
		description:    queryMetricsDescription(),
		requireManager: true,
	}, b.QueryMetrics)

	// Creation tools. Manager-only, matching the REST routes. All
	// additive; descriptions carry the ask-first protocol — a model should
	// gather missing details conversationally, not guess them.
	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateChart,
		title: "Save dashboard chart",
		description: "Save a chart to an org-wide dashboard so it stays available and LIVE " +
			"after this conversation — its relative window (last_days) re-runs on every " +
			"page load. Same measure/dimension/filter vocabulary as openloc_query_metrics. " +
			"Workflow: run openloc_query_metrics first and show the result (render_chart) " +
			"so the user confirms it's the right data, THEN save it here. Reuse an existing " +
			"dashboard name unless the user wants a new one. Title the chart by WHAT it " +
			"shows, never by its window or interval: the dashboard's date-range and " +
			"granularity controls change those, and a title saying \"last 90 days\" then " +
			"contradicts the chart. After creating, open the dashboard with navigate_to.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateChart)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSetDashFilters,
		title: "Set dashboard filters",
		description: "Turn on the interactive control row for an EXISTING dashboard, so one " +
			"set of dropdowns narrows every chart on it instead of each chart carrying a " +
			"fixed window. For a control that belongs to ONE chart — a dropdown that is " +
			"meaningful on one card and useless on the rest, or letting one chart use a " +
			"different interval from its neighbours — use openloc_set_chart_filters instead. Controls: date_range, unit (words/segments/characters), " +
			"granularity (daily/weekly/monthly), and the dimension filters " +
			"target_language, source_language, customer, project, user. This REPLACES the " +
			"current list — pass every control you want kept, or an empty list to remove " +
			"the row. Dimension dropdowns list only values with data in the chosen range. " +
			"Owner/admin only, because it changes what everyone in the org sees. The " +
			"selections themselves are not saved: they live in the URL, so a filtered view " +
			"is a shareable link.",
		requireManager: true,
		requireWrite:   true,
	}, b.SetDashboardFilters)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSetChartFilters,
		title: "Set chart filters",
		description: "Turn on a control row for ONE chart, in addition to the dashboard's. " +
			"Use this when a control belongs to a single card: a dropdown that is meaningful " +
			"on one chart and dead on the others (an AI engine filter beside a token chart), " +
			"or to let one chart use a different granularity, date range or unit from its " +
			"neighbours. Same controls as the dashboard row plus ai_engine. Where both the " +
			"chart and the dashboard offer the same control, the CHART's selection wins. " +
			"This REPLACES the chart's own list — pass every control to keep, or an empty " +
			"list to remove its row; it never touches the dashboard's. The chart is named by " +
			"title within its dashboard, because titles repeat across dashboards. " +
			"Selections are not saved: they live in the URL, so the view stays shareable.",
		requireManager: true,
		requireWrite:   true,
	}, b.SetChartFilters)
}
