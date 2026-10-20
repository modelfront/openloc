package terminology_test

import (
	"slices"
	"testing"

	"openloc/internal/testutil"
)

// An organization delete removes term concepts in batches by term base, live
// and soft-deleted alike, and every concept delete removes its terms: both
// foreign keys need an index that holds every row, or those lookups scan
// every tenant's concepts and terms.
func TestTermSchema_DeleteForeignKeysAreIndexed(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	for table, column := range map[string]string{"term_concept": "term_base_id", "term": "concept_id"} {
		if slices.Contains(testutil.UnindexedForeignKeys(t, pool, table), column) {
			t.Errorf("%s.%s has no index that holds every row", table, column)
		}
	}
}
