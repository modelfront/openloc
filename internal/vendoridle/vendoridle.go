// Package vendoridle disables the vendor org memberships nobody uses: a
// member who has not used their vendor org for InactiveAfter loses access to
// it, after a warning email WarnAhead earlier. Only that membership is
// disabled; the person's account and other orgs stay. The vendor's admins
// enable any member again, and the client org enables the vendor's admins.
package vendoridle

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/logging"
)

const (
	// InactiveAfter is how long a vendor org membership may go unused.
	InactiveAfter = 90 * 24 * time.Hour
	// WarnAhead is how long before the disable the warning email goes out.
	WarnAhead = 7 * 24 * time.Hour

	// TaskSweep warns and disables the memberships that are due.
	TaskSweep = "scheduler:vendor-idle-sweep"

	// sweepBatch bounds one statement of the sweep; it loops until done.
	sweepBatch = 500

	// claimLease is how long a claimed warning is the claiming sweep's to
	// send; past it, a claim whose email never went out is free again. The
	// sweep runs hourly, so a lost claim delays a warning by about two hours.
	claimLease = time.Hour

	// releaseTimeout bounds recording a sent warning and giving back a
	// batch's unsent claims, which run even after the sweep's own context
	// ended.
	releaseTimeout = 10 * time.Second
)

// NewSweepTask is the periodic sweep task.
func NewSweepTask() *asynq.Task { return asynq.NewTask(TaskSweep, nil) }

// EmailSender delivers the warning email (notification.Sender satisfies it).
type EmailSender interface {
	SendEmail(ctx context.Context, to, subject string, body emaillayout.Email) error
}

// Sweeper runs the inactivity sweep.
type Sweeper struct {
	q       db.Querier
	sender  EmailSender
	baseURL string
	batch   int32 // rows per statement; sweepBatch outside tests
}

// New returns a Sweeper. sender may be nil: nobody is warned, so nobody is
// disabled either.
func New(q db.Querier, sender EmailSender, baseURL string) *Sweeper {
	return &Sweeper{q: q, sender: sender, baseURL: baseURL, batch: sweepBatch}
}

// RegisterHandlers mounts the sweep on mux.
func (s *Sweeper) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskSweep, func(ctx context.Context, _ *asynq.Task) error { return s.Sweep(ctx) })
}

// Sweep sends the warnings that are due, then disables the memberships whose
// warning is WarnAhead old and still unused. A failed email is logged and
// tried again on the next sweep.
func (s *Sweeper) Sweep(ctx context.Context) error {
	now := time.Now()
	if _, err := s.q.WithdrawUnusableVendorWarnings(ctx); err != nil {
		return fmt.Errorf("withdraw inactivity warnings: %w", err)
	}
	if err := s.warn(ctx, now.Add(-(InactiveAfter - WarnAhead))); err != nil {
		return err
	}
	var disabled int64
	for {
		n, err := s.q.DisableInactiveVendorMemberships(ctx, db.DisableInactiveVendorMembershipsParams{
			UnusedSince:  ts(now.Add(-InactiveAfter)),
			WarnedBefore: ts(now.Add(-WarnAhead)),
			Lim:          s.batch,
		})
		if err != nil {
			return fmt.Errorf("disable inactive vendor memberships: %w", err)
		}
		disabled += n
		if n < int64(s.batch) {
			break
		}
	}
	if disabled > 0 {
		logging.Info(ctx, "disabled unused vendor org memberships", "vendor-idle-disabled", logging.Count(int(disabled)))
	}
	return nil
}

// warn claims the warnings that are due a batch at a time, emails them, and
// sets each warning only once its email went out, so nobody is disabled on a
// warning they never got. Claims that were not sent (the email failed, the
// sweep's context ended first, or recording an earlier warning failed) are
// released in one statement on a bounded
// context that outlives the sweep's; one that cannot be released expires
// after claimLease. A batch with any failure ends the warnings until the next
// sweep.
func (s *Sweeper) warn(ctx context.Context, unusedSince time.Time) error {
	if s.sender == nil {
		return nil
	}
	for {
		due, err := s.q.ClaimInactivityWarnings(ctx, db.ClaimInactivityWarningsParams{
			UnusedSince: ts(unusedSince), ClaimsExpireBefore: ts(time.Now().Add(-claimLease)), Lim: s.batch,
		})
		if err != nil {
			return fmt.Errorf("claim inactivity warnings: %w", err)
		}
		var unsent []int64
		var markErr error
		for _, m := range due {
			if ctx.Err() == nil && markErr == nil {
				err := s.sendWarning(ctx, m)
				if err == nil {
					// The email went out: an unrecorded one expires with its
					// claim, so it is never released for a quick resend.
					markErr = s.markWarned(ctx, m.ID)
					continue
				}
				logging.Error(ctx, err, "sending the inactivity warning failed", "vendor-idle-warn-failed",
					logging.TenantID(m.TenantID), logging.UserID(m.UserID))
			}
			unsent = append(unsent, m.ID)
		}
		if err := s.release(ctx, unsent); err != nil {
			return errors.Join(err, markErr, ctx.Err())
		}
		if markErr != nil {
			return markErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(unsent) > 0 || len(due) < int(s.batch) {
			return nil
		}
	}
}

// markWarned records a warning whose email went out, on a bounded context
// that outlives the sweep's: the email is sent either way. A failure only
// means the claim expires and the person is warned twice.
func (s *Sweeper) markWarned(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := s.q.MarkInactivityWarned(ctx, id); err != nil {
		return fmt.Errorf("mark inactivity warned: %w", err)
	}
	return nil
}

// release gives back claims whose email was not sent.
func (s *Sweeper) release(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := s.q.ReleaseInactivityClaims(ctx, ids); err != nil {
		return fmt.Errorf("release inactivity claims: %w", err)
	}
	return nil
}

func (s *Sweeper) sendWarning(ctx context.Context, m db.ClaimInactivityWarningsRow) error {
	link := emaillayout.AppURL(s.baseURL, "/login")
	days := int(WarnAhead / (24 * time.Hour))
	body, err := emaillayout.Render(s.baseURL, emaillayout.Content{
		Heading: fmt.Sprintf("Keep your access to %s", m.TenantName),
		Body: emaillayout.Join(
			emaillayout.P("You have not used %s on OpenLoc since %s.", emaillayout.Strong(m.TenantName),
				m.LastActiveAt.Time.Format("January 2, 2006")),
			emaillayout.P("Sign in within %s days to keep your access. After that, your access to this organization is disabled, and an admin has to enable it again.", strconv.Itoa(days)),
		),
		Button: &emaillayout.Button{Label: "Sign in", URL: link},
		Notes:  emaillayout.LinkFallback(link),
	})
	if err != nil {
		return fmt.Errorf("render inactivity warning: %w", err)
	}
	return s.sender.SendEmail(ctx, m.Email, fmt.Sprintf("Your access to %s ends in %d days", m.TenantName, days), body)
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
