package tenantdelete_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/tenantdelete"
	"openloc/internal/testutil"
)

// A delete is queued with a unique window, not a fixed task id: asynq keeps
// a fixed id while a failed task sits archived, which would turn every later
// sweep enqueue into a silent no-op.
func TestIntegrationDeleteTenant_QueuedOnceButNotForever(t *testing.T) {
	rec := &taskRecorder{}
	if err := tenantdelete.Enqueue(rec, 42); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if len(rec.opts) != 1 {
		t.Fatalf("queued %d tasks, want 1", len(rec.opts))
	}
	opts := fmt.Sprint(rec.opts[0])
	if !strings.Contains(opts, "Unique(") || strings.Contains(opts, "TaskID(") {
		t.Errorf("delete options = %s, want a unique window and no fixed task id", opts)
	}
}

// The sweep queues the delete of every tenant still marked deleting, and only
// those.
func TestIntegrationDeleteTenant_SweepQueuesEveryClosedTenant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	closedA, _ := seedOrg(t, pool, q, "closed-a", 0, 0, 0)
	closedB, _ := seedOrg(t, pool, q, "closed-b", 0, 0, 0)
	seedOrg(t, pool, q, "live", 0, 0, 0)
	for _, id := range []int64{closedA, closedB} {
		if _, err := q.MarkTenantDeleting(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	rec := &taskRecorder{}
	if err := tenantdelete.New(pool, rec).HandleSweep(ctx, tenantdelete.NewSweepTask()); err != nil {
		t.Fatalf("HandleSweep: %v", err)
	}
	var got []string
	for _, task := range rec.tasks {
		got = append(got, string(task.Payload()))
	}
	want := []string{fmt.Sprintf(`{"tenant_id":%d}`, closedA), fmt.Sprintf(`{"tenant_id":%d}`, closedB)}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sweep queued %v, want %v", got, want)
	}
}

// An operator cannot change the status of an organization being deleted, so
// it cannot be reopened to its members halfway through.
func TestIntegrationDeleteTenant_StatusOfAClosedTenantIsFixed(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenantID, _ := seedOrg(t, pool, q, "closing", 0, 0, 0)
	if _, err := q.MarkTenantDeleting(ctx, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.SetTenantStatus(ctx, db.SetTenantStatusParams{ID: tenantID, Status: "approved"}); err == nil {
		t.Error("SetTenantStatus changed a deleting tenant")
	}
	status, err := q.GetTenantStatus(ctx, tenantID)
	if err != nil || status.Status != tenantdelete.StatusDeleting {
		t.Errorf("status = %+v (%v), want %q", status, err, tenantdelete.StatusDeleting)
	}
}

// Without a worker queue the delete runs in the request, in the same steps.
func TestIntegrationDeleteTenant_WithoutAQueueTheRequestDeletes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenantID, ownerID := seedOrg(t, pool, q, "closing", 2, 100, 10)

	resp := testutil.DoRequest(t, router, "DELETE", "/api/tenants/current", nil, ownerID, tenantID)
	testutil.AssertStatus(t, resp, http.StatusAccepted)
	if left := orgRows(t, pool, tenantID); left != 0 {
		t.Errorf("%d rows of the organization left after the request", left)
	}
}
