package tenantdelete_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/tenantdelete"
	"openloc/internal/testutil"
)

// Only one delete of an organization runs at a time: while one holds the
// organization's delete lock, a second returns at once and deletes nothing,
// so two deletes never race over the same files.
func TestIntegrationDeleteTenant_OneDeleteAtATime(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenantID, _ := seedOrg(t, pool, q, "closing", 2, 100, 10)
	if _, err := q.MarkTenantDeleting(ctx, tenantID); err != nil {
		t.Fatal(err)
	}
	before := orgRows(t, pool, tenantID)

	// The running delete's lock, held on a connection of its own.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended('tenant-delete:' || $1::bigint, 0))`, tenantID).Scan(&locked); err != nil || !locked {
		t.Fatalf("take the delete lock: %v %v", locked, err)
	}

	if err := tenantdelete.New(pool, nil).Delete(ctx, tenantID); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if after := orgRows(t, pool, tenantID); after != before {
		t.Errorf("a second delete removed %d rows while the first held the lock", before-after)
	}

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended('tenant-delete:' || $1::bigint, 0))`, tenantID); err != nil {
		t.Fatal(err)
	}
	if err := tenantdelete.New(pool, nil).Delete(ctx, tenantID); err != nil {
		t.Fatalf("Delete after the lock is free: %v", err)
	}
	if left := orgRows(t, pool, tenantID); left != 0 {
		t.Errorf("%d rows left once the lock was free", left)
	}
}

// A delete needs one connection, the one holding its lock: on a pool of one
// connection it still finishes, so deletes running at once never wait on
// each other for connections.
func TestIntegrationDeleteTenant_RunsOnOneConnection(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenantID, _ := seedOrg(t, pool, q, "closing", 2, 10, 10)
	if _, err := q.MarkTenantDeleting(context.Background(), tenantID); err != nil {
		t.Fatal(err)
	}
	cfg := pool.Config()
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tenantdelete.New(one, nil).Delete(ctx, tenantID); err != nil {
		t.Fatalf("Delete on a one-connection pool: %v", err)
	}
	if left := orgRows(t, pool, tenantID); left != 0 {
		t.Errorf("%d rows of the deleted organization left", left)
	}
}
