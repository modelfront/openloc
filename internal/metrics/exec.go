package metrics

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/logging"
)

// queryTimeout bounds a single metric query. Aggregates over indexed
// tenant-scoped joins should run in milliseconds; hitting this means a
// degenerate plan, and the chat turn should fail fast rather than hang.
const queryTimeout = 5 * time.Second

// Run compiles and executes a metrics request for one tenant.
//
// When the result would exceed the caller's row budget AND the request has a
// time grain, the grain is WIDENED and the query re-run: 366 daily buckets
// across 3 engines is 1098 rows, the same question weekly is 159. Re-running at
// a wider grain is correct by construction — SUM and AVG over a week are
// computed from that week's rows — where folding rows afterwards would have to
// know which value columns are additive, and would be wrong for averages.
//
// Widening costs a second round trip only when the first attempt overflows. It
// is a retry rather than a calculation because the row count depends on the
// CARDINALITY of the other dimensions (how many engines, customers), which is
// not knowable without asking.
func Run(ctx context.Context, pool *pgxpool.Pool, tenantID int64, req Request) (*Result, error) {
	grains := widerGrains(req)
	for i, g := range grains {
		attempt := req
		if g != "" {
			attempt = withGrain(req, g)
		}
		res, err := runOnce(ctx, pool, tenantID, attempt)
		if err != nil {
			return nil, err
		}
		res.Grain = g
		if len(res.Rows) <= req.budget() {
			return res, nil
		}
		if i < len(grains)-1 {
			continue // a wider bucket is still available
		}
		// Out of grains. A single categorical dimension can still be folded:
		// keep the biggest values and aggregate the rest into one Other row, so
		// the total survives instead of the tail being dropped.
		if folded, ok := foldCategorical(ctx, pool, tenantID, attempt); ok {
			folded.Grain = g
			return folded, nil
		}
		res.Rows = res.Rows[:req.budget()]
		res.Truncated = true
		return res, nil
	}
	// Unreachable: widerGrains always yields at least the request as given.
	return runOnce(ctx, pool, tenantID, req)
}

// foldCategorical re-runs the query keeping the budget's worth of biggest
// values plus one aggregated Other. Reports false when the shape cannot be
// folded — more than one dimension, so there is no single tail to collapse.
func foldCategorical(ctx context.Context, pool *pgxpool.Pool, tenantID int64, req Request) (*Result, bool) {
	if len(req.Dimensions) != 1 || timeGrains[req.Dimensions[0]] {
		return nil, false
	}
	req.topN = req.budget() - 1 // room for the Other row
	res, err := runOnce(ctx, pool, tenantID, req)
	if err != nil {
		// Falling back to truncation is right, but silently is not: a fold that
		// never works would look like a design choice rather than a broken query.
		logging.Warn(ctx, err, "folding a categorical result failed; truncating instead", "metrics-fold-failed")
		return nil, false
	}
	res.Folded = true
	return res, true
}

// widerGrains lists the grains to try, narrowest first. A request with no time
// dimension has nothing to widen, so it yields a single no-op attempt.
func widerGrains(req Request) []string {
	order := []string{"day", "week", "month"}
	for _, d := range req.Dimensions {
		if !timeGrains[d] {
			continue
		}
		for i, g := range order {
			if g == d {
				return order[i:]
			}
		}
	}
	return []string{""}
}

// withGrain swaps the request's time dimension. The slice is cloned: callers
// keep their Request, and the dashboard reuses one across cards.
func withGrain(req Request, grain string) Request {
	dims := make([]string, len(req.Dimensions))
	copy(dims, req.Dimensions)
	for i, d := range dims {
		if timeGrains[d] {
			dims[i] = grain
			break
		}
	}
	req.Dimensions = dims
	return req
}

func runOnce(ctx context.Context, pool *pgxpool.Pool, tenantID int64, req Request) (*Result, error) {
	c, err := compile(tenantID, req)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	rows, err := pool.Query(ctx, c.sql, c.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := &Result{
		Measure:    req.Measure,
		Definition: c.def,
		Columns:    c.columns,
		Rows:       []map[string]any{},
		Units:      make(map[string]string, len(measures[req.Measure].values)),
	}
	isValue := make(map[string]bool, len(measures[req.Measure].values))
	for _, v := range measures[req.Measure].values {
		isValue[v.name] = true
		res.Units[v.name] = v.unit
		if v.average {
			res.Averages = append(res.Averages, v.name)
		}
		if v.derived {
			res.Derived = append(res.Derived, v.name)
		}
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(c.columns))
		for i, col := range c.columns {
			row[col] = normalizeValue(vals[i], isValue[col])
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return res, nil
}

// normalizeValue makes rows JSON-friendly: dates become "YYYY-MM-DD" (time
// grains are date_trunc'd, so the clock part is noise), and a NULL DIMENSION
// (e.g. a customer-less project) becomes "" so charts get a stable key.
//
// A NULL VALUE stays null. The only NULL a value column produces is an average
// over an empty bucket, and that is "no value", not an empty label: a chart has
// to draw a break there, never a point.
func normalizeValue(v any, isValue bool) any {
	if v == nil && isValue {
		return nil
	}
	switch t := v.(type) {
	case pgtype.Numeric:
		// SUM over a bigint column yields NUMERIC, so a folded or cast-free
		// aggregate arrives as a decimal the client would render as a string.
		// Token counts and segment counts have no fractional part.
		f, err := t.Float64Value()
		if err != nil || !f.Valid {
			return nil
		}
		return int64(f.Float64)
	case time.Time:
		return t.Format("2006-01-02")
	case nil:
		return ""
	default:
		return v
	}
}

// Validate dry-compiles a request without executing it. create_chart uses it
// so a bad definition fails at creation time (model-visible, correctable),
// not at first dashboard render.
func Validate(req Request) error {
	_, err := compile(1, req)
	return err
}

// RangeFromLastDays resolves a stored relative window into [from, to).
// Dashboards store RELATIVE ranges ("last 60 days") so charts stay live;
// resolving happens at render time, here, never at creation.
func RangeFromLastDays(days int, now time.Time) (from, to time.Time, err error) {
	if days < 1 || days > 365 {
		return time.Time{}, time.Time{}, invalidf("last_days must be 1-365")
	}
	// End of today (exclusive) so today's events are always included.
	to = now.Truncate(24*time.Hour).AddDate(0, 0, 1)
	return to.AddDate(0, 0, -days), to, nil
}
