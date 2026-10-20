package metrics

import "strings"

// Join graphs. Frozen constants — the compiler never edits these, only
// appends parameterized predicates. All terminate in projects (alias p) so
// the tenant pin applies structurally.
const (
	// customerJoin reads a project's customer from its Customer label, exposed
	// as alias c. Shared by every graph so one customer filter narrows all of
	// them the same way; it needs alias p.
	customerJoin = `LEFT JOIN label_definitions ld_c ON ld_c.tenant_id = p.tenant_id
                               AND ld_c.system_key = 'customer' AND ld_c.deleted_at IS NULL
LEFT JOIN project_labels pl_c ON pl_c.project_id = p.id AND pl_c.label_definition_id = ld_c.id
LEFT JOIN label_values c ON c.id = pl_c.label_value_id AND c.deleted_at IS NULL`

	// translationToProject walks a translation (alias t) up to its project
	// (alias p), where the compiler pins the tenant.
	translationToProject = `JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
`

	historyGraph = `translation_history th
JOIN translations t ON t.id = th.translation_id
` + translationToProject + `LEFT JOIN users u ON u.id = th.created_by
LEFT JOIN mt_engines me ON me.id = th.mt_engine_id
` + customerJoin + `
LEFT JOIN ai_engines ais ON ais.id = th.ai_engine_id
LEFT JOIN job_step_assignments jsa ON jsa.id = th.job_step_assignment_id
LEFT JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id`

	// Token spend. Terminates in projects like the others, which is why a usage
	// row carries project_id: the compiler pins on `p.tenant_id`, so a call with
	// no project (chat, MT preview) is recorded but not chartable.
	aiUsageGraph = `ai_usage_events e
JOIN projects p ON p.id = e.project_id
` + customerJoin + `
LEFT JOIN ai_engines ais ON ais.id = e.ai_engine_id`

	qaGraph = `qa_results qr
JOIN translations t ON t.id = qr.translation_id
` + translationToProject + customerJoin

	// One row per translation (a segment in one target language), so every
	// segment counts once whatever its history.
	translationGraph = `translations t
` + translationToProject + customerJoin
)

// dimensions shared by measures on the history graph.
// graphTenantPins is the fact table's own tenant column, for the graphs whose
// fact table has one. The compiler pins it next to p.tenant_id, so a time
// range is read through the table's (tenant_id, created_at) index.
var graphTenantPins = map[string]string{
	historyGraph: "th.tenant_id",
}

var historyDims = map[string]dimension{
	"target_language": {expr: "t.target_language", normalizeLang: true},
	"source_language": {expr: "p.source_language", normalizeLang: true},
	"user":            {expr: "u.name"},
	"mt_engine":       {expr: "me.name"},
	"project":         {expr: "p.name"},
	"project_id":      {expr: "p.id", numeric: true},
	// Customer is a built-in label, so its "name" is the label value.
	"customer": {expr: "c.value"},
	"kind":     {expr: "th.kind::text"},
	// Where the text came from before this event: the kind of the translation's
	// last MT, TM or file-import row. Splits auto-approvals into "MT we trusted"
	// and "TM we trusted".
	"segment_type": {expr: "(SELECT h2.kind::text FROM translation_history h2 " +
		"WHERE h2.translation_id = th.translation_id AND h2.kind IN ('mt', 'tm', 'file_import') " +
		"AND h2.id < th.id ORDER BY h2.id DESC LIMIT 1)"},
	// How quality prediction resolved this translation. Four values:
	// approved / ape_approved / ape_needs_review / rejected.
	"predict_outcome": {expr: "t.predict_outcome::text"},
	// AI engines are attributed separately from vendor MT engines: "DeepL" and
	// "GPT-4o behind prompt X" are not comparable units, so they never share a
	// dimension. Keyed `ai_engine` to pair with `mt_engine`.
	"ai_engine":  {expr: "ais.name"},
	"ai_purpose": {expr: "ais.purpose"},
	// The workflow step the event happened in, by name: steps are copied per
	// settings profile, so grouping by name sums "Review" across projects.
	// Human confirms, step handoffs and the AI step's rows carry a step; MT and
	// TM rows do not, and read as empty.
	"workflow_step": {expr: "ws.name"},
}

