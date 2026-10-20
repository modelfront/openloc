package mcp

import (
	"strings"
	"testing"

	"openloc/internal/metrics"
)

// Pins that every registered measure is named in the generated query_metrics
// description: a measure the model has never been told about is unreachable,
// and nothing else fails.
func TestQueryMetricsDescriptionCoversEveryMeasure(t *testing.T) {
	desc := queryMetricsDescription()
	for name := range metrics.Definitions() {
		if !strings.Contains(desc, name) {
			t.Errorf("measure %q is missing from the query_metrics description; "+
				"the model cannot use a measure it has never been told about", name)
		}
	}
}

// A dimension the model does not know about is a chart nobody can ask for.
func TestQueryMetricsDescriptionCoversEveryDimension(t *testing.T) {
	desc := queryMetricsDescription()
	for name := range metrics.Definitions() {
		for _, dim := range metrics.DimensionsFor(name) {
			if !strings.Contains(desc, dim) {
				t.Errorf("dimension %q of measure %q is missing from the description", dim, name)
			}
		}
	}
}

// The flags are in the response whether or not the model is told what they
// mean. Without this guidance it reports a capped answer as the whole picture.
func TestQueryMetricsDescriptionExplainsPartialResults(t *testing.T) {
	desc := queryMetricsDescription()
	for _, want := range []string{"truncated", "folded", "grain"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description never mentions %q, so the model has no reason to relay it", want)
		}
	}
	// Naming the field is not enough — it has to say what it costs the answer.
	if !strings.Contains(desc, "INCOMPLETE") {
		t.Error("description names `truncated` without saying the answer is incomplete")
	}
}
