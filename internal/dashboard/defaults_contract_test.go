package dashboard

// The defaults live in their own package because internal/dashboard imports
// internal/auth, so the signup paths cannot import this one back. That forced
// the chart shapes to be declared twice.
//
// This is the test that makes the duplication safe: it decodes every default
// through the types the RENDERER actually uses and compiles it through the
// metrics layer. A field renamed on one side, or a measure renamed in the
// registry, fails here rather than on some new org's first visit.

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"openloc/internal/dashboarddefaults"
	"openloc/internal/metrics"
)

// asStored marshals a default the way InstallDefaults does, then decodes it the
// way the render path does — the exact round trip a real chart makes.
func asStored(t *testing.T, c dashboarddefaults.DefaultChart) (ChartDefinition, ChartViz) {
	t.Helper()
	defRaw, err := json.Marshal(c.Definition)
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}
	vizRaw, err := json.Marshal(c.Viz)
	if err != nil {
		t.Fatalf("marshal viz: %v", err)
	}
	var def ChartDefinition
	if err := json.Unmarshal(defRaw, &def); err != nil {
		t.Fatalf("decode definition: %v", err)
	}
	var viz ChartViz
	if err := json.Unmarshal(vizRaw, &viz); err != nil {
		t.Fatalf("decode viz: %v", err)
	}
	return def, viz
}

func TestDefaultsSurviveTheRoundTripAndCompile(t *testing.T) {
	for _, d := range dashboarddefaults.Defaults {
		for _, c := range d.Charts {
			t.Run(d.Name+"/"+c.Title, func(t *testing.T) {
				def, viz := asStored(t, c)

				// A field that failed to cross the package boundary arrives zero.
				if def.Measure == "" {
					t.Fatal("measure did not survive the round trip — the two shapes have drifted")
				}
				if def.LastDays == 0 {
					t.Error("last_days did not survive the round trip")
				}
				if viz.Type == "" || viz.XKey == "" || len(viz.Series) == 0 {
					t.Fatalf("viz did not survive the round trip: %+v", viz)
				}

				from, to, err := metrics.RangeFromLastDays(def.LastDays, time.Now())
				if err != nil {
					t.Fatalf("range: %v", err)
				}
				if err := metrics.Validate(metrics.Request{
					Measure: def.Measure, Dimensions: def.Dimensions, Filters: def.Filters,
					From: from, To: to,
				}); err != nil {
					t.Errorf("default chart does not compile: %v", err)
				}

				// The axis has to name a column the query produces, or the card
				// draws an empty chart rather than failing visibly.
				if !slices.Contains(def.Dimensions, viz.XKey) {
					t.Errorf("x_key %q is not among dimensions %v", viz.XKey, def.Dimensions)
				}
				if viz.SeriesKey != "" && !slices.Contains(def.Dimensions, viz.SeriesKey) {
					t.Errorf("series_key %q is not among dimensions %v", viz.SeriesKey, def.Dimensions)
				}
			})
		}
	}
}

// A control the filter bar cannot draw is a dropdown that does nothing.
func TestDefaultControlsAreReal(t *testing.T) {
	for _, d := range dashboarddefaults.Defaults {
		if err := (FilterConfig{Controls: d.Controls}).Validate(); err != nil {
			t.Errorf("dashboard %q: %v", d.Name, err)
		}
	}
}

// Shared names are unique per tenant, so a duplicate would fail to install on
// every new organisation.
func TestDefaultNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range dashboarddefaults.Defaults {
		if seen[d.Name] {
			t.Errorf("two default dashboards are both named %q", d.Name)
		}
		seen[d.Name] = true
	}
}