// dimensions for the token-spend graph. Names match the history ones where they
// mean the same thing, so one dashboard control narrows both.
var aiUsageDims = map[string]dimension{
	"ai_engine":  {expr: "e.ai_engine_name"},
	"ai_purpose": {expr: "e.purpose"},
	"provider":   {expr: "e.provider"},
	"model":      {expr: "e.model"},
	"project":    {expr: "p.name"},
	"project_id": {expr: "p.id", numeric: true},
	"customer":   {expr: "c.value"},
}

var translationDims = map[string]dimension{
	"target_language": {expr: "t.target_language", normalizeLang: true},
	"source_language": {expr: "p.source_language", normalizeLang: true},
	"project":         {expr: "p.name"},
	"project_id":      {expr: "p.id", numeric: true},
	"customer":        {expr: "c.value"},
	// How the target was first filled: tm, mt, ape or file_import. Empty when
	// nothing automatic filled it — a person translated it, or nobody has yet.
	// A later human edit keeps it.
	"origin": {expr: "t.origin::text"},
	// Where the translation stands now: needs_review, approved, auto_approved…
	"status": {expr: "t.status::text"},
}

var qaDims = map[string]dimension{
	"check_type":      {expr: "qr.check_type"},
	"severity":        {expr: "qr.severity"},
	"target_language": {expr: "t.target_language", normalizeLang: true},
	"source_language": {expr: "p.source_language", normalizeLang: true},
	"project":         {expr: "p.name"},
	"project_id":      {expr: "p.id", numeric: true},
	// Customer is a built-in label, so its "name" is the label value.
	"customer": {expr: "c.value"},
}

// originKinds are the history kinds that change a translation's text, which is
// what gives it an origin. The other kinds are status events (statusKinds).
// Together they are every translation_history kind — pinned by an integration
// test, so a new kind has to be placed in one of them.
var (
	originKinds = []string{"tm", "mt", "ape", "human", "propagation", "file_import"}
	statusKinds = []string{"cleared", "approved", "auto_approved", "unconfirmed"}
)

// sqlList renders constant identifiers as a SQL IN list: 'a', 'b'.
func sqlList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + v + "'"
	}
	return strings.Join(quoted, ", ")
}

// timeGrains are dimension keys available on every measure; the compiler
// expands them over the measure's timeExpr. Range filters come from
// Request.From/To, never from these.
var timeGrains = map[string]bool{"day": true, "week": true, "month": true}

