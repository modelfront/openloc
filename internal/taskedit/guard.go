package taskedit

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/project"
	"openloc/internal/projectevents"
	"openloc/internal/stepround"
)

// Guard checks edits against the job's current task and reopens a completed
// task when a manager edits it.
type Guard struct {
	q    db.Querier
	pool database.TxBeginner
}

// New returns a Guard. pool may be nil where no edit can reach a completed
// task (unit tests on a mock querier); a reopen then fails with an error
// instead of writing outside a transaction.
func New(q db.Querier, pool *pgxpool.Pool) *Guard {
	g := &Guard{q: q}
	if pool != nil {
		g.pool = pool
	}
	return g
}

// Ref names the job an edit lands on: by translation (the job comes from the
// translation's own segment and language) or by job. Set exactly one.
type Ref struct {
	TranslationID int64
	JobID         int64
}

// Decision is a passed Check, to hand to Apply.
type Decision struct {
	verdict verdict
	state   State
	role    string
	opts    Opts
	found   bool
}

// InFirstStep reports whether the edit's current task is the workflow's first
// step. True when the edit has no job or the job has no workflow.
func (d Decision) InFirstStep() bool {
	return !d.found || d.state.inFirstStep()
}

// Result is what an allowed edit sees. ActiveID is the job's in-progress task
// after Apply (0 when the job has none); Reopened is true when this call put a
// completed task back in progress.
type Result struct {
	JobID    int64
	ActiveID int64
	Reopened bool

	// For AfterCommit: the project to re-evaluate when the reopen took the job
	// out of completed, and the reopened task's name for the log line.
	projectID   int64
	jobReopened bool
	stepName    string
}

type noReopenKey struct{}

// WithoutReopen marks ctx as a caller that never reopens a completed task: an
// AI tool, not a person in the editor. MCP marks every tool call.
func WithoutReopen(ctx context.Context) context.Context {
	return context.WithValue(ctx, noReopenKey{}, true)
}

func reopenRefused(ctx context.Context) bool {
	v, _ := ctx.Value(noReopenKey{}).(bool)
	return v
}

// Check decides without writing, for the caller in ctx (its role from the auth
// claims; no claims reads as no role). A refusal comes back as *LockedError. A
// ref with no job row (translations that predate jobs) is allowed.
func (g *Guard) Check(ctx context.Context, tenantID int64, ref Ref, opts Opts) (Decision, error) {
	role := ""
	if claims, ok := auth.ClaimsFromContext(ctx); ok {
		role = claims.Role
	}
	opts.NoReopen = opts.NoReopen || reopenRefused(ctx)
	st, err := readState(ctx, g.q, tenantID, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		logging.Warn(ctx, err, "task edit guard: no job for the edited segment; allowing", "task-edit-no-job",
			logging.TranslationID(ref.TranslationID), logging.JobID(ref.JobID))
		return Decision{verdict: allow}, nil
	}
	if err != nil {
		return Decision{}, err
	}
	v, lerr := decide(st, role, opts)
	if lerr != nil {
		return Decision{}, lerr
	}
	return Decision{verdict: v, state: st, role: role, opts: opts, found: true}, nil
}

// Apply carries out a Decision: nothing for an allowed edit, the reopen
// transaction for a completed task. Call it right before the write, after any
// other check that can still refuse the edit.
func (g *Guard) Apply(ctx context.Context, tenantID int64, d Decision) (Result, error) {
	if d.verdict != reopen {
		return Result{JobID: d.state.JobID, ActiveID: d.state.ActiveID}, nil
	}
	return g.reopen(ctx, tenantID, d)
}

// ApplyInTx is Apply inside the caller's transaction, so the reopen commits or
// rolls back with the write it is for: a write that may still save nothing (a
// re-import that needs force) reopens nothing either. Call AfterCommit with
// the Result once tx has committed.
func (g *Guard) ApplyInTx(ctx context.Context, tx pgx.Tx, tenantID int64, d Decision) (Result, error) {
	if d.verdict != reopen {
		return Result{JobID: d.state.JobID, ActiveID: d.state.ActiveID}, nil
	}
	return g.reopenInTx(ctx, db.New(tx), tenantID, d)
}

// AfterCommit finishes a reopen whose transaction has committed: a reopened
// job can take a completed project with it. A no-op for any other Result.
func (g *Guard) AfterCommit(ctx context.Context, tenantID int64, res Result) {
	if !res.Reopened {
		return
	}
	if res.jobReopened {
		project.Reevaluate(ctx, g.q, tenantID, res.projectID)
	}
	logging.Info(ctx, "task reopened by edit", "task-reopened-by-edit",
		logging.JobID(res.JobID), logging.WorkflowStep(res.stepName))
}

