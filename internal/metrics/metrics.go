// Package metrics is the semantic layer behind the chat's query_metrics tool.
// It compiles a whitelisted (measure, dimensions,
// filters, time range) request into one tenant-scoped aggregate SQL query.
//
// The security model is structural, not sanitizational: every fragment of SQL
// text is a compile-time Go constant selected by enum key, and every value
// that originates from the model or the user travels as a bind parameter.
// There is no code path on which caller input becomes SQL text. Measures'
// join graphs are frozen constants that terminate in projects, and the
// compiler always pins `p.tenant_id = $1`; on translation_history it also
// pins the table's own tenant_id, which its time index leads.
package metrics

import (
	"errors"
	"fmt"
	"time"
)

// MaxRangeDays caps the queryable window. Aggregates are cheap, but an
// unbounded range invites a full history scan per chat message.
const MaxRangeDays = 366

// RowCap is the DEFAULT row budget: what a chat answer may carry into a model's
// context. Aggregate cardinality is small by construction (dims × grain), so a
// request that needs more than this is usually asking for a resolution the
// range cannot carry — which AutoGrain fixes by widening the bucket rather than
// by dropping rows.
const RowCap = 500

// ChartRowCap is the budget for a rendered chart. Higher than RowCap because
// the consumer is a browser, not a context window: 2000 points draw fine, while
// 2000 rows of JSON in a chat reply are pure cost. Same data, different
// constraint — which is the whole reason the budget is per-caller.
const ChartRowCap = 2000

// Request is the compiled unit: what the tool hands the semantic layer.
// TenantID never comes from the model — the MCP backend fills it from claims.
type Request struct {
	Measure    string            `json:"measure"`
	Dimensions []string          `json:"dimensions"`
	Filters    map[string]string `json:"filters"`
	From       time.Time         `json:"from"`
	To         time.Time         `json:"to"`
	// RowBudget is how many rows this CALLER can use; 0 means RowCap. A chart
	// passes ChartRowCap. Exceeding it widens the time grain, and only then —
	// when there is no grain left to widen — folds the tail into "Other".
	RowBudget int `json:"-"`
	// topN folds a categorical result to its N biggest values plus one "Other".
	// Set by Run on a retry, never by a caller.
	topN int
}

// OtherBucket is the label for the folded tail of a categorical result. Matches
// the client's own series cap, so a chart that folds twice reads consistently.
const OtherBucket = "Other"

// budget resolves the caller's row allowance.
func (r Request) budget() int {
	if r.RowBudget > 0 {
		return r.RowBudget
	}
	return RowCap
}

// Result is rows-of-objects plus enough metadata for the model to describe
// the numbers honestly (definition text, truncation).
type Result struct {
	Measure    string           `json:"measure"`
	Definition string           `json:"definition"`
	Columns    []string         `json:"columns"`
	Rows       []map[string]any `json:"rows"`
	Truncated  bool             `json:"truncated,omitempty"`
	// Folded reports that the smallest values were aggregated into one
	// OtherBucket row because the result did not fit the budget. Nothing is
	// lost, unlike truncation — but the reader has to know the tail is a total,
	// not a value.
	Folded bool `json:"folded,omitempty"`
	// Grain is the time bucket the result actually uses. It differs from the
	// requested one when the range could not carry that resolution — a chart
	// that silently answers at a different resolution than it was asked for is
	// the truncation problem wearing a hat, so this is reported, not hidden.
	Grain string `json:"grain,omitempty"`
	// Averages names the value columns that are not additive, so a chart
	// neither stacks nor sums them.
	Averages []string `json:"averages,omitempty"`
	// Units maps each value column to what it counts ("words", "minutes"), so
	// a chart can print the number with its unit.
	Units map[string]string `json:"units,omitempty"`
	// Derived names the value columns that are sums of other value columns, so
	// a chart does not add them to their parts.
	Derived []string `json:"derived,omitempty"`
}

// ErrValidation marks caller errors (unknown measure/dimension/filter,
// bad range). The tool layer maps it to invalid_argument; anything else
// is internal.
var ErrValidation = errors.New("metrics validation")

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

// measure describes one whitelisted metric. Every string in here is SQL text
// and MUST be a compile-time constant — never build one from caller input.
type measure struct {
	// definition is stated to the model verbatim so it relays what the
	// number means instead of inventing semantics.
	definition string
	// from is the frozen join graph. Must expose alias p for projects
	// (tenant pin) and whatever aliases the dim/value exprs use.
	from string
	// where are fixed measure predicates (event-kind gates etc.),
	// ANDed after the tenant pin. May be empty.
	where string
	// timeExpr is the event-time column the range filter and time grains
	// apply to.
	timeExpr string
	// values are the aggregate output columns, in stable order.
	values []valueCol
	// distinctUnit, when non-empty, makes the compiler dedupe the fact
	// stream on (unit × dim-combo) in an inner subquery before
	// aggregating — e.g. a translation saved five times in a month
	// counts its words once for that month.
	distinctUnit string
	// dims lists which dimension keys this measure supports.
	dims []string
	// dimSource resolves those keys to expressions (historyDims or qaDims —
	// it must match the aliases available in `from`).
	dimSource map[string]dimension
}

type valueCol struct {
	name string // output column name
	expr string // aggregate over the (possibly deduped) fact stream
	// unit is what the number counts. Columns sharing a unit can be plotted on
	// one axis (input and output tokens); columns that don't cannot — words and
	// segments summed into one series is a number of nothing. The chart builder
	// uses it to decide when picking a second value is meaningful.
	unit string
	// average marks a column that is NOT additive. Gap filling writes 0 into an
	// empty bucket, which is the truth for a sum or a count and a lie for a
	// mean: "average edit distance 0" claims effortless edits where there were
	// none at all. Those buckets stay NULL, and the chart shows a break.
	average bool
	// derived marks a column that is the sum of other value columns of the
	// measure (total_tokens = input + output): adding it to them counts twice.
	derived bool
}

// dimension is a whitelisted GROUP BY / filter column.
type dimension struct {
	expr string // selectable expression over the measure's join graph
	// normalizeLang runs langcode.Normalize on the filter value so
	// "DE-de" and "de" hit the same rows.
	normalizeLang bool
	// numeric filters compare as bigint (ids); everything else compares
	// case-insensitively on text.
	numeric bool
}
