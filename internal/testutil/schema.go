package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UnindexedForeignKeys lists the foreign-key columns of table that no valid
// index leads. Deleting a parent row looks up every row that points at it, so
// a foreign key without one scans the whole table on every parent delete. A
// partial index counts only when its predicate is "<column> IS NOT NULL",
// which every such lookup satisfies.
func UnindexedForeignKeys(t testing.TB, pool *pgxpool.Pool, table string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT a.attname
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.conrelid = $1::regclass
		  AND c.contype = 'f'
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_index i
		      WHERE i.indrelid = c.conrelid
		        AND i.indisvalid
		        AND (i.indpred IS NULL
		             OR pg_get_expr(i.indpred, i.indrelid) = '(' || quote_ident(a.attname) || ' IS NOT NULL)')
		        AND i.indkey[0] = c.conkey[1]
		  )
		ORDER BY a.attname`, table)
	if err != nil {
		t.Fatalf("query foreign keys of %s: %v", table, err)
	}
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scan: %v", err)
		}
		missing = append(missing, col)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return missing
}
