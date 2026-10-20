package metrics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"openloc/internal/langcode"
)

const maxDims = 3

// compiled is what Run executes. sql is assembled exclusively from registry
// constants; args carries every caller-influenced value.
type compiled struct {
	sql     string
	args    []any
	columns []string // dims then value names, in SELECT order
	def     string
}

// dimSel is one resolved GROUP BY dimension. key doubles as the output
// alias — safe as SQL text because keys only ever come from the whitelist.
type dimSel struct {
	key    string
	expr   string
	isTime bool
}

// compile validates a Request against the whitelist and builds the query.
// tenantID is bound as $1 and comes from auth claims — never from the request.
func compile(tenantID int64, req Request) (*compiled, error) {
	m, ok := measures[req.Measure]
	if !ok {
		return nil, invalidf("unknown measure %q; available: %s", req.Measure, strings.Join(measureNames(), ", "))
	}

	if req.From.IsZero() || req.To.IsZero() {
		return nil, invalidf("time range is required (from and to)")
	}
	if !req.To.After(req.From) {
		return nil, invalidf("'to' must be after 'from'")
	}
	if req.To.Sub(req.From) > MaxRangeDays*24*time.Hour {
		return nil, invalidf("time range exceeds %d days — narrow it", MaxRangeDays)
	}

	if len(req.Dimensions) > maxDims {
		return nil, invalidf("at most %d dimensions per query", maxDims)
	}

	args := []any{tenantID, req.From, req.To}
	bind := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	// Dimensions: resolve each requested key to (constant expr, alias=key).
	var dims []dimSel
	seen := map[string]bool{}
	for _, key := range req.Dimensions {
		if seen[key] {
			return nil, invalidf("dimension %q repeated", key)
		}
		seen[key] = true
		switch {
		case timeGrains[key]:
			// key is one of the three grain constants; it reaches SQL only
			// through this branch, so the date_trunc unit is constant text.
			dims = append(dims, dimSel{key: key, expr: fmt.Sprintf("date_trunc('%s', %s)::date", key, m.timeExpr), isTime: true})
		default:
			d, ok := m.dimSource[key]
			if !ok || !contains(m.dims, key) {
				return nil, invalidf("measure %q has no dimension %q; available: %s, day, week, month",
					req.Measure, key, strings.Join(m.dims, ", "))
			}
			dims = append(dims, dimSel{key: key, expr: d.expr})
		}
	}

	// Filters: keys must be declared (non-time) dimensions; values bind.
	var filterSQL []string
	for _, key := range sortedKeys(req.Filters) {
		val := strings.TrimSpace(req.Filters[key])
		if val == "" {
			return nil, invalidf("filter %q has an empty value", key)
		}
		if len(val) > 200 {
			return nil, invalidf("filter %q value too long", key)
		}
		d, ok := m.dimSource[key]
		if !ok || !contains(m.dims, key) {
			return nil, invalidf("measure %q has no filter %q; filterable: %s",
				req.Measure, key, strings.Join(m.dims, ", "))
		}
		switch {
		case d.numeric:
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, invalidf("filter %q must be an integer id", key)
			}
			filterSQL = append(filterSQL, fmt.Sprintf("%s = %s", d.expr, bind(n)))
		case d.normalizeLang:
			filterSQL = append(filterSQL, fmt.Sprintf("LOWER(%s) = %s", d.expr, bind(langcode.Normalize(val))))
		default:
			filterSQL = append(filterSQL, fmt.Sprintf("LOWER(%s) = LOWER(%s)", d.expr, bind(val)))
		}
	}

	// WHERE shared by both shapes: tenant pin first (the project's, plus the
	// fact table's own tenant column where it has one, which its time index
	// leads), then live projects only, then range, then the measure's fixed
	// predicates, then filters.
	where := []string{"p.tenant_id = $1"}
	if pin, ok := graphTenantPins[m.from]; ok {
		where = append(where, pin+" = $1")
	}
	where = append(where,
		"p.deleted_at IS NULL",
		fmt.Sprintf("%s >= $2", m.timeExpr),
		fmt.Sprintf("%s < $3", m.timeExpr),
	)
	if m.where != "" {
		where = append(where, m.where)
	}
	where = append(where, filterSQL...)

	var b strings.Builder
	var columns []string
	for _, d := range dims {
		columns = append(columns, d.key)
	}
	for _, v := range m.values {
		columns = append(columns, v.name)
	}

	valueParts := make([]string, len(m.values))
	for i, v := range m.values {
		valueParts[i] = fmt.Sprintf("%s AS %s", v.expr, v.name)
	}

	if m.distinctUnit != "" {
		// Two-layer shape: dedupe the fact stream on (unit × dim combo)
		// before aggregating, so repeated events per translation count once
		// per group. Carries haul the fact columns the outer aggregates use.
		inner := []string{fmt.Sprintf("%s AS _unit", m.distinctUnit)}
		inner = append(inner, carriesSorted()...)
		for _, d := range dims {
			inner = append(inner, fmt.Sprintf("%s AS %s", d.expr, quoteIdent(d.key)))
		}
		b.WriteString("SELECT ")
		for _, d := range dims {
			b.WriteString(quoteIdent(d.key) + ", ")
		}
		b.WriteString(strings.Join(valueParts, ", "))
		b.WriteString("\nFROM (\n  SELECT DISTINCT ")
		b.WriteString(strings.Join(inner, ", "))
		b.WriteString("\n  FROM ")
		b.WriteString(m.from)
		b.WriteString("\n  WHERE ")
		b.WriteString(strings.Join(where, " AND "))
		b.WriteString("\n) sub")
		if len(dims) > 0 {
			b.WriteString("\nGROUP BY " + joinDimKeys(dims))
		}
		b.WriteString("\nORDER BY " + orderClause(dims, m.values, true))
	} else {
		b.WriteString("SELECT ")
		for _, d := range dims {
			b.WriteString(fmt.Sprintf("%s AS %s, ", d.expr, quoteIdent(d.key)))
		}
		b.WriteString(strings.Join(valueParts, ", "))
		b.WriteString("\nFROM ")
		b.WriteString(m.from)
		b.WriteString("\nWHERE ")
		b.WriteString(strings.Join(where, " AND "))
		if len(dims) > 0 {
			b.WriteString("\nGROUP BY " + joinDimExprs(dims))
		}
		b.WriteString("\nORDER BY " + orderClause(dims, m.values, false))
	}
	sql := b.String()
	// Categorical overflow: keep the biggest values and aggregate the rest into
	// one row rather than dropping them. Truncation answers "the top 500 and
	// nothing about the rest"; folding answers "the top N, and here is what
	// everything else came to" — which is the question a reader actually has.
	if req.topN > 0 {
		if cat := soleCategorical(dims); cat != "" {
			sql = foldTail(sql, cat, m.values, req.topN)
		}
	}
	// Gap filling: a bucket with no activity is missing from a GROUP BY, which
	// on a line or area chart joins two points across the emptiness as though
	// the middle were interpolated. Only applied when the grain is the ONLY
	// dimension: with a second one, filling means the cross product of buckets ×
	// values, and the client already fills within the series it received.
	if g := soleTimeGrain(dims); g != "" {
		sql = fillGaps(sql, g, m.values, len(args))
		args = append(args, req.From, req.To)
	}
	sql += fmt.Sprintf("\nLIMIT %d", req.budget()+1)

	return &compiled{sql: sql, args: args, columns: columns, def: m.definition}, nil
}

