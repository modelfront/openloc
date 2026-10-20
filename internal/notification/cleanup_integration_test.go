// Integration test for migration 031 — polymorphic-ref cleanup
// triggers on `notifications`.
//
// What we pin:
//   1. Deleting a job clears its job-typed notifications.
//   2. Deleting a project (which CASCADEs to its jobs) clears BOTH
//      project-typed AND job-typed notifications via per-row trigger
//      firing on every cascaded job.
//   3. Cross-tenant isolation: tenant A's job delete doesn't touch
//      tenant B's notifications.
//
// Without the triggers every job/project delete leaves dangling rows in
// user notification queues.
//
// Anti-tautology proof (manual, recorded 2026-05-07):
//   Mutation:    `DROP TRIGGER cleanup_job_notifications ON jobs;`
//                applied via raw SQL before the test.
//   Diagnostic:  TestNotificationsCleanup_JobDelete fails because
//                the notification row persists after the job DELETE.
//   Restore:     re-apply the migration's CREATE TRIGGER block.

package notification_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestNotificationsCleanup_JobDelete(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "JobCleanupCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@jobcleanup.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")

	// Seed a notification keyed to this job. Using raw SQL so the test
	// doesn't depend on the production CreateNotification helper's
	// behaviour staying constant.
	if _, err := pool.Exec(ctx,
		`INSERT INTO notifications (tenant_id, user_id, event_type, subject, body_preview, reference_type, reference_id, emailed)
		 VALUES ($1, $2, 'deadline', 'test', '', 'job', $3, false)`,
		tenant.ID, user.ID, job.ID); err != nil {
		t.Fatalf("seed notification: %v", err)
	}

	// Sanity: the notification exists before delete.
	var beforeCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE reference_type = 'job' AND reference_id = $1`, job.ID,
	).Scan(&beforeCount); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if beforeCount != 1 {
		t.Fatalf("expected 1 notification before delete, got %d", beforeCount)
	}

	// Delete the job. The BEFORE DELETE trigger must wipe the notification.
	if _, err := pool.Exec(ctx, `DELETE FROM jobs WHERE id = $1`, job.ID); err != nil {
		t.Fatalf("delete job: %v", err)
	}

	var afterCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE reference_type = 'job' AND reference_id = $1`, job.ID,
	).Scan(&afterCount); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if afterCount != 0 {
		t.Errorf("expected 0 notifications after job delete, got %d (trigger didn't fire)", afterCount)
	}
}

// TestNotificationsCleanup_ProjectDelete_CascadesToJobs is the
// load-bearing test. It pins TWO behaviours in one shot:
//   - The project trigger fires for a project-typed notification.
//   - The job trigger fires per-row for each cascaded job, cleaning
//     job-typed notifications too.
//
// Without per-row trigger fan-out via the cascade, a project delete
// leaves job-typed notifications dangling.
func TestNotificationsCleanup_ProjectDelete_CascadesToJobs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ProjCleanupCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@projcleanup.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")

	// Two notifications: one project-typed, one job-typed.
	if _, err := pool.Exec(ctx,
		`INSERT INTO notifications (tenant_id, user_id, event_type, subject, body_preview, reference_type, reference_id, emailed)
		 VALUES ($1, $2, 'deadline', 'p', '', 'project', $3, false),
		        ($1, $2, 'deadline', 'j', '', 'job',     $4, false)`,
		tenant.ID, user.ID, project.ID, job.ID); err != nil {
		t.Fatalf("seed notifications: %v", err)
	}

	// Delete the project. The cascade chain
	// projects → files → segments → jobs fires the per-row job trigger
	// AND the project trigger fires for the project itself.
	if _, err := pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, project.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	// Both notifications should be gone — the project-typed via the
	// project trigger, the job-typed via the cascade-fired job trigger.
	var remaining int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications
		 WHERE (reference_type = 'project' AND reference_id = $1)
		    OR (reference_type = 'job'     AND reference_id = $2)`,
		project.ID, job.ID,
	).Scan(&remaining); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if remaining != 0 {
		t.Errorf("expected 0 notifications after project delete, got %d (cascade trigger didn't fire)", remaining)
	}
}

// TestNotificationsCleanup_LeavesOtherTenants pins that the trigger's
// scope is the row's reference_id, NOT some over-broad sweep. A
// regression that DELETEd by reference_type alone would silently wipe
// every tenant's notifications — the test catches it.
func TestNotificationsCleanup_LeavesOtherTenants(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Tenant A
	tenantA := testutil.SeedTenant(t, q, "TenantA-Cleanup")
	userA := testutil.SeedUser(t, q, tenantA.ID, "a@cleanup.test", "pw")
	projectA := testutil.SeedProject(t, q, tenantA.ID, "PA", "en", []string{"fr"})
	fileA := testutil.SeedFile(t, q, projectA.ID, "fa.txt", db.FileFormatTxt)
	jobA := testutil.SeedJob(t, q, fileA.ID, "fr")

	// Tenant B
	tenantB := testutil.SeedTenant(t, q, "TenantB-Cleanup")
	userB := testutil.SeedUser(t, q, tenantB.ID, "b@cleanup.test", "pw")
	projectB := testutil.SeedProject(t, q, tenantB.ID, "PB", "en", []string{"fr"})
	fileB := testutil.SeedFile(t, q, projectB.ID, "fb.txt", db.FileFormatTxt)
	jobB := testutil.SeedJob(t, q, fileB.ID, "fr")

	// One notification per tenant pointing at their respective jobs.
	if _, err := pool.Exec(ctx,
		`INSERT INTO notifications (tenant_id, user_id, event_type, subject, body_preview, reference_type, reference_id, emailed)
		 VALUES ($1, $2, 'deadline', 'a', '', 'job', $3, false),
		        ($4, $5, 'deadline', 'b', '', 'job', $6, false)`,
		tenantA.ID, userA.ID, jobA.ID,
		tenantB.ID, userB.ID, jobB.ID); err != nil {
		t.Fatalf("seed notifications: %v", err)
	}

	// Delete tenant A's job only.
	if _, err := pool.Exec(ctx, `DELETE FROM jobs WHERE id = $1`, jobA.ID); err != nil {
		t.Fatalf("delete job A: %v", err)
	}

	// Tenant A's notification: gone. Tenant B's: untouched.
	var aCount, bCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE reference_type = 'job' AND reference_id = $1`, jobA.ID,
	).Scan(&aCount); err != nil {
		t.Fatalf("count A after: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE reference_type = 'job' AND reference_id = $1`, jobB.ID,
	).Scan(&bCount); err != nil {
		t.Fatalf("count B after: %v", err)
	}
	if aCount != 0 {
		t.Errorf("tenant A notification should be deleted, got %d remaining", aCount)
	}
	if bCount != 1 {
		t.Errorf("tenant B notification should be PRESERVED, got %d remaining (trigger over-deleted across tenants)", bCount)
	}
}
