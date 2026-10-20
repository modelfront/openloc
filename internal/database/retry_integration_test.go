package database_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationRetryConflicts_RetriedTransactionWritesOnce pins that a
// transaction run again after a deadlock leaves one set of effects: the
// failed run's insert rolls back with it.
func TestIntegrationRetryConflicts_RetriedTransactionWritesOnce(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	runs := 0
	err := database.RetryConflicts(ctx, "test-retry", func() error {
		return database.InTx(ctx, pool, func(_ pgx.Tx, q db.Querier) error {
			runs++
			if _, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Retry Corp", Slug: "retry-corp"}); err != nil {
				return err
			}
			if runs == 1 {
				return &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("RetryConflicts: %v", err)
	}
	if runs != 2 {
		t.Errorf("runs: got %d, want 2", runs)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE slug = 'retry-corp'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("tenants written: got %d, want 1", n)
	}
}

func TestRetryConflicts_StopsOnOtherErrorsAndAfterThreeTries(t *testing.T) {
	ctx := context.Background()
	other := errors.New("boom")
	runs := 0
	if err := database.RetryConflicts(ctx, "test-retry", func() error { runs++; return other }); !errors.Is(err, other) || runs != 1 {
		t.Errorf("other error: got %v after %d runs, want boom after 1", err, runs)
	}
	runs = 0
	conflict := &pgconn.PgError{Code: "40001"}
	if err := database.RetryConflicts(ctx, "test-retry", func() error { runs++; return conflict }); !database.IsConflict(err) || runs != 3 {
		t.Errorf("conflict: got %v after %d runs, want the conflict after 3", err, runs)
	}
}
