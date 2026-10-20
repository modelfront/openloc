package dashboard

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/metrics"
)

// Dashboard-level filters: one control set per dashboard, applied to every
// chart on it at render time.
//
// Only the control LIST is persisted (dashboards.filters). The selected values
// travel as query parameters on the data endpoint, so a filtered view is a
// shareable URL and nothing is stored per user.
//
// Two controls never reach SQL. `unit` picks which value column the card plots
// — every volume measure already returns words, segments and characters in the
// same row, so switching between them is a client-side series swap. `date_range`
// resolves to last_days, which the metrics layer turns into an absolute window.

// Control names. The dimension controls are the metrics dimension keys
// themselves; the three special ones below can't collide because no measure
// declares a dimension by those names.
const (
	ControlDateRange   = "date_range"
	ControlUnit        = "unit"
	ControlGranularity = "granularity"
)

// DefaultLastDays is the window a dashboard opens with. The controls carry real
// defaults rather than a "chart default" row, so the dashboard — not each
// chart's stored range — decides what the page shows.
const DefaultLastDays = 90

// optionsMeasure derives the dimension dropdowns' values. segments_by_origin is
// the only measure declaring all five wide filterable dimensions, and its predicate
// is the widest (every history event bar 'cleared') — so one query per
// dimension covers the dashboard regardless of which measures its cards use,
// and the options mean the same thing on every card.
const optionsMeasure = "segments_by_origin"

// optionsMeasureFor picks which measure supplies a dimension's dropdown values.
// Most come from segments_by_origin, whose predicate is the widest. A dimension
// that measure does not declare needs its own source, or the options query 422s
// on a key the compiler has never heard of for it.
func optionsMeasureFor(dim string) string {
	switch dim {
	case "ai_engine":
		return "ai_engine_usage"
	default:
		return optionsMeasure
	}
}

// maxOptionsPerDimension bounds a dropdown. Rows arrive busiest-first (the
// compiler orders categorical results by the first value column descending), so
// a truncated list keeps the values anyone would actually pick.
const maxOptionsPerDimension = 200

// filterableDims are the dimensions offered as dashboard controls. The five wide
// ones are declared by at least five of the six measures, so a dashboard mixing
// measures still narrows most of its cards; ai_engine is the exception below. Narrow dimensions (kind, check_type,
// severity) are deliberately absent — one measure each, so a dashboard-wide
// control would be a no-op on every other card.
var filterableDims = []string{
	"target_language",
	"source_language",
	"customer",
	"project",
	"user",
	// Narrower than the rest — only the AI measures declare it — so on a mixed
	// dashboard it visibly narrows some cards and not others. Accepted because
	// an AI dashboard is the case it exists for, and `apply` drops a filter a
	// measure cannot take rather than failing the card.
	"ai_engine",
}

// FilterConfig is the persisted shape of dashboards.filters.
type FilterConfig struct {
	Controls []string `json:"controls"`
}

// Validate rejects unknown controls and duplicates. Called on write so a render
// never has to reason about a config it doesn't recognise.
func (c FilterConfig) Validate() error {
	seen := map[string]bool{}
	for _, name := range c.Controls {
		if seen[name] {
			return fmt.Errorf("control %q listed twice", name)
		}
		seen[name] = true
		switch name {
		case ControlDateRange, ControlUnit, ControlGranularity:
		default:
			if !slices.Contains(filterableDims, name) {
				return fmt.Errorf("unknown control %q", name)
			}
		}
	}
	return nil
}

// dimensionOptions returns the values a dimension actually has data for in the
// window, busiest first. Narrowing the date range therefore narrows every
// dropdown with it — a language pair nobody touched last week isn't offered.
//
// NULL dimensions (a project with no customer) normalize to "" upstream and are
// dropped here: "" is a valid chart key but not something to pick from a list.
func dimensionOptions(ctx context.Context, pool *pgxpool.Pool, tenantID int64, dim string, from, to time.Time) ([]string, error) {
	res, err := metrics.Run(ctx, pool, tenantID, metrics.Request{
		Measure:    optionsMeasureFor(dim),
		Dimensions: []string{dim},
		From:       from,
		To:         to,
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res.Rows))
	seen := map[string]bool{}
	for _, row := range res.Rows {
		v, _ := row[dim].(string)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		if out = append(out, v); len(out) == maxOptionsPerDimension {
			break
		}
	}
	return out, nil
}

