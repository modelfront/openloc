// Package hold keeps track of who has a segment open in the CAT editor. While
// a user holds a translation, other users' writes to it are refused (or
// skipped in bulk), and their editors show the segment read-only with the
// holder's name. A hold is a lease: the editor renews it while the segment
// stays open and releases it when the segment closes.
package hold

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
)

// CodeSegmentHeld is the error code of a write refused because another user
// has the segment open. The frontend and API clients branch on it.
const CodeSegmentHeld = "segment_held"

// HeldError is a write refused because another user holds the segment.
type HeldError struct {
	HolderID   int64
	HolderName string
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("%s is editing this segment. Try again when they have moved on.", e.HolderName)
}

// StatusCode and ErrorCode let httputil.WriteCodedError write the refusal.
func (e *HeldError) StatusCode() int   { return http.StatusConflict }
func (e *HeldError) ErrorCode() string { return CodeSegmentHeld }

// HeldByOther returns a *HeldError when a user other than actorID holds the
// translation now, and nil when nobody else does. Callers use it to explain a
// write that matched no row.
func HeldByOther(ctx context.Context, q db.Querier, tenantID, translationID, actorID int64) (*HeldError, error) {
	h, err := q.GetLiveHolder(ctx, db.GetLiveHolderParams{TranslationID: translationID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read segment holder: %w", err)
	}
	if h.UserID == actorID {
		return nil, nil
	}
	return &HeldError{HolderID: h.UserID, HolderName: h.HolderName}, nil
}
