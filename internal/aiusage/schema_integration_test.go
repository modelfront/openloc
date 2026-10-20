package aiusage_test

import (
	"testing"

	"openloc/internal/testutil"
)

// Deleting a job sets ai_usage_events.job_id to NULL on its rows; every
// foreign key of the table must lead an index so that lookup does not scan
// all usage events.
func TestAIUsageEventsSchema_EveryForeignKeyIsIndexed(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	if missing := testutil.UnindexedForeignKeys(t, pool, "ai_usage_events"); len(missing) > 0 {
		t.Errorf("foreign keys of ai_usage_events with no index: %v", missing)
	}
}
