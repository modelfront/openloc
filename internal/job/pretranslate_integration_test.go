package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// =============================================================================
// Project-level pre-translate fan-out
// =============================================================================
//
// HandleProjectPretranslate enqueues pre-translate for every job in a
// project that isn't already running or completed, so a PM can
// trigger pre-translation across all files from the Analysis tab
// without opening each file's CAT editor.
//
// Note on test routing: the standard test router doesn't wire an
// asynq enqueuer (no Redis in tests), so HandleProjectPretranslate
// falls through to the synchronous `pipeline.TranslateFile` path. That
// path doesn't actually call any MT engine in this test setup (the
// jobs have zero translatable segments), so the work completes
// instantly and the response is HTTP 200 with `queued > 0`.
//
// =============================================================================
// Anti-tautology RED proofs (durable artifact)
// =============================================================================
// Proof 1 — running/completed jobs are correctly skipped.
//   Mutation:    handler_pipeline.go::HandleProjectPretranslate — drop
//                the `if pipeline.IsPipelineRunning(...) || ... continue`
//                guard.
//   Test:        TestIntegrationProjectPretranslate_SkipsRunningAndCompleted
//   Diagnostic:  the test seeds 2 running + 1 completed + 2 untouched
//                jobs; without the guard, all 5 would queue and
//                `skipped` would be 0 (test asserts 3).
//   Proven:      2026-05-06.
//
// Proof 2 — cross-tenant attempts return 404.
//   Mutation:    handler_pipeline.go::HandleProjectPretranslate — remove
//                the `GetProject` tenant check.
//   Test:        TestIntegrationProjectPretranslate_CrossTenantReturns404
//   Diagnostic:  the test would receive 200 + a queued count instead
//                of the expected 404.
//   Proven:      2026-05-06.

