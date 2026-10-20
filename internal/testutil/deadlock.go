package testutil

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
)

// BackendPID returns the Postgres backend pid of tx's connection.
func BackendPID(t testing.TB, tx pgx.Tx) uint32 {
	t.Helper()
	var pid uint32
	if err := tx.QueryRow(context.Background(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("backend pid: %v", err)
	}
	return pid
}

// WaitLockWait polls until the backend pid waits on a lock, so a race test
// moves the other side only once this one is really blocked.
func WaitLockWait(t testing.TB, pool *pgxpool.Pool, pid uint32) {
	t.Helper()
	pollUntil(t, pool, "backend never waited on a lock",
		`SELECT COALESCE(bool_or(wait_event_type = 'Lock'), false) FROM pg_stat_activity WHERE pid = $1`, pid)
}

// blockedQuery polls until some backend waits on a lock pid holds, and
// returns the statement it waits in.
func blockedQuery(t testing.TB, pool *pgxpool.Pool, pid uint32) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var query string
		err := pool.QueryRow(context.Background(),
			`SELECT query FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)) LIMIT 1`, pid).Scan(&query)
		if err == nil {
			return query
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no backend ever waited on the step change (pid %d)", pid)
	return ""
}

func pollUntil(t testing.TB, pool *pgxpool.Pool, failure, query string, pid uint32) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var ok bool
		if err := pool.QueryRow(context.Background(), query, pid).Scan(&ok); err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s (pid %d)", failure, pid)
}

// ExpectQueuedBehindStepChange runs write while a step change on the job
// holds the job lock, a pending recount and the translation lastID, and fails
// t unless write waits on a job lock and both succeed. write must reach other
// rows of the job before lastID, so without the job lock it deadlocks with the
// recount; the step change's 30 s deadlock check makes write the side
// Postgres cancels. With moveTo, the step change also makes that task the
// job's current one.
func ExpectQueuedBehindStepChange(t testing.TB, pool *pgxpool.Pool, jobID, lastID int64, write func() error, moveTo ...int64) {
	t.Helper()
	ctx := context.Background()
	stepTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin step change: %v", err)
	}
	defer func() { _ = stepTx.Rollback(ctx) }()
	exec := func(sql string, args ...any) int64 {
		tag, err := stepTx.Exec(ctx, sql, args...)
		if err != nil {
			t.Fatalf("step change %q: %v", sql, err)
		}
		return tag.RowsAffected()
	}
	exec(`SET LOCAL deadlock_timeout = '30s'`)
	exec(`SELECT 1 FROM jobs WHERE id = $1 FOR UPDATE`, jobID)
	// What every step change does: mark the job for a recount at commit.
	if n := exec(`INSERT INTO job_translation_stats_pending (file_id, target_language)
		SELECT file_id, target_language FROM jobs WHERE id = $1`, jobID); n != 1 {
		t.Fatalf("mark the job for a recount: %d rows, want 1", n)
	}
	exec(`SELECT 1 FROM translations WHERE id = $1 FOR UPDATE`, lastID)
	for _, stepID := range moveTo {
		exec(`UPDATE jobs SET current_step_id = $2 WHERE id = $1`, jobID, stepID)
	}

	stepPID := BackendPID(t, stepTx)
	writeDone := make(chan error, 1)
	go func() { writeDone <- write() }()
	// The write must wait for the job lock itself, before it holds any row.
	if q := blockedQuery(t, pool, stepPID); !strings.Contains(q, "-- name: Lock") || !strings.Contains(q, "OF j") {
		t.Errorf("the write waited on the step change in %q, want a job lock", firstLine(q))
	}
	commitDone := make(chan error, 1)
	go func() { commitDone <- stepTx.Commit(ctx) }()

	for _, w := range []struct {
		what string
		done chan error
	}{{"write", writeDone}, {"step change commit", commitDone}} {
		select {
		case err := <-w.done:
			if err != nil {
				t.Fatalf("%s: %v", w.what, err)
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("%s: still blocked after 20s", w.what)
		}
	}
}

// RecountJob is a German job on its first task, with an admin assigned and
// translated rows, for ExpectQueuedBehindStepChange.
type RecountJob struct {
	TenantID, AdminID, ProjectID, JobID int64
	// IDs are the job's translations, in id order.
	IDs []int64
}

// SeedRecountJob seeds a RecountJob with n translated rows.
func SeedRecountJob(t testing.TB, pool *pgxpool.Pool, q db.Querier, name string, n int) RecountJob {
	t.Helper()
	tenant := SeedTenant(t, q, name)
	admin := SeedUserWithRole(t, q, tenant.ID, strings.ToLower(name)+"@test.com", "pass", "admin")
	project := SeedProject(t, q, tenant.ID, name, "en", []string{"de"})
	file := SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := SeedJob(t, q, file.ID, "de")
	SeedJobStep(t, q, project.SettingsProfileID, job.ID, &admin.ID)
	ids := make([]int64, n)
	for i := range ids {
		seg := SeedSegment(t, q, file.ID, fmt.Sprintf("Source %d", i), int32(i+1))
		ids[i] = SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview).ID
		if _, err := pool.Exec(context.Background(),
			`UPDATE translations SET active_text = $2 WHERE id = $1`, ids[i], fmt.Sprintf("Ziel %d", i)); err != nil {
			t.Fatalf("set text: %v", err)
		}
	}
	return RecountJob{TenantID: tenant.ID, AdminID: admin.ID, ProjectID: project.ID, JobID: job.ID, IDs: ids}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