// overrides is one request's filter selections.
type overrides struct {
	lastDays int               // 0 = keep the chart's stored range
	grain    string            // "" = keep the chart's stored grain
	dims     map[string]string // dimension filters; empty values dropped
}

// parseOverrides reads the selections off the query string. Unparseable values
// are ignored rather than rejected: a stale bookmark should render the chart,
// not an error page. Real validation happens in the metrics compiler, which
// binds every value it accepts.
func parseOverrides(r *http.Request) overrides {
	q := r.URL.Query()
	o := overrides{dims: map[string]string{}}

	if n, err := strconv.Atoi(q.Get("last_days")); err == nil && n > 0 {
		o.lastDays = n
	}
	if g := q.Get("grain"); metrics.IsTimeGrain(g) {
		o.grain = g
	}
	for _, key := range filterableDims {
		if v := q.Get(key); v != "" {
			o.dims[key] = v
		}
	}
	return o
}

func (o overrides) empty() bool {
	return o.lastDays == 0 && o.grain == "" && len(o.dims) == 0
}

// apply merges the dashboard's selections into one chart's stored definition
// and viz.
//
// Anything the chart's measure doesn't support is DROPPED, not rejected. On a
// dashboard mixing measures, a language filter must narrow the volume cards and
// leave "QA issues by severity" alone — and the compiler 422s on a filter key a
// measure doesn't declare, so the drop has to happen here.
func (o overrides) apply(def ChartDefinition, viz ChartViz) (ChartDefinition, ChartViz) {
	if o.empty() {
		return def, viz
	}
	supported := metrics.DimensionsFor(def.Measure)

	// Copy the reference types: ChartDefinition is passed by value, but its
	// slice and map still alias the caller's, and this function must not edit
	// the stored definition it was handed.
	def.Dimensions = slices.Clone(def.Dimensions)
	def.Filters = maps.Clone(def.Filters)

	if o.lastDays > 0 {
		def.LastDays = o.lastDays
	}

	// Grain: swap in place. A chart with no time dimension (e.g. words by
	// customer) has nothing to swap, so the control is a no-op for it.
	if o.grain != "" {
		for i, d := range def.Dimensions {
			if !metrics.IsTimeGrain(d) {
				continue
			}
			def.Dimensions[i] = o.grain
			// The compiler aliases a grain column to the grain name, so the x
			// axis has to follow it — but only when it was pointing at the old
			// grain rather than at some other dimension.
			if viz.XKey == d {
				viz.XKey = o.grain
			}
			break
		}
	}

	for key, val := range o.dims {
		if metrics.IsTimeGrain(key) || !slices.Contains(supported, key) {
			continue
		}
		if def.Filters == nil {
			def.Filters = map[string]string{}
		}
		def.Filters[key] = val // dashboard selection wins over the chart's own
	}
	return def, viz
}

// OverriddenFilters reports which of a chart's OWN filters a dashboard would
// take over, sorted for a stable message.
//
// Nothing about moving a chart is unsafe — the merge drops whatever a measure
// can't take, so any chart renders on any dashboard. This is the one case where
// the result is surprising rather than wrong: a chart pinned to customer=Acme,
// dropped on a dashboard with a customer control, silently starts answering a
// different question. The caller confirms rather than blocks.
func OverriddenFilters(chartFilters map[string]string, controls []string) []string {
	var out []string
	for key := range chartFilters {
		if slices.Contains(controls, key) {
			out = append(out, key)
		}
	}
	slices.Sort(out)
	return out
}