// orderClause: time series read chronologically; categorical results order by
// the first value column descending so "top engines" reads top-first.
func orderClause(dims []dimSel, values []valueCol, byAlias bool) string {
	for _, d := range dims {
		if d.isTime {
			if byAlias {
				return quoteIdent(d.key) + " ASC"
			}
			return d.expr + " ASC"
		}
	}
	return values[0].name + " DESC"
}

func joinDimKeys(dims []dimSel) string {
	keys := make([]string, len(dims))
	for i, d := range dims {
		keys[i] = quoteIdent(d.key)
	}
	return strings.Join(keys, ", ")
}

func joinDimExprs(dims []dimSel) string {
	exprs := make([]string, len(dims))
	for i, d := range dims {
		exprs[i] = d.expr
	}
	return strings.Join(exprs, ", ")
}

func measureNames() []string {
	names := make([]string, 0, len(measures))
	for n := range measures {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// carriesSorted returns "expr AS name" fragments in stable order.
func carriesSorted() []string {
	names := make([]string, 0, len(carries))
	for n := range carries {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%s AS %s", carries[n], n)
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// soleTimeGrain returns the grain when it is the only dimension, else "".
func soleTimeGrain(dims []dimSel) string {
	if len(dims) == 1 && dims[0].isTime {
		return dims[0].key
	}
	return ""
}

// fillGaps wraps a compiled query so every bucket in the range appears, whether
// or not anything happened in it.
//
// generate_series is anchored with the same date_trunc the measure uses, so the
// generated buckets land exactly on the grouped ones — an unanchored series
// would start mid-week and match nothing. The range is [from, to), so the
// series ends at the bucket holding the last instant before `to`.
func fillGaps(inner, grain string, values []valueCol, argc int) string {
	cols := make([]string, len(values))
	for i, v := range values {
		if v.average {
			// Not additive: an empty bucket has no mean, and inventing 0 would
			// read as "no effort" rather than "no data".
			cols[i] = fmt.Sprintf("f.%s AS %s", v.name, v.name)
			continue
		}
		cols[i] = fmt.Sprintf("COALESCE(f.%s, 0) AS %s", v.name, v.name)
	}
	from, to := argc+1, argc+2
	return fmt.Sprintf(`WITH facts AS (
%s
)
SELECT b.bucket::date AS %s, %s
FROM generate_series(
  date_trunc('%s', $%d::timestamptz),
  date_trunc('%s', $%d::timestamptz - interval '1 microsecond'),
  interval '1 %s'
) AS b(bucket)
LEFT JOIN facts f ON f.%s = b.bucket::date
ORDER BY 1 ASC`,
		strings.TrimSuffix(inner, "\n"), quoteIdent(grain), strings.Join(cols, ", "),
		grain, from, grain, to, grain, quoteIdent(grain))
}

// soleCategorical returns the dimension to fold on: the only one, and not a
// time grain. Folding a multi-dimension result would have to choose WHICH
// dimension's tail to collapse, and any choice is a guess about intent.
func soleCategorical(dims []dimSel) string {
	if len(dims) == 1 && !dims[0].isTime {
		return dims[0].key
	}
	return ""
}

// foldTail keeps the n biggest rows and sums the remainder into one OtherBucket.
//
// Non-additive columns come back NULL for that row: the mean of a folded tail
// is not the mean of its parts, and a plausible-looking wrong average is worse
// than an visible absence.
func foldTail(inner, dim string, values []valueCol, n int) string {
	cols := make([]string, len(values))
	for i, v := range values {
		if v.average {
			// MAX over a group of one is that row's value; the Other group has
			// only NULLs, so it stays NULL.
			cols[i] = fmt.Sprintf("MAX(CASE WHEN rn <= %d THEN %s END) AS %s", n, v.name, v.name)
			continue
		}
		cols[i] = fmt.Sprintf("SUM(%s) AS %s", v.name, v.name)
	}
	// Ranked by the first value column, matching the ORDER BY the inner query
	// already used, so "biggest" means the same thing before and after folding.
	return fmt.Sprintf(`WITH ranked AS (
  SELECT *, row_number() OVER (ORDER BY %s DESC) AS rn
  FROM (
%s
  ) inner_facts
)
SELECT CASE WHEN rn <= %d THEN %s ELSE '%s' END AS %s, %s
FROM ranked
GROUP BY 1, CASE WHEN rn <= %d THEN rn ELSE %d END
ORDER BY 2 DESC`,
		values[0].name, inner, n, quoteIdent(dim), OtherBucket, quoteIdent(dim), strings.Join(cols, ", "), n, n+1)
}

// quoteIdent quotes a dimension alias. Some keys are reserved words — unquoted,
// `user` reads as CURRENT_USER, the database role — so every alias the
// compiler writes as an identifier goes through here. Keys come from the
// registry, never from the request, but quoting escapes them anyway.
func quoteIdent(key string) string {
	return `"` + strings.ReplaceAll(key, `"`, `""`) + `"`
}
