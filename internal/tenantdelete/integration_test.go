package tenantdelete_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/tenantdelete"
	"openloc/internal/testutil"
)

// taskRecorder is the router's enqueuer, recording the long tasks it is
// handed and their options.
type taskRecorder struct {
	testutil.RecordingEnqueuer
	mu    sync.Mutex
	tasks []*asynq.Task
	opts  [][]asynq.Option
}

func (r *taskRecorder) EnqueueLongTask(task *asynq.Task, opts ...asynq.Option) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = append(r.tasks, task)
	r.opts = append(r.opts, opts)
	return nil
}

// seedOrg is a tenant with one project and one file of segments, its owner, a
// TM with tmEntries entries and a term base with concepts concepts.
func seedOrg(t *testing.T, pool *pgxpool.Pool, q db.Querier, name string, files, tmEntries, concepts int) (tenantID, ownerID int64) {
	t.Helper()
	ctx := context.Background()
	ownerID, tenantID = testutil.RegisterUser(t, q, name)
	project := testutil.SeedProject(t, q, tenantID, name+" project", "en", []string{"de"})
	for i := 0; i < files; i++ {
		file := testutil.SeedFile(t, q, project.ID, name+".txt", db.FileFormatTxt)
		testutil.SeedSegmentsBulk(t, pool, file.ID, "de", 0, 50, "Ziel")
	}
	tm := testutil.SeedTM(t, q, tenantID, name+" TM")
	testutil.SeedTMEntriesBulk(t, pool, tm.ID, "en", "de", tmEntries, func(i int) (string, string) {
		return fmt.Sprintf("source %d", i), fmt.Sprintf("Ziel %d", i)
	})
	tb := testutil.SeedTermBase(t, q, tenantID, name+" terms", "en", "de")
	if _, err := pool.Exec(ctx, `
		WITH c AS (INSERT INTO term_concept (term_base_id) SELECT $1 FROM generate_series(1, $2::int) RETURNING id)
		INSERT INTO term (concept_id, language, text) SELECT id, 'en', 'term ' || id FROM c`, tb.ID, concepts); err != nil {
		t.Fatalf("seed terms: %v", err)
	}
	return tenantID, ownerID
}

func orgRows(t *testing.T, pool *pgxpool.Pool, tenantID int64) int64 {
	t.Helper()
	return testutil.CountRows(t, pool, `
		SELECT (SELECT count(*) FROM tenants WHERE id = $1)
		     + (SELECT count(*) FROM translations t JOIN segments s ON s.id = t.segment_id
		          JOIN files f ON f.id = s.file_id JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1)
		     + (SELECT count(*) FROM translation_memory e JOIN tm_collections c ON c.id = e.tm_id WHERE c.tenant_id = $1)
		     + (SELECT count(*) FROM term_concept c JOIN term_bases b ON b.id = c.term_base_id WHERE b.tenant_id = $1)`, tenantID)
}