// measures is THE whitelist. Adding an entry means adding: a definition the
// model can quote, a frozen join graph, and a cross-tenant integration test
// (see integration_test.go — a measure without one doesn't ship).
var measures = map[string]measure{
	"words_processed": {
		definition: "Source words of segments a human confirmed or edited in the period " +
			"(translation_history kind='human' or 'approved' — a confirm that changed the " +
			"text, or one that approved it unchanged). Each translation counts once per " +
			"dimension combination, so repeated saves of the same segment don't inflate " +
			"the number. Returns words, segments and source characters.",
		from:         historyGraph,
		where:        "th.kind IN ('human', 'approved')",
		timeExpr:     "th.created_at",
		distinctUnit: "t.id",
		values: []valueCol{
			{name: "words", expr: "COALESCE(SUM(word_count), 0)", unit: "words"},
			{name: "segments", expr: "COUNT(*)", unit: "segments"},
			{name: "characters", expr: "COALESCE(SUM(char_count), 0)", unit: "characters"},
		},
		dims:      []string{"target_language", "source_language", "user", "project", "project_id", "customer", "workflow_step"},
		dimSource: historyDims,
	},

	"source_volume": {
		definition: "Source volume that entered translation in the period: every translation " +
			"(a segment in one target language) once, dated when it was created, with its " +
			"source words. Not an activity count — a segment's history does not add to it. " +
			"Group by 'origin' for how it was first filled (tm, mt, ape, file_import; empty = " +
			"nothing automatic) and by 'status' for where it stands now. " +
			"Returns segments, words and source characters.",
		from:     translationGraph,
		timeExpr: "t.created_at",
		values: []valueCol{
			{name: "words", expr: "COALESCE(SUM(s.word_count), 0)", unit: "words"},
			{name: "segments", expr: "COUNT(*)", unit: "segments"},
			{name: "characters", expr: "COALESCE(SUM(length(s.source_text)), 0)", unit: "characters"},
		},
		dims:      []string{"origin", "status", "target_language", "source_language", "project", "project_id", "customer"},
		dimSource: translationDims,
	},

	"segments_by_origin": {
		definition: "Translations touched in the period, split by history event kind " +
			"(tm, mt, ape, human, propagation, file_import). Each translation counts once per " +
			"kind per dimension combination. Group by 'kind' to get the origin split. " +
			"Status events (approved, auto_approved, unconfirmed) are excluded — they change " +
			"no text, so they have no origin. Returns segments, words and source characters.",
		from: historyGraph,
		// Listed, not excluded: an IN list can use the (kind, created_at) index,
		// where NOT IN reads every history row in the range.
		where:        "th.kind IN (" + sqlList(originKinds) + ")",
		timeExpr:     "th.created_at",
		distinctUnit: "t.id",
		values: []valueCol{
			{name: "segments", expr: "COUNT(*)", unit: "segments"},
			{name: "words", expr: "COALESCE(SUM(word_count), 0)", unit: "words"},
			{name: "characters", expr: "COALESCE(SUM(char_count), 0)", unit: "characters"},
		},
		dims:      []string{"kind", "mt_engine", "target_language", "source_language", "user", "project", "project_id", "customer", "workflow_step"},
		dimSource: historyDims,
	},

	"mt_engine_usage": {
		definition: "Segments machine-translated in the period and their source words, by MT " +
			"engine (translation_history kind='mt' with an engine attributed). Each translation " +
			"counts once per dimension combination even if retranslated. " +
			"Returns segments, words and source characters.",
		from:         historyGraph,
		where:        "th.kind = 'mt' AND th.mt_engine_id IS NOT NULL",
		timeExpr:     "th.created_at",
		distinctUnit: "t.id",
		values: []valueCol{
			{name: "segments", expr: "COUNT(*)", unit: "segments"},
			{name: "words", expr: "COALESCE(SUM(word_count), 0)", unit: "words"},
			{name: "characters", expr: "COALESCE(SUM(char_count), 0)", unit: "characters"},
		},
		dims:      []string{"mt_engine", "target_language", "source_language", "user", "project", "project_id", "customer"},
		dimSource: historyDims,
	},

	"ai_engine_usage": {
		definition: "Segments sent to an AI engine in the period, and their source words, by AI " +
			"engine: translated by AI machine translation, or reviewed by quality prediction and " +
			"post-editing, whether approved as they were or post-edited (translation_history with " +
			"an AI engine attributed). A segment the engine neither approved nor post-edited leaves " +
			"no record and is not counted, so a quality-prediction-only engine counts only its " +
			"approvals. Group by 'ai_purpose' to split AI machine translation from QE/APE. " +
			"Vendor MT engines (DeepL, Google) are NOT included — see mt_engine_usage. " +
			"Returns segments, words and source characters.",
		from: historyGraph,
		// Every row an AI engine wrote: mt (AI MT), ape (post-edited) and
		// auto_approved (approved as it was). A rejection writes none.
		where:        "th.ai_engine_id IS NOT NULL",
		timeExpr:     "th.created_at",
		distinctUnit: "t.id",
		values: []valueCol{
			{name: "segments", expr: "COUNT(*)", unit: "segments"},
			{name: "words", expr: "COALESCE(SUM(word_count), 0)", unit: "words"},
			{name: "characters", expr: "COALESCE(SUM(char_count), 0)", unit: "characters"},
		},
		dims:      []string{"ai_engine", "ai_purpose", "target_language", "source_language", "user", "project", "project_id", "customer", "workflow_step"},
		dimSource: historyDims,
	},

	"ai_token_usage": {
		definition: "Tokens spent on AI provider calls in the period, by AI engine, provider or " +
			"model (ai_usage_events). One row per API call, so this counts API COST rather than " +
			"volume — a thousand words may be one call or thirty. Group by 'ai_purpose' to split " +
			"machine translation from post-editing. Calls with no project (chat, previews) are " +
			"recorded but not included here. Returns input, output and total tokens, and calls.",
		from:     aiUsageGraph,
		timeExpr: "e.created_at",
		values: []valueCol{
			// Cast back to bigint: SUM over a bigint column yields NUMERIC, which
			// would reach the client as a decimal string while every other
			// measure returns a plain integer. Token counts have no fractions.
			{name: "input_tokens", expr: "COALESCE(SUM(e.input_tokens), 0)::bigint", unit: "tokens"},
			{name: "output_tokens", expr: "COALESCE(SUM(e.output_tokens), 0)::bigint", unit: "tokens"},
			{name: "total_tokens", expr: "COALESCE(SUM(e.input_tokens + e.output_tokens), 0)::bigint", unit: "tokens", derived: true},
			{name: "calls", expr: "COUNT(*)", unit: "calls"},
		},
		dims:      []string{"ai_engine", "ai_purpose", "provider", "model", "project", "project_id", "customer"},
		dimSource: aiUsageDims,
	},

	"auto_approved": {
		definition: "Source words quality prediction cleared WITHOUT a human, dated when it " +
			"cleared them: the approval events — the MT/TM accepted as-is ('approved'), or " +
			"post-editing fixed it and the fix was accepted ('ape_approved'). Group by " +
			"'segment_type' to split MT from TM, or by 'predict_outcome' to split accepted-as-is " +
			"from APE-corrected. Returns words, segments and source characters.",
		from: historyGraph,
		// Only the approval events count, so a later edit neither re-dates the
		// approval nor adds a 'human' bucket. ape_needs_review and rejected are
		// excluded: APE produced something, but a person still had to look.
		where: "t.predict_outcome IN ('approved', 'ape_approved') AND (th.kind = 'auto_approved' " +
			"OR (th.kind = 'ape' AND th.details->>'predict_outcome' = 'ape_approved'))",
		timeExpr:     "th.created_at",
		distinctUnit: "t.id",
		values: []valueCol{
			{name: "words", expr: "COALESCE(SUM(word_count), 0)", unit: "words"},
			{name: "segments", expr: "COUNT(*)", unit: "segments"},
			{name: "characters", expr: "COALESCE(SUM(char_count), 0)", unit: "characters"},
		},
		dims:      []string{"segment_type", "kind", "predict_outcome", "ai_engine", "mt_engine", "target_language", "source_language", "user", "project", "project_id", "customer", "workflow_step"},
		dimSource: historyDims,
	},

	"editing_time": {
		definition: "Minutes of active human editing recorded on save events in the period " +
			"(sum of per-event editing time; automation events carry none). 'events' is the " +
			"number of timed saves.",
		from:     historyGraph,
		where:    "th.editing_time_ms IS NOT NULL",
		timeExpr: "th.created_at",
		values: []valueCol{
			{name: "minutes", expr: "ROUND(COALESCE(SUM(th.editing_time_ms), 0) / 60000.0, 1)::float8", unit: "minutes"},
			{name: "events", expr: "COUNT(*)", unit: "events"},
		},
		dims:      []string{"user", "target_language", "source_language", "project", "project_id", "customer", "workflow_step"},
		dimSource: historyDims,
	},

	"post_edit_effort": {
		definition: "Average character edit distance of human edits in the period (proxy for " +
			"post-editing effort; higher = more rewriting), plus the number of edit events.",
		from:     historyGraph,
		where:    "th.kind = 'human' AND th.edit_distance IS NOT NULL",
		timeExpr: "th.created_at",
		values: []valueCol{
			{name: "avg_edit_distance", expr: "ROUND(AVG(th.edit_distance), 1)::float8", average: true, unit: "edit distance"},
			{name: "events", expr: "COUNT(*)", unit: "events"},
		},
		dims:      []string{"user", "target_language", "source_language", "project", "project_id", "customer", "workflow_step"},
		dimSource: historyDims,
	},

	"qa_issues": {
		definition: "Open QA findings, by check type and severity, dated by the QA run that last " +
			"found them. A segment holds one finding per check: a re-check replaces it, and a " +
			"finding that stops firing is removed. Ignored findings are not counted.",
		from: qaGraph,
		// Ignored findings read as "no issue" everywhere else (job QA counts,
		// analytics, the editor).
		where:    "NOT qr.ignored",
		timeExpr: "qr.created_at",
		values: []valueCol{
			{name: "issues", expr: "COUNT(*)", unit: "issues"},
		},
		dims:      []string{"check_type", "severity", "target_language", "source_language", "project", "project_id", "customer"},
		dimSource: qaDims,
	},
}

