package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Exec runs a seeding or setup statement on pool and fails the test on error.
func Exec(t testing.TB, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

// QueryInt64s returns the first column of every row sql returns, such as a
// list of ids.
func QueryInt64s(t testing.TB, pool *pgxpool.Pool, sql string, args ...any) []int64 {
	t.Helper()
	rows, err := pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// LockWaiters counts the sessions of the test database that wait on a lock.
func LockWaiters(t testing.TB, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
