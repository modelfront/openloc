package hold_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"openloc/internal/hold"
)

// Two users take the same free segment at the same moment: exactly one gets
// it and the other is told who did, every round.
func TestIntegrationHold_ConcurrentAcquireHasOneWinner(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	ctx := context.Background()
	svc := hold.NewService(f.q, f.pool)

	for round := 0; round < 20; round++ {
		if _, err := f.pool.Exec(ctx, `DELETE FROM translation_holds`); err != nil {
			t.Fatal(err)
		}
		users := []int64{f.linguist.ID, f.pm.ID}
		errs := make([]error, len(users))
		var start, done sync.WaitGroup
		start.Add(1)
		for i, uid := range users {
			done.Add(1)
			go func(i int, uid int64) {
				defer done.Done()
				start.Wait()
				_, errs[i] = svc.Acquire(ctx, f.tenant, uid, f.job.ID, f.t1.ID, fmt.Sprintf("tab-%d", uid))
			}(i, uid)
		}
		start.Done()
		done.Wait()

		winners, refused := 0, 0
		for i, err := range errs {
			var held *hold.HeldError
			switch {
			case err == nil:
				winners++
			case errors.As(err, &held):
				refused++
				if held.HolderID == users[i] {
					t.Fatalf("round %d: user %d was refused by their own hold", round, users[i])
				}
			default:
				t.Fatalf("round %d: unexpected error: %v", round, err)
			}
		}
		if winners != 1 || refused != 1 {
			t.Fatalf("round %d: %d winners and %d refusals, want 1 and 1", round, winners, refused)
		}
	}
}

// The sweep deletes holds expired more than a minute ago and leaves live ones.
func TestIntegrationHold_SweepDeletesOnlyExpired(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	ctx := context.Background()
	svc := hold.NewService(f.q, f.pool)

	if _, err := svc.Acquire(ctx, f.tenant, f.linguist.ID, f.job.ID, f.t1.ID, "tab-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Acquire(ctx, f.tenant, f.pm.ID, f.job.ID, f.t2.ID, "tab-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx,
		`UPDATE translation_holds SET expires_at = now() - interval '2 minutes' WHERE translation_id = $1`, f.t2.ID); err != nil {
		t.Fatal(err)
	}
	if err := hold.NewSweeper(f.q).HandleSweep(ctx, hold.NewSweepTask()); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	rows, err := f.pool.Query(ctx, `SELECT translation_id FROM translation_holds ORDER BY translation_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 1 || ids[0] != f.t1.ID {
		t.Fatalf("holds after the sweep = %v, want only the live one (%d)", ids, f.t1.ID)
	}
}
