package translationmemory_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationConcurrentTMEntryPatch_NoPoolDeadlock pins that 3
// concurrent stale-baseline PATCHes on a MaxConns=2 pool all return
// promptly — exactly 1×200 and 2×409. HandleUpdateEntry must roll back its
// tx before any pool-bound conflict query; a loser that holds its tx
// connection while acquiring a second one wedges the pool (hold-and-wait).
// pgxpool's default MaxConns is max(4, NumCPU), so only a deliberately tiny
// pool makes the wedge reproducible on any machine.
func TestIntegrationConcurrentTMEntryPatch_NoPoolDeadlock(t *testing.T) {
	bigPool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "tm-deadlock")
	user := testutil.SeedUser(t, q, tenant.ID, "deadlock@test", "pass")

	coll, err := q.CreateTMCollection(ctx, db.CreateTMCollectionParams{
		Name:     "deadlock-tm",
		TenantID: tenant.ID,
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	entry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           coll.ID,
		SourceLanguage: "en",
		TargetLanguage: "de",
		SourceText:     "Your session has expired",
		TargetText:     "Ihre Sitzung ist abgelaufen",
		CreatedBy:      "seed",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("insert entry: %v", err)
	}

	// Tiny pool: 2 connections. 3 concurrent PATCHes → after the winner
	// commits, both losers hold a tx connection each (pool exhausted); a
	// loser that then acquires a second one for the conflict recheck blocks
	// forever. MaxConns=2 makes the wedge deterministic regardless of how
	// many cores the machine running this test has.
	smallCfg, err := pgxpool.ParseConfig(bigPool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	smallCfg.MaxConns = 2
	smallPool, err := pgxpool.NewWithConfig(ctx, smallCfg)
	if err != nil {
		t.Fatalf("small pool: %v", err)
	}
	// Close with a deadline: pgxpool.Close() BLOCKS until every acquired
	// connection is released. When the regression is present, the wedged
	// handler goroutines never release theirs — a plain deferred Close turns
	// a clean 15s test failure into a 10-minute go-test timeout. Leak the pool in that case; the process exits
	// right after anyway.
	defer func() {
		done := make(chan struct{})
		go func() { smallPool.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Log("smallPool.Close() blocked >3s — wedged connections leaked (expected only when the deadlock regression is present)")
		}
	}()

	router := testutil.NewTestRouter(smallPool)

	baseline := entry.UpdatedAt.Time.Format(time.RFC3339Nano)
	url := fmt.Sprintf("/api/tms/%d/entries/%d", coll.ID, entry.ID)

	type result struct{ code int }
	results := make(chan result, 3)
	for i := 0; i < 3; i++ {
		go func(i int) {
			rec := testutil.DoRequest(t, router, http.MethodPatch, url, map[string]any{
				"source_text":         "Your session has expired",
				"target_text":         fmt.Sprintf("variant_%d", i),
				"expected_updated_at": baseline,
			}, user.ID, tenant.ID)
			results <- result{rec.Code}
		}(i)
	}

	// Generous overall deadline: the three requests complete in well under a
	// second when healthy. If the pool wedges, the losers never return — the timeout
	// turns the wedge into a clean failure instead of a hung test run.
	deadline := time.After(15 * time.Second)
	var codes []int
	for len(codes) < 3 {
		select {
		case r := <-results:
			codes = append(codes, r.code)
		case <-deadline:
			t.Fatalf("pool deadlock: only %d/3 PATCHes returned within 15s (got %v) — "+
				"a conflict-path query is acquiring a second pool connection while the tx holds the first", len(codes), codes)
		}
	}

	var wins, conflicts int
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			wins++
		case http.StatusConflict:
			conflicts++
		}
	}
	if wins != 1 || conflicts != 2 {
		t.Errorf("optimistic-lock invariant: want exactly 1×200 + 2×409, got %v", codes)
	}
}
