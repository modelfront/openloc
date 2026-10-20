package metrics_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"openloc/internal/metrics"
	"openloc/internal/testutil"
)

// A history measure over a time range reads the history of that tenant and
// that range only: neither the tenant's older history nor another tenant's
// history is scanned and then filtered away.
func TestIntegrationMetricsHistoryMeasuresReadOnlyTheirRange(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()
	seedHistory := func(fileID, userID, engineID int64, from, n int, age time.Duration) {
		t.Helper()
		ids := testutil.SeedSegmentsBulk(t, f.pool, fileID, "de", from, n, "Ziel")
		if _, err := f.pool.Exec(ctx, `
			INSERT INTO translation_history (translation_id, kind, created_by, mt_engine_id, editing_time_ms, edit_distance, created_at)
			SELECT id, (ARRAY['human', 'mt', 'approved', 'auto_approved'])[1 + (id % 4)::int]::translation_history_kind,
			       $2, $3, 1000, 1, now() - $4::interval
			FROM unnest($1::bigint[]) id`, ids, userID, engineID, age.String()); err != nil {
			t.Fatalf("seed history: %v", err)
		}
	}
	// Tenant A's older work, outside the charted range, and the recent work
	// of tenant B, twenty times larger, inside it.
	seedHistory(f.fileA.ID, f.userA.ID, f.engineA.ID, 1000, 300, 200*24*time.Hour)
	seedHistory(f.fileB.ID, f.userB.ID, f.engineB.ID, 1000, 6000, time.Hour)
	if _, err := f.pool.Exec(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}

	names := metrics.HistoryMeasuresForTest()
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("no history measures found")
	}
	now := time.Now().UTC()
	for _, name := range names {
		sql, args, err := metrics.CompileForTest(f.tenantA.ID, metrics.Request{
			Measure: name, From: now.AddDate(0, 0, -30), To: now.Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("%s: compile: %v", name, err)
		}
		if read := testutil.ExplainAnalyze(t, f.pool, sql, args...).RowsScanned("translation_history"); read > 50 {
			t.Errorf("%s over 30 days read %.0f history rows; tenant A has a handful in range", name, read)
		}
	}
}
