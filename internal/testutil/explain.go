package testutil

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GeneratedSQL returns the SQL sqlc generated for the named query ("-- name:"
// line), read from the generated package, so a plan test explains the exact
// statement the code sends.
func GeneratedSQL(t testing.TB, name string) string {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(filename), "..", "database", "sqlcgen")
	files, err := filepath.Glob(filepath.Join(dir, "*.sql.go"))
	if err != nil {
		t.Fatalf("list generated files: %v", err)
	}
	marker := "`-- name: " + name + " "
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		_, rest, ok := strings.Cut(string(src), marker)
		if !ok {
			continue
		}
		body, _, ok := strings.Cut(rest, "`")
		if !ok {
			t.Fatalf("unterminated SQL for %s", name)
		}
		return "-- name: " + name + " " + body
	}
	t.Fatalf("no generated SQL named %s", name)
	return ""
}

// PlanNode is one node of an EXPLAIN (ANALYZE, FORMAT JSON) plan.
type PlanNode struct {
	RelationName string  `json:"Relation Name"`
	ActualRows   float64 `json:"Actual Rows"`
	ActualLoops  float64 `json:"Actual Loops"`
	// Rows a scan read and then dropped, per loop.
	RemovedByFilter  float64    `json:"Rows Removed by Filter"`
	RemovedByRecheck float64    `json:"Rows Removed by Index Recheck"`
	Plans            []PlanNode `json:"Plans"`
}

// RowQuerier is a pool or a transaction: what ExplainAnalyze runs on.
type RowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ExplainAnalyze runs sql with args under EXPLAIN (ANALYZE, FORMAT JSON) and
// returns the root plan node. Run it in a transaction that is rolled back to
// explain a statement that writes.
func ExplainAnalyze(t testing.TB, q RowQuerier, sql string, args ...any) PlanNode {
	t.Helper()
	var raw []byte
	if err := q.QueryRow(context.Background(), "EXPLAIN (ANALYZE, FORMAT JSON) "+sql, args...).Scan(&raw); err != nil {
		t.Fatalf("explain analyze: %v", err)
	}
	var out []struct {
		Plan PlanNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out) == 0 {
		t.Fatalf("parse explain output: %v", err)
	}
	return out[0].Plan
}

// RowsRead sums the rows every scan of relation produced (rows × loops) in
// the plan: how much of that table the statement actually read.
func (n PlanNode) RowsRead(relation string) float64 {
	total := 0.0
	if n.RelationName == relation {
		total += n.ActualRows * n.ActualLoops
	}
	for _, c := range n.Plans {
		total += c.RowsRead(relation)
	}
	return total
}

// RowsScanned is RowsRead plus the rows each scan of relation read and then
// dropped by a filter or an index recheck: everything the statement touched.
func (n PlanNode) RowsScanned(relation string) float64 {
	total := 0.0
	if n.RelationName == relation {
		total += (n.ActualRows + n.RemovedByFilter + n.RemovedByRecheck) * n.ActualLoops
	}
	for _, c := range n.Plans {
		total += c.RowsScanned(relation)
	}
	return total
}

// Explain returns the text plan of sql run with args, one line per plan row.
func Explain(t testing.TB, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), "EXPLAIN "+sql, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows: %v", err)
	}
	return b.String()
}
