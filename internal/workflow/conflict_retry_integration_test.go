package workflow_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/workflow"
)

// conflictOnce runs each transaction on inner, but rolls the first one back
// with a deadlock error after its body ran, as Postgres does when it picks
// the transaction as a deadlock victim.
type conflictOnce struct {
	inner pipeline.TxRunner
	runs  int
}

func (c *conflictOnce) InTx(ctx context.Context, fn func(db.Querier) error) error {
	c.runs++
	first := c.runs == 1
	return c.inner.InTx(ctx, func(q db.Querier) error {
		if err := fn(q); err != nil {
			return err
		}
		if first {
			return &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
		}
		return nil
	})
}

// TestIntegrationAdvanceJob_RetriesADeadlockOnce pins that a step advance
// picked as a deadlock victim runs again and lands once: the next step is
// current, its first round is open once, with one start row per segment.
func TestIntegrationAdvanceJob_RetriesADeadlockOnce(t *testing.T) {
	f := setupRounds(t, "Translate", "Review")
	runner := &conflictOnce{inner: pipeline.NewPgxTxRunner(f.pool)}
	svc := workflow.NewService(f.q, runner, nil)

	active, err := f.q.GetActiveStepForJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("active step: %v", err)
	}
	if _, err := svc.AdvanceJob(f.ctx, f.tenantID, f.jobID, active, &f.ownerID, nil, false, nil); err != nil {
		t.Fatalf("AdvanceJob: %v", err)
	}
	if runner.runs != 2 {
		t.Errorf("transaction runs: got %d, want 2", runner.runs)
	}
	var current *int64
	var round int32
	var starts int
	if err := f.pool.QueryRow(f.ctx, `SELECT current_step_id FROM jobs WHERE id = $1`, f.jobID).Scan(&current); err != nil {
		t.Fatalf("job: %v", err)
	}
	if current == nil || *current != f.steps[1] {
		t.Errorf("current step: got %v, want %d", current, f.steps[1])
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT round FROM job_step_assignments WHERE id = $1`, f.steps[1]).Scan(&round); err != nil {
		t.Fatalf("round: %v", err)
	}
	if round != 1 {
		t.Errorf("review round: got %d, want 1", round)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM translation_step_statuses WHERE job_step_assignment_id = $1`, f.steps[1]).Scan(&starts); err != nil {
		t.Fatalf("start rows: %v", err)
	}
	if starts != len(f.segs) {
		t.Errorf("review start rows: got %d, want %d", starts, len(f.segs))
	}
}

// TestIntegrationReject_RetriesADeadlockOnce pins the same for a reject: the
// job lands back on the first step once, whose second round is open once.
func TestIntegrationReject_RetriesADeadlockOnce(t *testing.T) {
	f := setupRounds(t, "Translate", "Review")
	active, err := f.q.GetActiveStepForJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("active step: %v", err)
	}
	if _, err := f.svc.AdvanceJob(f.ctx, f.tenantID, f.jobID, active, &f.ownerID, nil, false, nil); err != nil {
		t.Fatalf("AdvanceJob: %v", err)
	}

	runner := &conflictOnce{inner: pipeline.NewPgxTxRunner(f.pool)}
	f.svc = workflow.NewService(f.q, runner, nil)
	if _, err := f.rejectTo(t, f.owner(), 0, false); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if runner.runs != 2 {
		t.Errorf("transaction runs: got %d, want 2", runner.runs)
	}
	var current *int64
	var round int32
	var starts int
	if err := f.pool.QueryRow(f.ctx, `SELECT current_step_id FROM jobs WHERE id = $1`, f.jobID).Scan(&current); err != nil {
		t.Fatalf("job: %v", err)
	}
	if current == nil || *current != f.steps[0] {
		t.Errorf("current step: got %v, want %d", current, f.steps[0])
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT round FROM job_step_assignments WHERE id = $1`, f.steps[0]).Scan(&round); err != nil {
		t.Fatalf("round: %v", err)
	}
	if round != 2 {
		t.Errorf("translate round: got %d, want 2", round)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM translation_step_statuses WHERE job_step_assignment_id = $1 AND round = 2`, f.steps[0]).Scan(&starts); err != nil {
		t.Fatalf("start rows: %v", err)
	}
	if starts != len(f.segs) {
		t.Errorf("translate round 2 start rows: got %d, want %d", starts, len(f.segs))
	}
	if got := f.stepStatusOf(t, 1); got != "rejected" {
		t.Errorf("review step status: got %q, want rejected", got)
	}
}