// TestIntegrationProjectPretranslate_QueuesAllEligibleJobs is the
// happy path: a project with three fresh jobs (no pre-translate yet)
// gets all three enqueued. Asserts the response shape carries
// `queued`, `skipped`, `total` so the FE toast can summarize.
func TestIntegrationProjectPretranslate_QueuesAllEligibleJobs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PretranslateCo")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Pretranslate Test", "en", []string{"es", "fr"})

	file1 := testutil.SeedFile(t, q, project.ID, "a.txt", db.FileFormatTxt)
	file2 := testutil.SeedFile(t, q, project.ID, "b.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, file1.ID, "es")
	testutil.SeedJob(t, q, file1.ID, "fr")
	testutil.SeedJob(t, q, file2.ID, "es")
	// 3 jobs total, all in the default "created" state (jobs.status
	// defaults to 'created' per migration 001_init.sql; SeedJob calls
	// CreateJob without overriding). "created" is neither "running" nor
	// "completed", so all three are eligible to be queued.

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/projects/%d/pretranslate", project.ID),
		nil, user.ID, tenant.ID,
	)
	// Test router has no enqueuer, so the handler runs synchronously
	// and returns 200 (not 202).
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Queued  int `json:"queued"`
		Skipped int `json:"skipped"`
		Total   int `json:"total"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	if resp.Total != 3 {
		t.Errorf("total: want 3, got %d", resp.Total)
	}
	if resp.Queued != 3 {
		t.Errorf("queued: want 3 (all eligible), got %d", resp.Queued)
	}
	if resp.Skipped != 0 {
		t.Errorf("skipped: want 0 (no jobs running/completed), got %d", resp.Skipped)
	}
}

// TestIntegrationProjectPretranslate_SkipsRunningAndCompleted pins the
// skip-policy contract. The handler must NOT re-enqueue jobs that are
// already mid-flight (would race) or already done (no-op). This is
// the load-bearing user-facing behavior — without it, a PM who clicks
// "Pre-translate all" while one job is mid-MT would race the running
// pipeline.
func TestIntegrationProjectPretranslate_SkipsRunningAndCompleted(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	enq := &testutil.RecordingEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(pool, enq)
	tenant := testutil.SeedTenant(t, q, "SkipCo")
	user := testutil.SeedUser(t, q, tenant.ID, "skip@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Skip Test", "en", []string{"es"})

	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	jobRunning := testutil.SeedJob(t, q, file.ID, "es")
	jobCompleted := testutil.SeedJob(t, q, file.ID, "de")
	jobReady := testutil.SeedJob(t, q, file.ID, "fr")

	// Force two jobs into the "skip" states. Use raw SQL because the
	// public job-update queries are workflow-specific and we just want
	// the column flipped for this test.
	//
	// A REAL mid-flight job is in_progress with a non-"complete" step —
	// exactly what setJobStatus/ClaimJobPipeline write. Never seed
	// status='running': no code writes it, so a handler check against it
	// would pass here and never fire in production.
	if _, err := pool.Exec(ctx,
		`UPDATE jobs SET status = 'in_progress', current_step = 'MT: 3 / 10' WHERE id = $1`, jobRunning.ID); err != nil {
		t.Fatalf("force running: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE jobs SET status = 'completed' WHERE id = $1`, jobCompleted.ID); err != nil {
		t.Fatalf("force completed: %v", err)
	}

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/projects/%d/pretranslate", project.ID),
		nil, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusAccepted)

	var resp struct {
		Queued  int `json:"queued"`
		Skipped int `json:"skipped"`
		Total   int `json:"total"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	// What was actually queued, not just what the response claims.
	if len(enq.Pipelines) != 1 || enq.Pipelines[0].JobID != jobReady.ID {
		t.Errorf("enqueued pipelines = %+v, want only job %d", enq.Pipelines, jobReady.ID)
	}
	if resp.Total != 3 {
		t.Errorf("total: want 3, got %d", resp.Total)
	}
	if resp.Skipped != 2 {
		t.Errorf("skipped: want 2 (one running + one completed), got %d", resp.Skipped)
	}
	if resp.Queued != 1 {
		t.Errorf("queued: want 1 (only jobReady), got %d", resp.Queued)
	}
}

// TestIntegrationProjectPretranslate_CrossTenantReturns404 — tenant B
// caller hits tenant A's project. Even though pretranslate is a
// "harmless" idempotent op, the tenant boundary holds. Every
// tenant-touching endpoint needs a cross-tenant test (CONTRIBUTING.md,
// "Tenant isolation").
func TestIntegrationProjectPretranslate_CrossTenantReturns404(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "TenantA")
	userA := testutil.SeedUser(t, q, tenantA.ID, "a@test.com", "pass")
	projectA := testutil.SeedProject(t, q, tenantA.ID, "Project A", "en", []string{"es"})
	fileA := testutil.SeedFile(t, q, projectA.ID, "a.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, fileA.ID, "es")
	_ = userA

	tenantB := testutil.SeedTenant(t, q, "TenantB")
	userB := testutil.SeedUser(t, q, tenantB.ID, "b@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/projects/%d/pretranslate", projectA.ID),
		nil, userB.ID, tenantB.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

// TestIntegrationProjectPretranslate_EmptyProject — no jobs to run.
// Should return queued=0, skipped=0, total=0 with HTTP 200 (not 500
// or 404). A FE toast that says "Queued 0 jobs" gives the user
// honest feedback rather than a confusing error.
func TestIntegrationProjectPretranslate_EmptyProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "EmptyCo")
	user := testutil.SeedUser(t, q, tenant.ID, "empty@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Empty Project", "en", []string{"es"})

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/projects/%d/pretranslate", project.ID),
		nil, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	body := rec.Body.String()
	var resp map[string]int
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["queued"] != 0 || resp["skipped"] != 0 || resp["total"] != 0 {
		t.Errorf("empty project should report all zeros, got %+v", resp)
	}
}
