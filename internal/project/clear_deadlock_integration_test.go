package project_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// TestIntegrationProjectClear_QueuesBehindStepChange pins that a
// project-wide clear that meets a step change on one of its jobs waits for it
// instead of deadlocking with its recount, then clears every row.
func TestIntegrationProjectClear_QueuesBehindStepChange(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	j := testutil.SeedRecountJob(t, pool, q, "ProjectDeadlockCo", 3)

	testutil.ExpectQueuedBehindStepChange(t, pool, j.JobID, j.IDs[len(j.IDs)-1], func() error {
		rec := testutil.DoRequestWithRole(t, router, http.MethodPost, fmt.Sprintf("/api/projects/%d/clear-translations", j.ProjectID),
			map[string]any{"scope": "all"}, j.AdminID, j.TenantID, "admin")
		if rec.Code != http.StatusOK {
			return fmt.Errorf("status %d: %s", rec.Code, rec.Body.String())
		}
		return nil
	})

	var withText int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(active_text) FROM translations WHERE id = ANY($1)`, j.IDs).Scan(&withText); err != nil {
		t.Fatal(err)
	}
	if withText != 0 {
		t.Errorf("%d rows kept their text, want all cleared", withText)
	}
}