// carries maps the fact columns a two-layer (distinctUnit) measure needs to
// haul from the inner dedup select into the outer aggregate. The outer value
// exprs reference these by alias (e.g. SUM(word_count)); single-layer
// measures reference table aliases directly.
var carries = map[string]string{
	"word_count": "s.word_count",
	// Source characters, counted the same way every measure counts words: off
	// the segment, deduped per translation. Cheap enough to carry on every
	// two-layer measure rather than gate it per measure.
	"char_count": "length(s.source_text)",
}

// Definitions returns measure name → human definition, for the tool
// description. Sorted iteration is the caller's job if it needs stable order.
func Definitions() map[string]string {
	out := make(map[string]string, len(measures))
	for name, m := range measures {
		out[name] = m.definition
	}
	return out
}

// IsTimeGrain reports whether key is one of the time grains every measure
// accepts as a dimension. Grains are never valid as FILTERS — they have no
// entry in a measure's dimSource.
func IsTimeGrain(key string) bool { return timeGrains[key] }

// DimensionsFor returns the dimension keys (including time grains) a measure
// supports, or nil if the measure is unknown.
func DimensionsFor(name string) []string {
	m, ok := measures[name]
	if !ok {
		return nil
	}
	out := append([]string{}, m.dims...)
	out = append(out, "day", "week", "month")
	return out
}

