package hold

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/taskedit"
)

// TTL is how long a hold lives without a renewal. The editor renews every
// 20 s (HOLD_RENEW_MS in web/src/hooks/useSegmentHold.ts), so a hold ends
// this long after its tab stops renewing: closed without the release reaching
// the server, asleep, or crashed.
const TTL = 90 * time.Second

// maxTabIDLen bounds the client-chosen tab id.
const maxTabIDLen = 64

// MaxHoldsPerUserJob bounds one user's live holds in a job. The tab id comes
// from the client, so "one hold per tab" alone does not stop a user from
// holding every segment.
const MaxHoldsPerUserJob = 5

// errLocked: a locked segment takes no hold; nobody may edit it.
var errLocked = httputil.NewConflictError("segment is locked")

// Service takes, renews and releases holds.
type Service struct {
	q     db.Querier
	guard *taskedit.Guard
}

// NewService returns a Service. pool backs the task guard.
func NewService(q db.Querier, pool *pgxpool.Pool) *Service {
	return &Service{q: q, guard: taskedit.New(q, pool)}
}

// Acquired is a hold the caller now has, with the segment as it stands (the
// same shape the job's translation list returns), so the editor starts from
// the newest version before the user types.
type Acquired struct {
	TranslationID int64          `json:"translation_id"`
	ExpiresAt     time.Time      `json:"expires_at"`
	Translation   db.Translation `json:"translation"`
}

// Holder is a live hold in a job.
type Holder struct {
	TranslationID int64     `json:"translation_id"`
	UserID        int64     `json:"user_id"`
	HolderName    string    `json:"holder_name"`
	AcquiredAt    time.Time `json:"acquired_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// Acquire takes or renews the caller's hold on a translation of the job, from
// one browser tab. The tab's hold on any other segment ends first, whether or
// not this one is granted: the tab has moved on. Refusals: not found (the
// translation is not in the job), a locked segment, a task the caller may not
// edit (the guard's coded error; their hold on it ends), and *HeldError when
// another user holds it.
func (s *Service) Acquire(ctx context.Context, tenantID, userID, jobID, translationID int64, tabID string) (Acquired, error) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" || len(tabID) > maxTabIDLen {
		return Acquired{}, httputil.NewClientError("tab_id is required")
	}
	if err := s.q.MoveTabHold(ctx, db.MoveTabHoldParams{
		TenantID: tenantID, UserID: userID, TabID: tabID, TranslationID: translationID,
	}); err != nil {
		return Acquired{}, fmt.Errorf("move tab hold: %w", err)
	}
	target, err := s.q.GetHoldTarget(ctx, db.GetHoldTargetParams{
		TranslationID: translationID, JobID: jobID, TenantID: tenantID,
	})
	if err != nil {
		return Acquired{}, err
	}
	if target.Locked {
		return Acquired{}, errLocked
	}
	if _, err := s.guard.Check(ctx, tenantID, taskedit.Ref{TranslationID: translationID}, taskedit.Opts{}); err != nil {
		if relErr := s.q.ReleaseUserHold(ctx, db.ReleaseUserHoldParams{
			TenantID: tenantID, TranslationID: translationID, UserID: userID,
		}); relErr != nil {
			return Acquired{}, fmt.Errorf("release hold of a refused task: %w", relErr)
		}
		return Acquired{}, err
	}

	var held *db.AcquireHoldRow
	var deadlock error
	for attempt := 0; attempt < 2 && held == nil; attempt++ {
		row, err := s.q.AcquireHold(ctx, db.AcquireHoldParams{
			TranslationID: translationID, TenantID: tenantID, JobID: jobID,
			UserID: userID, TabID: tabID, TtlSeconds: int32(TTL / time.Second),
		})
		if err == nil {
			held = &row
			break
		}
		// The translation or its job was deleted since GetHoldTarget.
		if httputil.IsForeignKeyViolation(err) {
			return Acquired{}, pgx.ErrNoRows
		}
		// Postgres aborted the insert to break a deadlock (a delete of the
		// project cascading through the same rows): try once more.
		if database.IsConflict(err) {
			deadlock = err
			logging.Warn(ctx, err, "segment hold insert lost a deadlock", "hold-deadlock-retry",
				logging.TranslationID(translationID))
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Acquired{}, fmt.Errorf("acquire hold: %w", err)
		}
		holder, err := HeldByOther(ctx, s.q, tenantID, translationID, userID)
		if err != nil {
			return Acquired{}, err
		}
		if holder != nil {
			return Acquired{}, holder
		}
		// The other hold expired between the two statements: try once more.
	}
	if held == nil && deadlock != nil {
		return Acquired{}, fmt.Errorf("acquire hold: %w", deadlock)
	}
	if held == nil {
		return Acquired{}, fmt.Errorf("acquire hold on translation %d: no row after retry", translationID)
	}
	if err := s.q.TrimUserJobHolds(ctx, db.TrimUserJobHoldsParams{
		TenantID: tenantID, JobID: jobID, UserID: userID, Keep: MaxHoldsPerUserJob,
	}); err != nil {
		return Acquired{}, fmt.Errorf("trim holds: %w", err)
	}
	return Acquired{TranslationID: translationID, ExpiresAt: held.ExpiresAt.Time, Translation: target}, nil
}

// Release ends the caller's hold on a translation from one tab. A hold that
// another tab of theirs took over, or another user's, stays.
func (s *Service) Release(ctx context.Context, tenantID, userID, translationID int64, tabID string) error {
	return s.q.ReleaseTabHold(ctx, db.ReleaseTabHoldParams{
		TenantID: tenantID, TranslationID: translationID, UserID: userID, TabID: strings.TrimSpace(tabID),
	})
}

// List returns the live holds in a job.
func (s *Service) List(ctx context.Context, tenantID, jobID int64) ([]Holder, error) {
	rows, err := s.q.ListJobHolds(ctx, db.ListJobHoldsParams{JobID: jobID, TenantID: tenantID})
	if err != nil {
		return nil, err
	}
	out := make([]Holder, len(rows))
	for i, r := range rows {
		out[i] = Holder{
			TranslationID: r.TranslationID, UserID: r.UserID, HolderName: r.HolderName,
			AcquiredAt: ts(r.AcquiredAt), ExpiresAt: ts(r.ExpiresAt),
		}
	}
	return out, nil
}

func ts(t pgtype.Timestamptz) time.Time { return t.Time }
