package mcp

// The query_metrics tool description is GENERATED from the metrics registry,
// so a measure added there is one the chat can name with no second edit — as
// the suggestion prompt (internal/chat/suggest.go) does for the same reason.
//
// Only the GUIDANCE is hand-written — what the vocabulary means for how the
// model should behave. The vocabulary itself has one source.

import (
	"fmt"
	"sort"
	"strings"

	"openloc/internal/metrics"
)

// queryMetricsDescription builds the tool description from the live registry.
func queryMetricsDescription() string {
	defs := metrics.Definitions()
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names) // stable text → prompt-cache friendly

	var b strings.Builder
	b.WriteString("Aggregate organization analytics: one measure grouped by up to 3 dimensions " +
		"over a date range. Filters are server-side — use them instead of fetching broad " +
		"results and filtering yourself. Prefer this tool over per-project iteration for ANY " +
		"'how much / how many over time' question.\n\nMeasures:\n")
	for _, name := range names {
		fmt.Fprintf(&b, "- %s: %s Group or split by: %s.\n",
			name, defs[name], strings.Join(metrics.DimensionsFor(name), ", "))
	}
	b.WriteString("\nEvery measure also accepts the time grains day, week and month as dimensions.\n\n")

	// Behaviour the model cannot infer from the vocabulary. Without this it
	// reports a capped or re-bucketed answer as if it were the whole picture —
	// the flags are in the response either way, but nothing tells it they
	// change what the numbers MEAN.
	b.WriteString("Each result carries the measure's exact definition: relay it when describing numbers. " +
		"Three response fields change what an answer means and must be passed on to the user when set: " +
		"`truncated` — rows were dropped, so any 'top' or 'total' you state is INCOMPLETE; " +
		"`folded` — the smallest values were combined into a single 'Other' row, so 'Other' is a sum " +
		"of many things rather than one; " +
		"`grain` — the answer was re-bucketed to a wider interval than requested because the range " +
		"could not carry it, so say which interval the numbers actually cover.")
	return b.String()
}
