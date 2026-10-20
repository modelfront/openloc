package hold_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/hold"
	"openloc/internal/testutil"
)

// raceQuerier runs before() ahead of the first AcquireHold: what a concurrent
// request does between Acquire's checks and its insert.
type raceQuerier struct {
	db.Querier
	before func() error
	done   bool
}

func (r *raceQuerier) AcquireHold(ctx context.Context, arg db.AcquireHoldParams) (db.AcquireHoldRow, error) {
	if !r.done {
		r.done = true
		if err := r.before(); err != nil {
			return db.AcquireHoldRow{}, err
		}
	}
	return r.Querier.AcquireHold(ctx, arg)
}

// The job is deleted after Acquire found the segment but before the hold is
// written: the request is a not found, not a server error.
func TestIntegrationHold_JobDeletedMidAcquireIsNotFound(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	ctx := context.Background()
	q := &raceQuerier{Querier: f.q, before: func() error {
		_, err := f.pool.Exec(ctx, `DELETE FROM jobs WHERE id = $1`, f.job.ID)
		return err
	}}
	_, err := hold.NewService(q, f.pool).Acquire(ctx, f.tenant, f.linguist.ID, f.job.ID, f.t1.ID, "tab-a")
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("err = %v, want not found", err)
	}
}

// An insert Postgres aborted to break a deadlock is tried once more.
func TestIntegrationHold_DeadlockedInsertIsRetried(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	ctx := context.Background()
	logs := testutil.CaptureLogs(t)
	q := &raceQuerier{Querier: f.q, before: func() error {
		return &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
	}}
	got, err := hold.NewService(q, f.pool).Acquire(ctx, f.tenant, f.linguist.ID, f.job.ID, f.t1.ID, "tab-a")
	if err != nil || got.TranslationID != f.t1.ID {
		t.Fatalf("acquire = %+v, %v; want the hold", got, err)
	}
	if n := strings.Count(logs.String(), `"event.action":"hold-deadlock-retry"`); n != 1 {
		t.Errorf("deadlock warnings logged = %d, want 1", n)
	}
}

// Two lost deadlocks in a row end the request with the deadlock as its error.
func TestIntegrationHold_TwoDeadlocksReportTheDeadlock(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	ctx := context.Background()
	q := &alwaysDeadlocked{Querier: f.q}
	_, err := hold.NewService(q, f.pool).Acquire(ctx, f.tenant, f.linguist.ID, f.job.ID, f.t1.ID, "tab-a")
	if !database.IsConflict(err) || q.calls != 2 {
		t.Fatalf("err = %v after %d inserts, want the deadlock after 2", err, q.calls)
	}
}

type alwaysDeadlocked struct {
	db.Querier
	calls int
}

func (a *alwaysDeadlocked) AcquireHold(context.Context, db.AcquireHoldParams) (db.AcquireHoldRow, error) {
	a.calls++
	return db.AcquireHoldRow{}, &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
}
