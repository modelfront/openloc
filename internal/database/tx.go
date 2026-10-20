package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
)

// TxBeginner opens a transaction: a *pgxpool.Pool, or a pgx.Tx, whose Begin
// is a savepoint. A helper given the caller's transaction therefore commits
// or rolls back with it.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// InTx runs fn in one transaction and commits only if fn succeeds. fn gets the
// transaction, for helpers that open their own (they nest as savepoints), and
// a querier bound to it.
func InTx(ctx context.Context, b TxBeginner, fn func(tx pgx.Tx, q db.Querier) error) error {
	tx, err := b.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op after Commit

	if err := fn(tx, db.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// InJobsTx is InTx that first locks the jobs' rows, in id order, as a step
// change does before it writes. A write to many of a job's translations runs
// here, so it queues behind a step change instead of deadlocking with the
// step change's commit-time recount, which locks every translation of the job.
func InJobsTx(ctx context.Context, b TxBeginner, tenantID int64, jobIDs []int64, fn func(tx pgx.Tx, q db.Querier) error) error {
	return InTx(ctx, b, func(tx pgx.Tx, q db.Querier) error {
		if _, err := q.LockJobsForWrite(ctx, db.LockJobsForWriteParams{JobIds: jobIDs, TenantID: tenantID}); err != nil {
			return fmt.Errorf("lock jobs: %w", err)
		}
		return fn(tx, q)
	})
}

// FileJobIDs returns the id of the file's job in lang, for InJobsTx: none when
// the file has no job in that language.
func FileJobIDs(ctx context.Context, q db.Querier, fileID int64, lang string) ([]int64, error) {
	job, err := q.GetJobByFileAndLang(ctx, db.GetJobByFileAndLangParams{FileID: fileID, TargetLanguage: lang})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("get job: %w", err)
	}
	return []int64{job.ID}, nil
}
