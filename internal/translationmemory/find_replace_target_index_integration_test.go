package translationmemory_test

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"openloc/internal/testutil"
)

const (
	frIndexTMSize  = 100000 // entries in the searched TM
	frIndexMatches = 100    // of them, entries whose target holds the find term
)

// Find-and-replace on a large TM with scope target or both reads only the
// entries whose text can hold the term, not every entry of the TM, for both
// the page query and the totals, case-sensitive or not.
func TestIntegrationTMFindReplace_TargetScopeUsesIndex(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenant := testutil.SeedTenant(t, q, "FRIndexCorp")
	tm := testutil.SeedTM(t, q, tenant.ID, "FR Index TM")
	other := testutil.SeedTM(t, q, tenant.ID, "FR Index Other TM")
	seed := func(i int) (string, string) {
		if i%(frIndexTMSize/frIndexMatches) == 0 {
			return fmt.Sprintf("source line %d", i), fmt.Sprintf("línea con Zanahoria %d", i)
		}
		return fmt.Sprintf("source line %d", i), fmt.Sprintf("línea destino %d", i)
	}
	testutil.SeedTMEntriesBulk(t, pool, tm.ID, "en", "es", frIndexTMSize, seed)
	testutil.SeedTMEntriesBulk(t, pool, other.ID, "en", "es", frIndexTMSize, seed)
	testutil.Exec(t, pool, `ANALYZE translation_memory`)

	findSQL := testutil.GeneratedSQL(t, "FindTMEntriesForFR")
	countSQL := testutil.GeneratedSQL(t, "CountTMFRMatches")
	for _, scope := range []struct {
		name               string
		inSource, inTarget bool
	}{{"target", false, true}, {"both", true, true}} {
		for _, cs := range []struct {
			caseSensitive bool
			find          string
		}{{true, "Zanahoria"}, {false, "zanahoria"}} {
			name := fmt.Sprintf("%s/case_sensitive=%v", scope.name, cs.caseSensitive)
			pattern := "%" + cs.find + "%"

			// FindTMEntriesForFR: tm_id, source_language, target_language,
			// after_id, in_source, case_sensitive, pattern, in_target, page_limit.
			got, scanned := tmRowsReadAndScanned(t, pool, findSQL,
				tm.ID, "", "", int64(0), scope.inSource, cs.caseSensitive, pattern, scope.inTarget, int32(500))
			if got != frIndexMatches {
				t.Errorf("%s: page query returned %.0f entries, want %d", name, got, frIndexMatches)
			}
			if scanned > 10*frIndexMatches {
				t.Errorf("%s: page query scanned %.0f TM rows for %d matches in a TM of %d",
					name, scanned, frIndexMatches, frIndexTMSize)
			}

			// CountTMFRMatches: in_source, in_target, case_sensitive, find,
			// tm_id, source_language, target_language, pattern.
			got, scanned = tmRowsReadAndScanned(t, pool, countSQL,
				scope.inSource, scope.inTarget, cs.caseSensitive, cs.find, tm.ID, "", "", pattern)
			if got != frIndexMatches {
				t.Errorf("%s: totals query counted %.0f entries, want %d", name, got, frIndexMatches)
			}
			if scanned > 10*frIndexMatches {
				t.Errorf("%s: totals query scanned %.0f TM rows for %d matches in a TM of %d",
					name, scanned, frIndexMatches, frIndexTMSize)
			}
		}
	}
}

// tmRowsReadAndScanned runs sql under EXPLAIN ANALYZE, planned with its
// values as the app plans it, and returns the rows its scans of
// translation_memory produced and everything they touched.
func tmRowsReadAndScanned(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) (read, scanned float64) {
	t.Helper()
	plan := testutil.ExplainAnalyze(t, pool, sql, args...)
	return plan.RowsRead("translation_memory"), plan.RowsScanned("translation_memory")
}