// Ensure is Check then Apply, for writes with nothing in between.
func (g *Guard) Ensure(ctx context.Context, tenantID int64, ref Ref, opts Opts) (Result, error) {
	d, err := g.Check(ctx, tenantID, ref, opts)
	if err != nil {
		return Result{}, err
	}
	return g.Apply(ctx, tenantID, d)
}

// reopen runs reopenInTx in a transaction of its own.
func (g *Guard) reopen(ctx context.Context, tenantID int64, d Decision) (Result, error) {
	if g.pool == nil {
		return Result{}, errors.New("task edit guard: reopen needs a transaction pool")
	}
	jobID := d.state.JobID
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("begin reopen: %w", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			logging.Warn(ctx, err, "task reopen rollback failed", "task-reopen-rollback-failed", logging.JobID(jobID))
		}
	}()
	res, err := g.reopenInTx(ctx, db.New(tx), tenantID, d)
	if err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit reopen: %w", err)
	}
	g.AfterCommit(ctx, tenantID, res)
	return res, nil
}

// reopenInTx puts the job's last completed task back in progress, reopens the
// job when it was completed, and records it in project history, all on txq.
// Concurrent first edits serialise on the job row: the later one re-reads,
// finds the task in progress, and only writes its edit.
func (g *Guard) reopenInTx(ctx context.Context, txq *db.Queries, tenantID int64, d Decision) (Result, error) {
	jobID := d.state.JobID
	if _, err := txq.LockJobForTaskEdit(ctx, db.LockJobForTaskEditParams{JobID: jobID, TenantID: tenantID}); err != nil {
		return Result{}, fmt.Errorf("lock job: %w", err)
	}
	st, err := readState(ctx, txq, tenantID, Ref{JobID: jobID})
	if err != nil {
		return Result{}, fmt.Errorf("re-read task state: %w", err)
	}
	v, lerr := decide(st, d.role, d.opts)
	if lerr != nil {
		return Result{}, lerr
	}
	if v == allow {
		return Result{JobID: jobID, ActiveID: st.ActiveID}, nil
	}

	n, err := txq.ReopenCompletedStepAssignment(ctx, st.LastCompletedID)
	if err != nil {
		return Result{}, fmt.Errorf("reopen task: %w", err)
	}
	if n == 0 {
		return Result{}, fmt.Errorf("reopen task %d: no longer completed", st.LastCompletedID)
	}
	if err := txq.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &st.LastCompletedID}); err != nil {
		return Result{}, fmt.Errorf("set current task: %w", err)
	}
	if err := stepround.Open(ctx, txq, jobID, st.LastCompletedID, stepround.Restore); err != nil {
		return Result{}, err
	}
	jobRows, err := txq.MarkJobReopened(ctx, db.MarkJobReopenedParams{ID: jobID, CurrentStep: pipeline.ReopenedStepLabel})
	if err != nil {
		return Result{}, fmt.Errorf("reopen job: %w", err)
	}
	jobReopened := jobRows > 0

	actorID, actorName := projectevents.ActorFromContext(ctx)
	fileID := st.FileID
	if err := projectevents.EmitInTx(ctx, txq, projectevents.Event{
		ProjectID: st.ProjectID, TenantID: tenantID, Type: projectevents.TypeStepReopened,
		ActorID: actorID, ActorName: actorName, FileID: &fileID, JobID: &jobID,
		Details: map[string]any{
			"step": st.LastCompletedName, "step_type": st.LastCompletedType,
			"file_name": st.FileName, "target_language": st.TargetLanguage,
			"job_reopened": jobReopened,
		},
	}); err != nil {
		return Result{}, fmt.Errorf("record reopen: %w", err)
	}
	return Result{
		JobID: jobID, ActiveID: st.LastCompletedID, Reopened: true,
		projectID: st.ProjectID, jobReopened: jobReopened, stepName: st.LastCompletedName,
	}, nil
}

func readState(ctx context.Context, q db.Querier, tenantID int64, ref Ref) (State, error) {
	if ref.TranslationID != 0 {
		r, err := q.GetTaskEditStateForTranslation(ctx, db.GetTaskEditStateForTranslationParams{
			TranslationID: ref.TranslationID, TenantID: tenantID,
		})
		if err != nil {
			return State{}, err
		}
		return stateFromTranslation(r), nil
	}
	r, err := q.GetTaskEditState(ctx, db.GetTaskEditStateParams{JobID: ref.JobID, TenantID: tenantID})
	if err != nil {
		return State{}, err
	}
	return stateFromJob(r), nil
}