// ValueColumn is one output column as a chart builder needs it: its name and
// what it counts.
type ValueColumn struct {
	Name string `json:"name"`
	// Unit is what the number counts. Two columns may share one axis only when
	// their units match.
	Unit string `json:"unit"`
	// Average marks a non-additive column — never gap-filled, never folded.
	Average bool `json:"average,omitempty"`
}

// ValueColumnsWithUnits lists a measure's output columns with their units, in
// declaration order.
func ValueColumnsWithUnits(name string) []ValueColumn {
	m, ok := measures[name]
	if !ok {
		return nil
	}
	out := make([]ValueColumn, 0, len(m.values))
	for _, v := range m.values {
		out = append(out, ValueColumn{Name: v.name, Unit: v.unit, Average: v.average})
	}
	return out
}

// CategoricalDimensionsFor is DimensionsFor without the time grains: the
// dimensions that name a THING rather than a bucket of time. A picker offering
// "split by week" alongside a weekly axis is offering nonsense.
func CategoricalDimensionsFor(name string) []string {
	m, ok := measures[name]
	if !ok {
		return nil
	}
	return append([]string{}, m.dims...)
}

// ValueColumnsFor lists the value columns a measure returns, in the order it
// declares them. Callers use it to decide whether a control is meaningful: a
// unit dropdown only makes sense where the measure returns more than one of
// words / segments / characters.
func ValueColumnsFor(name string) []string {
	m, ok := measures[name]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m.values))
	for _, v := range m.values {
		out = append(out, v.name)
	}
	return out
}
