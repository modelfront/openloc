// Package testutil provides helpers for integration tests that require a real PostgreSQL database.
// Tests using SetupTestDB are automatically skipped when running with -short flag.
package testutil

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	// pgx stdlib adapter for goose
	_ "github.com/jackc/pgx/v5/stdlib"

	db "openloc/internal/database/sqlcgen"
)

// PostgresImage is the testcontainers Postgres image. It matches the postgres
// image used by the dev and production stacks and CI.
const PostgresImage = "postgres:17.11-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24"

var (
	sharedConnStr string
	containerOnce sync.Once
	containerErr  error
)

// migrationsDir returns the absolute path to the migrations directory.
func migrationsDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "database", "migrations")
}

func init() {
	// Enable ryuk (reaper container) so test postgres containers get cleaned up
	// automatically after tests finish. Without this, containers pile up indefinitely.
	// Ryuk handles multiple parallel test processes fine with recent testcontainers versions.
	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "false")
}

// startContainer starts a shared Postgres testcontainer (called once per process).
func startContainer() {
	ctx := context.Background()

	container, err := postgres.Run(ctx,
		PostgresImage,
		postgres.WithDatabase("openloc_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		// Both waits: under full-gate load the "ready" log can appear before
		// docker exposes the host port mapping, and ConnectionString then fails
		// with `port "5432/tcp" not found`. The port wait blocks until it exists.
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second),
				wait.ForListeningPort("5432/tcp").
					WithStartupTimeout(30*time.Second),
			),
		),
	)
	if err != nil {
		containerErr = fmt.Errorf("failed to start postgres container: %w", err)
		return
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		containerErr = fmt.Errorf("failed to get connection string: %w", err)
		return
	}

	// Run migrations using goose
	sqlDB, err := sql.Open("pgx", connStr)
	if err != nil {
		containerErr = fmt.Errorf("failed to open sql connection for migrations: %w", err)
		return
	}
	defer sqlDB.Close()

	if err := goose.Up(sqlDB, migrationsDir()); err != nil {
		containerErr = fmt.Errorf("failed to run migrations: %w", err)
		return
	}

	sharedConnStr = connStr
}

// SetupTestDB returns a connection pool and querier connected to a real Postgres testcontainer.
// The returned cleanup function truncates all tables to ensure test isolation.
// Automatically calls t.Skip when -short flag is set.
func SetupTestDB(t testing.TB) (*pgxpool.Pool, db.Querier, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test (use -short to skip)")
	}

	containerOnce.Do(startContainer)
	if containerErr != nil {
		t.Fatalf("testcontainer setup failed: %v", containerErr)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, sharedConnStr)
	if err != nil {
		t.Fatalf("failed to create pgx pool: %v", err)
	}

	// Truncate before the test to ensure clean state
	truncateAll(t, pool)

	cleanup := func() {
		truncateAll(t, pool)
		pool.Close()
	}

	return pool, db.New(pool), cleanup
}

// Ctx returns a background context for use in test fixtures.
func Ctx() context.Context {
	return context.Background()
}

// CountRows runs a `SELECT count(*) …` query and returns the count, for
// asserting from the database that a request did or did not write.
func CountRows(t testing.TB, pool *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count rows %q: %v", query, err)
	}
	return n
}

// truncateAll removes all data from application tables.
//
// Each test package gets its own container and its own database (containerOnce
// is per-process, and `go test` gives every package its own process), and no
// DB-backed package calls t.Parallel(). So nothing else is touching these
// tables while this runs. Two properties are still worth keeping:
//
//  1. **List every table explicitly.** CASCADE only reaches tables linked by a
//     foreign key, so a table with no FK path into this list keeps its rows and
//     leaks them into the next test. When a migration adds a table, ADD IT HERE
//     rather than assuming CASCADE finds it. Explicit listing also fixes the
//     lock-acquisition order, which is what makes (2) a safety net rather than
//     a necessity.
//  2. **Retry on deadlock.** TRUNCATE takes ACCESS EXCLUSIVE locks. Deadlock
//     (40P01) is unreachable while each package owns its database, but it is a
//     documented retryable condition and the retry is what keeps this correct
//     if packages ever share one — retrying is the right handling, not a
//     workaround masking a product bug.
func truncateAll(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	const stmt = `
		TRUNCATE
			invites,
			api_tokens,
			mcp_tool_calls,
			translation_memory,
			tm_imports,
			translations,
			segments,
			jobs,
			files,
			mt_router_rules,
			mt_routers,
			settings_tm_assignments,
			settings_terminology_assignments,
			workflow_steps,
			settings_profiles,
			templates,
			tm_collections,
			mt_engines,
			term_bases,
			term_concept,
			term,
			-- AI vendor graph, listed explicitly (see the doc comment).
			-- Children first: rows → systems, tokens → connections → vendors.
			ai_engine_rows,
			ai_engines,
			ai_vendor_tokens,
			ai_connections,
			ai_vendors,
			demo_workspace_items,
			projects,
			users,
			tenants
		CASCADE
	`

	// Bounded retry with a tiny backoff. Attempts are cheap (the tables are
	// empty or near-empty) and a real failure still surfaces as t.Fatalf.
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if _, err = pool.Exec(ctx, stmt); err == nil {
			return
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgerrcodeDeadlockDetected {
			break // not retryable — fail with the original error
		}
		time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
	t.Fatalf("failed to truncate tables: %v", err)
}

// pgerrcodeDeadlockDetected is Postgres SQLSTATE 40P01. Spelled out rather than
// pulling in a dependency for one constant.
const pgerrcodeDeadlockDetected = "40P01"
