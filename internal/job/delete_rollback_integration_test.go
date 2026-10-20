package job_test

import (
	"context"
	"fmt"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationJobDelete_RollsBackOnDeleteFailure pins the transactional
// behavior of HandleDeleteJob: DeleteJobTranslations and DeleteJob
// run in one transaction, so a forced DeleteJob failure must leave the
// translations in place — otherwise the project list shows an orphaned
// 0-translation job with no recovery path.
//
// Failure injection: this test installs a per-test BEFORE-DELETE trigger
// on `jobs` that raises an exception when the target job_id is deleted.
// Unlike the project test (which can lean on a NO-ACTION FK to settings_profiles),
// the only FK to `jobs.id` is `job_step_assignments` with ON DELETE CASCADE,
// so there's no natural failure mode — the trigger is the cleanest portable
// way to force the second query to abort.
//
// Anti-tautology: running the two deletes of HandleDeleteJob outside one transaction makes
// this test fail — translations are deleted before the trigger fires, and the
// readback finds 0 translations instead of 2.
func TestIntegrationJobDelete_RollsBackOnDeleteFailure(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "JobDelRollbackCorp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "jobdel@test.com", "pass", "admin")

	project := testutil.SeedProject(t, q, tenant.ID, "Rollback Project", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "rollback.txt", db.FileFormatTxt)
	seg1 := testutil.SeedSegment(t, q, file.ID, "Hello", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "World", 2)
	tr1 := testutil.SeedTranslation(t, q, seg1.ID, "es", db.TranslationStatusApproved)
	tr2 := testutil.SeedTranslation(t, q, seg2.ID, "es", db.TranslationStatusApproved)
	job := testutil.SeedJob(t, q, file.ID, "es")

	// Install per-test BEFORE-DELETE trigger that aborts only when this
	// specific job ID is the deletion target. Scoped + idempotent — the
	// trigger and function are dropped at the end of the test.
	triggerName := fmt.Sprintf("test_block_delete_job_%d", job.ID)
	fnName := fmt.Sprintf("test_block_fn_job_%d", job.ID)

	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger AS $body$
		BEGIN
			IF OLD.id = %d THEN
				RAISE EXCEPTION 'test-injected delete failure for job %d';
			END IF;
			RETURN OLD;
		END;
		$body$ LANGUAGE plpgsql;
	`, fnName, job.ID, job.ID)); err != nil {
		t.Fatalf("create test trigger function: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE DELETE ON jobs
		FOR EACH ROW EXECUTE FUNCTION %s();
	`, triggerName, fnName)); err != nil {
		t.Fatalf("create test trigger: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(),
			fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON jobs`, triggerName))
		_, _ = pool.Exec(context.Background(),
			fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fnName))
	}()

	// Sanity: translations exist BEFORE the call.
	var beforeCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM translations t
		 JOIN segments s ON s.id = t.segment_id
		 WHERE s.file_id = $1`, file.ID,
	).Scan(&beforeCount); err != nil {
		t.Fatalf("pre-readback: %v", err)
	}
	if beforeCount != 2 {
		t.Fatalf("expected 2 translations before delete, got %d", beforeCount)
	}

	// Call DELETE /api/jobs/{id} — expect 5xx because the trigger blocks the
	// DeleteJob step inside the handler's transaction.
	rec := testutil.DoRequest(t, router, "DELETE",
		fmt.Sprintf("/api/jobs/%d", job.ID), nil, user.ID, tenant.ID)
	if rec.Code < 500 || rec.Code >= 600 {
		t.Fatalf("expected 5xx (trigger blocks DeleteJob), got %d body=%s", rec.Code, rec.Body.String())
	}

	// ROLLBACK PROOF — both translations should still be there.
	var afterCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM translations t
		 JOIN segments s ON s.id = t.segment_id
		 WHERE s.file_id = $1`, file.ID,
	).Scan(&afterCount); err != nil {
		t.Fatalf("post-readback: %v", err)
	}
	if afterCount != 2 {
		t.Fatalf("translations were deleted despite tx wrap — got %d, want 2 (THE BUG: leaked across the failed second query)", afterCount)
	}

	// And the job itself should still be there (the trigger blocked its delete).
	var jobCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM jobs WHERE id = $1`, job.ID,
	).Scan(&jobCount); err != nil {
		t.Fatalf("readback job: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("job row was deleted — got %d, want 1", jobCount)
	}

	// Quiet the unused-vars warning when the type names matter for the reader.
	_, _ = tr1, tr2
}
