package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TableRowsRead returns the rows of table that the connection of tx has read
// by sequential scans and index fetches and not yet reported. The figure can
// include earlier work on the same connection, so a scale test reads it
// before and after the query under test and asserts on the difference.
func TableRowsRead(t testing.TB, tx pgx.Tx, table string) int64 {
	t.Helper()
	var n int64
	if err := tx.QueryRow(context.Background(), `
		SELECT COALESCE(sum(COALESCE(seq_tup_read, 0) + COALESCE(idx_tup_fetch, 0)), 0)::bigint
		FROM pg_stat_xact_user_tables WHERE relname = $1`, table).Scan(&n); err != nil {
		t.Fatalf("read rows read of %s: %v", table, err)
	}
	return n
}