// bakeControl freezes a control's CURRENT value into a chart's stored
// definition, so removing the control does not change what the chart shows.
//
// The three reshaping controls each bake somewhere different, because each
// changes the query in a different way:
//
//   - date_range  → definition.last_days
//   - granularity → the time dimension, and the x axis that follows it
//   - unit        → viz.series, since the unit was only ever a column choice
//   - a dimension → definition.filters, if the measure declares it
//
// Reports false when there was nothing to freeze: an empty value ("All
// customers"), or a measure that never took this filter in the first place. A
// chart the control never reached must not be edited on its way out.
func bakeControl(def ChartDefinition, viz ChartViz, control, value string) (ChartDefinition, ChartViz, bool) {
	if value == "" {
		return def, viz, false
	}
	// Copy the reference types: the caller holds the stored definition.
	def.Dimensions = slices.Clone(def.Dimensions)
	def.Filters = maps.Clone(def.Filters)

	switch control {
	case ControlDateRange:
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return def, viz, false
		}
		def.LastDays = n
		return def, viz, true

	case ControlGranularity:
		if !metrics.IsTimeGrain(value) {
			return def, viz, false
		}
		for i, d := range def.Dimensions {
			if !metrics.IsTimeGrain(d) {
				continue
			}
			// A chart already on this grain needs no edit, but it still counts
			// as handled — the control's value is now its permanent one.
			if viz.XKey == d {
				viz.XKey = value
			}
			def.Dimensions[i] = value
			return def, viz, true
		}
		return def, viz, false // no time dimension: the control never applied

	case ControlUnit:
		// Unit never reached SQL — it chose which returned column to plot — so
		// it freezes into the viz, not the query.
		viz.Series = []string{value}
		return def, viz, true

	default:
		if !slices.Contains(metrics.DimensionsFor(def.Measure), control) {
			return def, viz, false // this measure never took the filter
		}
		if def.Filters == nil {
			def.Filters = map[string]string{}
		}
		def.Filters[control] = value
		return def, viz, true
	}
}

// unitColumns are the value columns the unit control switches between. A
// measure returning only one of them has nothing to switch.
var unitColumns = []string{"words", "segments", "characters"}

// AvailableControls lists the controls that would actually DO something for one
// chart — what an "add filter" menu should offer.
//
// Excluded rather than offered-and-ignored, because a dropdown that changes
// nothing is worse than no dropdown: it invites the reader to believe the chart
// responded to it.
//
//   - date_range  always: every measure runs over a window.
//   - granularity only with a time dimension to swap.
//   - unit        only where the measure returns more than one unit column.
//   - dimensions  those the measure declares AND that are filterable at all.
func AvailableControls(def ChartDefinition) []string {
	out := []string{ControlDateRange}

	if slices.ContainsFunc(def.Dimensions, metrics.IsTimeGrain) {
		out = append(out, ControlGranularity)
	}
	units := 0
	for _, col := range metrics.ValueColumnsFor(def.Measure) {
		if slices.Contains(unitColumns, col) {
			units++
		}
	}
	if units > 1 {
		out = append(out, ControlUnit)
	}
	// filterableDims order, not the measure's, so every chart offers the same
	// dimensions in the same sequence.
	dims := metrics.DimensionsFor(def.Measure)
	for _, d := range filterableDims {
		if slices.Contains(dims, d) {
			out = append(out, d)
		}
	}
	return out
}

// AvailableForCharts is the union across a dashboard's charts: a control belongs
// on the dashboard row if at least ONE chart can use it. Partial application is
// the established behaviour — `apply` drops what a measure cannot take — so the
// alternative, offering only what every chart shares, would hide most controls
// on any mixed dashboard.
func AvailableForCharts(defs []ChartDefinition) []string {
	seen := map[string]bool{}
	// Never nil: an empty dashboard has no charts to take a union over, and a
	// JSON null reaches the browser as a missing array rather than an empty one.
	out := []string{}
	for _, order := range [][]string{{ControlDateRange, ControlUnit, ControlGranularity}, filterableDims} {
		for _, name := range order {
			for _, def := range defs {
				if !seen[name] && slices.Contains(AvailableControls(def), name) {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
	}
	return out
}
