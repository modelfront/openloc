package testutil

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryLog records every statement a CountingPool sends (plain queries, each
// query of a pgx batch, and COPY) and the rows each returned or wrote. A scale
// test runs the code under test on the pool and asserts that the statement or
// row count stays bounded as the data grows.
type QueryLog struct {
	mu    sync.Mutex
	stmts []*loggedStmt
}

type loggedStmt struct {
	sql  string
	rows int64
}

type stmtKey struct{}

// CountingPool opens a second pool on the test database whose statements land
// in the returned log. SetupTestDB must run first; the pool closes when the
// test ends.
func CountingPool(t testing.TB) (*pgxpool.Pool, *QueryLog) {
	t.Helper()
	if sharedConnStr == "" {
		t.Fatal("CountingPool: call SetupTestDB first")
	}
	cfg, err := pgxpool.ParseConfig(sharedConnStr)
	if err != nil {
		t.Fatalf("parse test database config: %v", err)
	}
	log := &QueryLog{}
	cfg.ConnConfig.Tracer = countingTracer{log: log}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create counting pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, log
}

// Reset forgets every recorded statement, so a test can count only the part
// it measures and not its own setup.
func (l *QueryLog) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stmts = nil
}

// Total is the number of statements sent since the last Reset.
func (l *QueryLog) Total() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.stmts)
}

// Count is the number of statements sent under the sqlc query name (its
// "-- name:" line) since the last Reset.
func (l *QueryLog) Count(name string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.stmts {
		if queryName(s.sql) == name {
			n++
		}
	}
	return n
}

// Rows is the number of rows returned or written by the statements sent under
// the sqlc query name since the last Reset (the command tag's row count).
func (l *QueryLog) Rows(name string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var n int64
	for _, s := range l.stmts {
		if queryName(s.sql) == name {
			n += s.rows
		}
	}
	return n
}

// TotalRows is the number of rows returned or written by every statement
// since the last Reset.
func (l *QueryLog) TotalRows() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var n int64
	for _, s := range l.stmts {
		n += s.rows
	}
	return n
}

// CountPrefix is the number of statements since the last Reset whose SQL,
// trimmed and upper-cased, starts with prefix ("BEGIN", "INSERT INTO foo").
func (l *QueryLog) CountPrefix(prefix string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	prefix = strings.ToUpper(prefix)
	n := 0
	for _, s := range l.stmts {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(s.sql)), prefix) {
			n++
		}
	}
	return n
}

func (l *QueryLog) add(sql string) *loggedStmt {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := &loggedStmt{sql: sql}
	l.stmts = append(l.stmts, st)
	return st
}

func (l *QueryLog) setRows(st *loggedStmt, rows int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st.rows = rows
}

type countingTracer struct{ log *QueryLog }

func (c countingTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, stmtKey{}, c.log.add(data.SQL))
}

func (c countingTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if st, ok := ctx.Value(stmtKey{}).(*loggedStmt); ok {
		c.log.setRows(st, data.CommandTag.RowsAffected())
	}
}

func (countingTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return ctx
}

func (c countingTracer) TraceBatchQuery(_ context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	c.log.setRows(c.log.add(data.SQL), data.CommandTag.RowsAffected())
}

func (countingTracer) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

func (c countingTracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	return context.WithValue(ctx, stmtKey{}, c.log.add("COPY "+data.TableName.Sanitize()))
}

func (c countingTracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	if st, ok := ctx.Value(stmtKey{}).(*loggedStmt); ok {
		c.log.setRows(st, data.CommandTag.RowsAffected())
	}
}
