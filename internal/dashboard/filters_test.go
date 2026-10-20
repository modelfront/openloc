package dashboard

import (
	"net/http/httptest"
	"slices"
	"testing"

	"openloc/internal/metrics"
)

func TestFilterConfigValidate(t *testing.T) {
	tests := []struct {
		name     string
		controls []string
		wantErr  bool
	}{
		{"empty", nil, false},
		{"all specials", []string{ControlDateRange, ControlUnit, ControlGranularity}, false},
		{"dimensions", []string{"target_language", "customer", "project", "user", "source_language"}, false},
		{"unknown control", []string{"colour"}, true},
		// Narrow dimensions are real metrics dimensions but deliberately not
		// dashboard controls — one measure each.
		{"narrow dimension rejected", []string{"severity"}, true},
		{"kind rejected", []string{"kind"}, true},
		{"duplicate", []string{"customer", "customer"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := FilterConfig{Controls: tt.controls}.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func overridesFrom(t *testing.T, query string) overrides {
	t.Helper()
	return parseOverrides(httptest.NewRequest("GET", "/x?"+query, nil))
}

func TestParseOverrides(t *testing.T) {
	o := overridesFrom(t, "last_days=30&grain=week&target_language=de&customer=Acme")
	if o.lastDays != 30 {
		t.Errorf("lastDays = %d, want 30", o.lastDays)
	}
	if o.grain != "week" {
		t.Errorf("grain = %q, want week", o.grain)
	}
	if o.dims["target_language"] != "de" || o.dims["customer"] != "Acme" {
		t.Errorf("dims = %v", o.dims)
	}

	// Junk is ignored, not rejected: a stale bookmark must still render.
	bad := overridesFrom(t, "last_days=banana&grain=fortnightly&severity=high")
	if !bad.empty() {
		t.Errorf("expected junk to parse as empty, got %+v", bad)
	}
}

func TestApplyOverridesLastDays(t *testing.T) {
	def, _ := overridesFrom(t, "last_days=7").apply(
		ChartDefinition{Measure: "words_processed", LastDays: 90}, ChartViz{})
	if def.LastDays != 7 {
		t.Errorf("LastDays = %d, want 7", def.LastDays)
	}
}

func TestApplyOverridesGrainSwapsDimensionAndAxis(t *testing.T) {
	def, viz := overridesFrom(t, "grain=month").apply(
		ChartDefinition{Measure: "words_processed", Dimensions: []string{"day"}},
		ChartViz{Type: "line", XKey: "day", Series: []string{"words"}},
	)
	if !slices.Equal(def.Dimensions, []string{"month"}) {
		t.Errorf("Dimensions = %v, want [month]", def.Dimensions)
	}
	if viz.XKey != "month" {
		t.Errorf("XKey = %q, want month", viz.XKey)
	}
}

// The x axis follows the grain only when it was pointing AT the grain. A chart
// plotting engines on x with a month series must keep its axis.
func TestApplyOverridesGrainLeavesForeignAxisAlone(t *testing.T) {
	def, viz := overridesFrom(t, "grain=month").apply(
		ChartDefinition{Measure: "mt_engine_usage", Dimensions: []string{"mt_engine", "week"}},
		ChartViz{Type: "bar", XKey: "mt_engine", Series: []string{"segments"}},
	)
	if !slices.Equal(def.Dimensions, []string{"mt_engine", "month"}) {
		t.Errorf("Dimensions = %v, want [mt_engine month]", def.Dimensions)
	}
	if viz.XKey != "mt_engine" {
		t.Errorf("XKey = %q, want mt_engine (unchanged)", viz.XKey)
	}
}

// A chart with no time dimension has nothing to swap — the control is a no-op
// for it rather than an error.
func TestApplyOverridesGrainNoopWithoutTimeDimension(t *testing.T) {
	def, viz := overridesFrom(t, "grain=month").apply(
		ChartDefinition{Measure: "words_processed", Dimensions: []string{"customer"}},
		ChartViz{Type: "bar", XKey: "customer", Series: []string{"words"}},
	)
	if !slices.Equal(def.Dimensions, []string{"customer"}) {
		t.Errorf("Dimensions = %v, want [customer]", def.Dimensions)
	}
	if viz.XKey != "customer" {
		t.Errorf("XKey = %q, want customer", viz.XKey)
	}
}

// The load-bearing case: the compiler 422s on a filter key the measure doesn't
// declare, so a dashboard-wide filter MUST be dropped for the cards that can't
// take it. qa_issues has no `user` dimension; words_processed does.
func TestApplyOverridesDropsUnsupportedFilter(t *testing.T) {
	o := overridesFrom(t, "user=Anna&target_language=de")

	supported, _ := o.apply(ChartDefinition{Measure: "words_processed"}, ChartViz{})
	if supported.Filters["user"] != "Anna" || supported.Filters["target_language"] != "de" {
		t.Errorf("words_processed filters = %v, want both applied", supported.Filters)
	}

	dropped, _ := o.apply(ChartDefinition{Measure: "qa_issues"}, ChartViz{})
	if _, ok := dropped.Filters["user"]; ok {
		t.Errorf("qa_issues kept unsupported filter %q: %v", "user", dropped.Filters)
	}
	if dropped.Filters["target_language"] != "de" {
		t.Errorf("qa_issues filters = %v, want target_language applied", dropped.Filters)
	}
}

func TestApplyOverridesDashboardWinsOverChartFilter(t *testing.T) {
	def, _ := overridesFrom(t, "customer=Acme").apply(
		ChartDefinition{Measure: "words_processed", Filters: map[string]string{"customer": "Globex"}},
		ChartViz{},
	)
	if def.Filters["customer"] != "Acme" {
		t.Errorf("customer = %q, want Acme (dashboard selection wins)", def.Filters["customer"])
	}
}

// apply receives the stored definition by value, but its slice and map still
// alias the caller's. Editing them in place would corrupt the row the handler
// just read.
func TestApplyOverridesDoesNotMutateInput(t *testing.T) {
	dims := []string{"day"}
	filters := map[string]string{"customer": "Globex"}
	in := ChartDefinition{Measure: "words_processed", Dimensions: dims, Filters: filters}

	overridesFrom(t, "grain=month&customer=Acme").apply(in, ChartViz{XKey: "day"})

	if dims[0] != "day" {
		t.Errorf("input dimensions mutated: %v", dims)
	}
	if filters["customer"] != "Globex" {
		t.Errorf("input filters mutated: %v", filters)
	}
}

func TestApplyOverridesEmptyIsPassthrough(t *testing.T) {
	in := ChartDefinition{Measure: "words_processed", Dimensions: []string{"week"}, LastDays: 90}
	inViz := ChartViz{Type: "line", XKey: "week", Series: []string{"words"}}

	def, viz := overridesFrom(t, "").apply(in, inViz)
	if def.LastDays != 90 || !slices.Equal(def.Dimensions, []string{"week"}) || viz.XKey != "week" {
		t.Errorf("passthrough changed the chart: %+v %+v", def, viz)
	}
}

func TestOverriddenFilters(t *testing.T) {
	tests := []struct {
		name         string
		chartFilters map[string]string
		controls     []string
		want         []string
	}{
		{
			"a control the chart also pins",
			map[string]string{"customer": "Acme"},
			[]string{"date_range", "customer"},
			[]string{"customer"},
		},
		{
			"sorted, so the message is stable",
			map[string]string{"customer": "Acme", "target_language": "de"},
			[]string{"target_language", "customer"},
			[]string{"customer", "target_language"},
		},
		// The common case: a chart pinned to something the destination doesn't
		// control keeps its own filter, so there is nothing to warn about.
		{"no overlap", map[string]string{"project": "Alpha"}, []string{"date_range", "customer"}, nil},
		{"chart pins nothing", nil, []string{"customer"}, nil},
		{"dashboard controls nothing", map[string]string{"customer": "Acme"}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OverriddenFilters(tt.chartFilters, tt.controls)
			if !slices.Equal(got, tt.want) {
				t.Errorf("OverriddenFilters() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ai_engine is offered as a control but is NOT declared by segments_by_origin,
// which supplies every other dropdown's values. Pointing it there would 422 on
// a dimension that measure has never heard of.
func TestOptionsMeasureDeclaresTheDimensionItSupplies(t *testing.T) {
	for _, dim := range filterableDims {
		measure := optionsMeasureFor(dim)
		if !slices.Contains(metrics.DimensionsFor(measure), dim) {
			t.Errorf("control %q takes its options from %q, which does not declare it", dim, measure)
		}
	}
}

// Removing a control must not change what a chart shows: its current value is
// frozen into the definition on the way out.
func TestBakeControl(t *testing.T) {
	base := ChartDefinition{Measure: "words_processed", Dimensions: []string{"week"}, LastDays: 90}
	baseViz := ChartViz{Type: "area", XKey: "week", Series: []string{"words"}}

	t.Run("date range becomes the stored window", func(t *testing.T) {
		def, _, ok := bakeControl(base, baseViz, ControlDateRange, "30")
		if !ok || def.LastDays != 30 {
			t.Errorf("last_days = %d (ok=%v), want 30", def.LastDays, ok)
		}
	})

	t.Run("granularity swaps the dimension and the axis with it", func(t *testing.T) {
		def, viz, ok := bakeControl(base, baseViz, ControlGranularity, "month")
		if !ok || !slices.Equal(def.Dimensions, []string{"month"}) || viz.XKey != "month" {
			t.Errorf("dims=%v x_key=%q ok=%v", def.Dimensions, viz.XKey, ok)
		}
	})

	t.Run("unit freezes into the series, not the query", func(t *testing.T) {
		def, viz, ok := bakeControl(base, baseViz, ControlUnit, "characters")
		if !ok || !slices.Equal(viz.Series, []string{"characters"}) {
			t.Errorf("series = %v, ok=%v", viz.Series, ok)
		}
		if len(def.Filters) != 0 {
			t.Errorf("unit leaked into the query as %v", def.Filters)
		}
	})

	t.Run("a dimension becomes a pinned filter", func(t *testing.T) {
		def, _, ok := bakeControl(base, baseViz, "customer", "Acme")
		if !ok || def.Filters["customer"] != "Acme" {
			t.Errorf("filters = %v, ok=%v", def.Filters, ok)
		}
	})

	// The cases where nothing should be written. A chart the control never
	// reached must come out of this untouched.
	t.Run("nothing to freeze", func(t *testing.T) {
		for _, tc := range []struct{ name, control, value string }{
			{"empty value means All", "customer", ""},
			{"measure does not take it", "check_type", "terminology"},
			{"no time dimension to swap", ControlGranularity, "month"},
			{"unparseable range", ControlDateRange, "banana"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				def := base
				if tc.name == "no time dimension to swap" {
					def = ChartDefinition{Measure: "words_processed", Dimensions: []string{"customer"}, LastDays: 90}
				}
				if _, _, ok := bakeControl(def, baseViz, tc.control, tc.value); ok {
					t.Error("reported a change where there was nothing to freeze")
				}
			})
		}
	})

	// bakeControl takes the definition by value, but its slice and map still
	// alias the caller's stored row.
	t.Run("does not mutate the input", func(t *testing.T) {
		dims := []string{"week"}
		filters := map[string]string{"customer": "Globex"}
		in := ChartDefinition{Measure: "words_processed", Dimensions: dims, Filters: filters, LastDays: 90}
		bakeControl(in, baseViz, ControlGranularity, "month")
		bakeControl(in, baseViz, "customer", "Acme")
		if dims[0] != "week" || filters["customer"] != "Globex" {
			t.Errorf("input mutated: dims=%v filters=%v", dims, filters)
		}
	})
}

// An "add filter" menu offers only controls that would DO something. A dropdown
// that changes nothing is worse than no dropdown: it invites the reader to
// believe the chart responded to it.
func TestAvailableControls(t *testing.T) {
	t.Run("a time chart offers granularity", func(t *testing.T) {
		got := AvailableControls(ChartDefinition{Measure: "words_processed", Dimensions: []string{"week"}})
		if !slices.Contains(got, ControlGranularity) {
			t.Errorf("got %v, want granularity", got)
		}
	})

	// Nothing to re-bucket, so the control would be a no-op.
	t.Run("a chart with no time dimension does not", func(t *testing.T) {
		got := AvailableControls(ChartDefinition{Measure: "words_processed", Dimensions: []string{"customer"}})
		if slices.Contains(got, ControlGranularity) {
			t.Errorf("got %v, want no granularity", got)
		}
	})

	// qa_issues returns only `issues` — there is no second unit to switch to.
	t.Run("unit is offered only where there is a choice", func(t *testing.T) {
		volume := AvailableControls(ChartDefinition{Measure: "words_processed"})
		qa := AvailableControls(ChartDefinition{Measure: "qa_issues"})
		if !slices.Contains(volume, ControlUnit) {
			t.Errorf("volume measure = %v, want unit", volume)
		}
		if slices.Contains(qa, ControlUnit) {
			t.Errorf("qa_issues = %v, want no unit", qa)
		}
	})

	t.Run("only dimensions the measure declares", func(t *testing.T) {
		got := AvailableControls(ChartDefinition{Measure: "qa_issues"})
		if slices.Contains(got, "user") {
			t.Errorf("qa_issues has no user dimension, but %v offers it", got)
		}
		if !slices.Contains(got, "target_language") {
			t.Errorf("got %v, want target_language", got)
		}
	})

	// mt_engine is a real dimension but not a dashboard control; ai_engine is.
	t.Run("only dimensions that are controls at all", func(t *testing.T) {
		got := AvailableControls(ChartDefinition{Measure: "mt_engine_usage"})
		if slices.Contains(got, "mt_engine") {
			t.Errorf("mt_engine is not a filterable control, but %v offers it", got)
		}
	})

	t.Run("date range is always available", func(t *testing.T) {
		for _, m := range []string{"words_processed", "qa_issues", "ai_token_usage"} {
			if !slices.Contains(AvailableControls(ChartDefinition{Measure: m}), ControlDateRange) {
				t.Errorf("%s does not offer date_range", m)
			}
		}
	})
}

// A control belongs on the dashboard row if ONE chart can use it: partial
// application is the established behaviour, and requiring unanimity would hide
// most controls on any mixed dashboard.
func TestAvailableForCharts(t *testing.T) {
	defs := []ChartDefinition{
		{Measure: "qa_issues", Dimensions: []string{"severity"}},               // no user, no unit
		{Measure: "words_processed", Dimensions: []string{"week", "customer"}}, // has both
	}
	got := AvailableForCharts(defs)
	for _, want := range []string{ControlDateRange, ControlUnit, ControlGranularity, "user", "customer"} {
		if !slices.Contains(got, want) {
			t.Errorf("got %v, want it to include %q from at least one chart", got, want)
		}
	}
	// No duplicates, and a stable order so the menu does not shuffle.
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c] {
			t.Errorf("%q listed twice in %v", c, got)
		}
		seen[c] = true
	}
	if len(got) > 0 && got[0] != ControlDateRange {
		t.Errorf("got %v, want date_range first", got)
	}
}

// An empty dashboard has no charts to take a union over. The list must still
// serialize as [] — a null reaches the browser as a missing array, and the
// filter menu reads its length.
func TestAvailableForChartsIsEmptyNotNil(t *testing.T) {
	got := AvailableForCharts(nil)
	if got == nil {
		t.Fatal("AvailableForCharts(nil) is nil; it must be an empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("AvailableForCharts(nil) = %v, want empty", got)
	}
}
