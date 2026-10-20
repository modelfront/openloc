package job_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// TestIntegrationJobBulkWrites_QueueBehindStepChange pins that a job's
// clear, selection clear and delete wait for a step change on the job instead
// of deadlocking with its recount, then succeed.
func TestIntegrationJobBulkWrites_QueueBehindStepChange(t *testing.T) {
	cases := []struct {
		name, method string
		path         func(jobID int64) string
		body         func(ids []int64) any
		status       int
		// cleared rows keep a NULL text; a deleted job keeps no rows.
		wantRows int
	}{
		{
			name: "clear", method: http.MethodPost,
			path:   func(id int64) string { return fmt.Sprintf("/api/jobs/%d/clear-translations", id) },
			body:   func([]int64) any { return map[string]any{"scope": "all"} },
			status: http.StatusOK, wantRows: 3,
		},
		{
			name: "clear selection", method: http.MethodPost,
			path:   func(id int64) string { return fmt.Sprintf("/api/jobs/%d/translations/clear", id) },
			body:   func(ids []int64) any { return map[string]any{"translation_ids": ids} },
			status: http.StatusOK, wantRows: 3,
		},
		{
			name: "delete", method: http.MethodDelete,
			path:   func(id int64) string { return fmt.Sprintf("/api/jobs/%d", id) },
			body:   func([]int64) any { return nil },
			status: http.StatusNoContent, wantRows: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool, q, cleanup := testutil.SetupTestDB(t)
			defer cleanup()
			router := testutil.NewTestRouter(pool)
			j := testutil.SeedRecountJob(t, pool, q, "JobDeadlockCo", 3)
			ids := j.IDs

			testutil.ExpectQueuedBehindStepChange(t, pool, j.JobID, ids[len(ids)-1], func() error {
				rec := testutil.DoRequestWithRole(t, router, tc.method, tc.path(j.JobID), tc.body(ids), j.AdminID, j.TenantID, "admin")
				if rec.Code != tc.status {
					return fmt.Errorf("status %d: %s", rec.Code, rec.Body.String())
				}
				return nil
			})

			var rows, withText int
			if err := pool.QueryRow(context.Background(),
				`SELECT count(*), count(active_text) FROM translations WHERE id = ANY($1)`, ids).Scan(&rows, &withText); err != nil {
				t.Fatal(err)
			}
			if rows != tc.wantRows || withText != 0 {
				t.Errorf("rows %d with text %d; want %d rows, none with text", rows, withText, tc.wantRows)
			}
		})
	}
}
