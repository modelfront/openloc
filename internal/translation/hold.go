package translation

import (
	"context"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/hold"
)

// ExplainNoRows says why a guarded segment write (UpdateTranslationHuman,
// SaveTranslationDraft, ClearTranslation) matched no row: a user other than
// actorID holds the segment (*hold.HeldError), the expected version moved
// (ErrConflict, only when the write carried one), or the row is gone
// (pgx.ErrNoRows). The caller resolved id tenant-scoped.
func ExplainNoRows(ctx context.Context, q db.Querier, tenantID, id, actorID int64, versioned bool) error {
	if err := CheckHeld(ctx, q, tenantID, id, actorID); err != nil {
		return err
	}
	if versioned {
		if _, err := q.GetTranslation(ctx, id); err == nil {
			return ErrConflict
		}
	}
	return pgx.ErrNoRows
}

// CheckHeld returns a *hold.HeldError when a user other than actorID holds the
// segment now, and nil otherwise. Gates call it before any side effect (a task
// reopen, a QA run); the write's own SQL check stays the guarantee.
func CheckHeld(ctx context.Context, q db.Querier, tenantID, id, actorID int64) error {
	held, err := hold.HeldByOther(ctx, q, tenantID, id, actorID)
	if err != nil {
		return err
	}
	if held != nil {
		return held
	}
	return nil
}