// The owner's delete request does no deleting itself: it closes the
// organization at once and leaves the data to a background task.
func TestIntegrationDeleteTenant_RequestClosesTheOrgAndQueuesTheDelete(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	rec := &taskRecorder{}
	router := testutil.NewTestRouterWithEnqueuer(pool, rec)
	tenantID, ownerID := seedOrg(t, pool, q, "closing", 2, 100, 10)
	before := orgRows(t, pool, tenantID)

	resp := testutil.DoRequest(t, router, "DELETE", "/api/tenants/current", nil, ownerID, tenantID)
	testutil.AssertStatus(t, resp, http.StatusAccepted)

	if after := orgRows(t, pool, tenantID); after != before {
		t.Errorf("the request deleted %d of the organization's rows, want 0 (the worker deletes them)", before-after)
	}
	status, err := q.GetTenantStatus(context.Background(), tenantID)
	if err != nil || status.Status != tenantdelete.StatusDeleting {
		t.Fatalf("tenant status = %+v (%v), want %q", status, err, tenantdelete.StatusDeleting)
	}
	if len(rec.tasks) != 1 || rec.tasks[0].Type() != tenantdelete.TaskDelete {
		t.Fatalf("queued tasks = %v, want one %s", rec.tasks, tenantdelete.TaskDelete)
	}
	var p struct {
		TenantID int64 `json:"tenant_id"`
	}
	if err := json.Unmarshal(rec.tasks[0].Payload(), &p); err != nil || p.TenantID != tenantID {
		t.Errorf("task payload = %s, want tenant %d", rec.tasks[0].Payload(), tenantID)
	}
	resp = testutil.DoRequest(t, router, "GET", "/api/projects", nil, ownerID, tenantID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

// The worker deletes a closed organization in bounded statements and leaves
// every other organization alone.
func TestIntegrationDeleteTenant_WorkerDeletesInBoundedSteps(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenantID, _ := seedOrg(t, pool, q, "closing", 3, 12000, 7000)
	otherID, _ := seedOrg(t, pool, q, "staying", 1, 10, 10)
	otherBefore := orgRows(t, pool, otherID)
	if _, err := q.MarkTenantDeleting(ctx, tenantID); err != nil {
		t.Fatal(err)
	}
	// 40 more projects of one file each: the file step visits every project
	// once, not every emptied project again for each file.
	for i := 0; i < 40; i++ {
		project := testutil.SeedProject(t, q, tenantID, fmt.Sprintf("closing project %d", i), "en", []string{"de"})
		testutil.SeedFile(t, q, project.ID, "one.txt", db.FileFormatTxt)
	}
	// Soft-deleted concepts go in the same bounded steps; more of them than
	// one step holds.
	if _, err := pool.Exec(ctx, `
		UPDATE term_concept SET deleted_at = now() WHERE id IN (
			SELECT c.id FROM term_concept c JOIN term_bases b ON b.id = c.term_base_id
			WHERE b.tenant_id = $1 LIMIT 5100)`, tenantID); err != nil {
		t.Fatal(err)
	}

	countPool, log := testutil.CountingPool(t)
	if err := tenantdelete.New(countPool, nil).Delete(ctx, tenantID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if left := orgRows(t, pool, tenantID); left != 0 {
		t.Errorf("%d rows of the deleted organization left", left)
	}
	if after := orgRows(t, pool, otherID); after != otherBefore {
		t.Errorf("the other organization lost %d rows", otherBefore-after)
	}
	// 43 files in 41 projects: one statement per file plus one per project
	// to find it empty, and one page of project ids plus the empty page.
	if n, rows := log.Count("DeleteProjectFileBatch"), log.Rows("DeleteProjectFileBatch"); rows != 43 || n != 43+41 {
		t.Errorf("file deletes: %d statements for %d files, want one per file and one per project (84 for 43)", n, rows)
	}
	if n := log.Count("ListTenantProjectIDsPage"); n != 2 {
		t.Errorf("listed the tenant's projects in %d statements, want 2 (one page, then the empty one)", n)
	}
	if n, rows := log.Count("DeleteTenantTMEntryBatch"), log.Rows("DeleteTenantTMEntryBatch"); rows != 12000 || n < 3 {
		t.Errorf("TM entry deletes: %d statements for %d entries, want chunks of at most 5000", n, rows)
	}
	if n, rows := log.Count("DeleteTenantTermConceptBatch"), log.Rows("DeleteTenantTermConceptBatch"); rows != 7000 || n < 3 {
		t.Errorf("term concept deletes: %d statements for %d concepts, want all 7000 in chunks of at most 5000", n, rows)
	}
}

// A tenant that is not closed is never deleted, whatever task arrives.
func TestIntegrationDeleteTenant_OnlyAClosedTenantIsDeleted(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenantID, _ := seedOrg(t, pool, q, "live", 1, 10, 10)
	before := orgRows(t, pool, tenantID)
	if err := tenantdelete.New(pool, nil).Delete(context.Background(), tenantID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if after := orgRows(t, pool, tenantID); after != before {
		t.Errorf("a live organization lost %d rows", before-after)
	}
}
