package testutil

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FailingPool opens a second pool on the test database that fails every query
// sqlc generated under one of names (its "-- name:" line) with "context
// canceled", and runs every other statement normally. A test builds a router
// on it to break one read of a request and then checks what the request left
// behind. SetupTestDB must run first; the pool closes when the test ends.
func FailingPool(t testing.TB, names ...string) *pgxpool.Pool {
	t.Helper()
	return failingPool(t, false, names)
}

// FailingPoolOutsideTx is FailingPool that fails the named queries only when
// they run outside a transaction: a read the request makes after its commit
// fails, while the same read inside the write's transaction runs.
func FailingPoolOutsideTx(t testing.TB, names ...string) *pgxpool.Pool {
	t.Helper()
	return failingPool(t, true, names)
}

func failingPool(t testing.TB, outsideTxOnly bool, names []string) *pgxpool.Pool {
	t.Helper()
	if sharedConnStr == "" {
		t.Fatal("FailingPool: call SetupTestDB first")
	}
	cfg, err := pgxpool.ParseConfig(sharedConnStr)
	if err != nil {
		t.Fatalf("parse test database config: %v", err)
	}
	tracer := failingTracer{names: make(map[string]bool, len(names)), outsideTxOnly: outsideTxOnly}
	for _, n := range names {
		tracer.names[n] = true
	}
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create failing pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// failingTracer fails the queries whose sqlc name is in names; with
// outsideTxOnly, only while the connection is not in a transaction.
type failingTracer struct {
	names         map[string]bool
	outsideTxOnly bool
}

// TraceQueryStart hands a listed query an already-cancelled context. pgx checks
// the context before it sends anything, so the query fails and the connection,
// and any transaction on it, stays usable.
func (f failingTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !f.names[queryName(data.SQL)] || (f.outsideTxOnly && conn.PgConn().TxStatus() != 'I') {
		return ctx
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	return ctx
}

func (failingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// queryName reads the name off a sqlc query: "-- name: GetConcept :one\n…"
// gives "GetConcept". Statements sqlc did not generate give "".
func queryName(sql string) string {
	rest, ok := strings.CutPrefix(sql, "-- name: ")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, " ")
	return name
}
