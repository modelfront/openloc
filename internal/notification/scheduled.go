// Package notification, scheduled.go — periodic background notification
// dispatch. This file sets the periodic-task convention other packages
// follow.
//
// Architectural placement:
//   - The asynq task type lives here (not in `internal/worker/tasks.go`)
//     because notification-scheduled tasks belong to the notification
//     domain, not the pipeline-translation domain. The asynq.ServeMux in
//     cmd/server/main.go registers both worker.Handler (pipeline tasks)
//     AND notification.ScheduledHandler (this) — they coexist on the same
//     mux but stay in their own packages.
//   - Dedup is in SQL: `GetJobsApproachingDeadline` excludes jobs that
//     already received an EventDeadline notification within the last 20h.
//     Backed by the partial index `idx_notifications_deadline_dedup`
//     (migration 027) so the NOT EXISTS subquery is an index scan, not a
//     seq scan.
//   - The hourly cron can fire as often as it likes; the SQL prevents
//     flapping. So this handler stays simple: query → loop → publish.
//
// Service.Send guarantees row-first then email (see service.go), so the
// dedup window is owned BEFORE the external HTTP call — no chance of a
// duplicate-email-next-hour race even on rare DB or HTTP failures.

package notification

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/hibiken/asynq"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// TaskDeadlineCheck is the asynq task type for the hourly deadline scan.
// Producer: asynq.Scheduler in cmd/server/main.go.
// Consumer: ScheduledHandler.HandleDeadlineCheck below.
const TaskDeadlineCheck = "scheduler:deadline-check"

// NewDeadlineCheckTask returns the asynq task that the scheduler enqueues
// hourly. No payload — the handler queries DB for state.
func NewDeadlineCheckTask() *asynq.Task {
	return asynq.NewTask(TaskDeadlineCheck, nil)
}

// ScheduledHandler dispatches periodic notification scans.
//
// Two mutexes:
//   - runMu: serialises RunDeadlineCheck so the asynq cron firing and an
//     admin manual-trigger can't race past the SQL `NOT EXISTS` dedup
//     check. Without this, two concurrent calls both see "no recent
//     notification" before either inserts → duplicate row + duplicate
//     email this hour. asynq.Unique already prevents two cron firings
//     from racing each other across replicas, but the admin endpoint is
//     not deduped at the asynq layer, so this is the in-process gate.
//     Held for the full duration of one check (DB query + per-row Send) —
//     acceptable because the check completes in seconds at typical scale
//     and concurrent triggers are a corner case (admin clicking during a
//     cron tick).
//   - obsMu: protects the lastRun* observability fields, kept brief.
//     Separate from runMu so HandleHealth doesn't block on a long
//     in-progress run; ops should be able to read the previous run's
//     timestamp even mid-cron.
//
// Carries lightweight observability state (lastRun*) so /health/scheduler
// can answer "has the cron actually been firing?" — useful for detecting
// silent failures (Redis flap, scheduler goroutine crash) where the asynq
// Scheduler stops without surfacing an error.
type ScheduledHandler struct {
	queries db.Querier
	service *Service

	runMu sync.Mutex // serialises RunDeadlineCheck against itself

	obsMu             sync.Mutex
	lastRunAt         time.Time
	lastRunDispatched int
	enabled           bool // mirror of cfg.RunScheduler at startup; set by SetEnabled
}

func NewScheduledHandler(q db.Querier, s *Service) *ScheduledHandler {
	return &ScheduledHandler{queries: q, service: s}
}

// SetEnabled marks whether the scheduler is registered (RUN_SCHEDULER=true).
// Called from cmd/server/main.go after the scheduler decision is made; lets
// /health/scheduler distinguish "scheduler disabled by config" from
// "scheduler enabled but hasn't fired yet."
func (h *ScheduledHandler) SetEnabled(enabled bool) {
	h.obsMu.Lock()
	defer h.obsMu.Unlock()
	h.enabled = enabled
}

// RunDeadlineCheck does the actual work of one cron firing. Returns the
// count of notifications dispatched (after dedup + per-user prefs) so admin
// callers can show a "ran X notifications" response. Both the asynq handler
// and the manual-trigger admin endpoint go through this function — same
// behaviour, same SQL dedup, same per-row error isolation.
//
// Errors per-row are logged but don't fail the whole call — one recipient's
// email failure shouldn't block the others. The query-level error IS
// returned (so asynq retries the whole task; the manual-trigger handler
// surfaces it as a 500).
func (h *ScheduledHandler) RunDeadlineCheck(ctx context.Context) (dispatched int, err error) {
	return h.runDeadlineCheck(ctx, nil)
}

// RunDeadlineCheckForTenant is RunDeadlineCheck limited to one org's jobs.
func (h *ScheduledHandler) RunDeadlineCheckForTenant(ctx context.Context, tenantID int64) (dispatched int, err error) {
	return h.runDeadlineCheck(ctx, &tenantID)
}

// runDeadlineCheck scans every org when tenantID is nil, else that org only.
func (h *ScheduledHandler) runDeadlineCheck(ctx context.Context, tenantID *int64) (dispatched int, err error) {
	// Serialise concurrent invocations — see runMu doc on the struct. The
	// admin manual-trigger endpoint and the asynq cron firing can both
	// land here simultaneously; without this lock both pass the SQL dedup
	// check before either inserts and the user gets duplicate emails.
	h.runMu.Lock()
	defer h.runMu.Unlock()

	rows, err := h.queries.GetJobsApproachingDeadline(ctx, tenantID)
	if err != nil {
		logging.Error(ctx, err, "deadline lookup failed", "notification-deadline-lookup-failed")
		return 0, err
	}

	for _, r := range rows {
		// Skip jobs with no assigned user — nothing to notify. Should be
		// rare in practice (assignment is the precondition for most flows)
		// but defensive against incomplete data.
		if r.AssignedTo == nil {
			continue
		}

		// tenant_id comes straight from the JOIN — no extra round-trip.
		event := Event{
			Type:        EventDeadline,
			TenantID:    r.TenantID,
			RecipientID: *r.AssignedTo,
			ActorID:     0, // system-fired, no human actor
			JobID:       r.JobID,
			ProjectID:   r.ProjectID,
			ProjectName: r.ProjectName,
			FileName:    r.FileName,
			TargetLang:  r.TargetLanguage,
		}
		event.StepName = r.StepName
		if r.Deadline.Valid {
			event.DeadlineAt = r.Deadline.Time
		}

		if err := h.service.Send(ctx, event); err != nil {
			logging.Warn(ctx, err, "deadline notification not sent", "notification-deadline-send-failed", logging.JobID(r.JobID))
			// continue — don't let one failure block the others
			continue
		}
		dispatched++
	}

	// Record observability state so /health/scheduler can answer "has the
	// cron been firing?" Updated even when dispatched=0 (a successful run
	// that found nothing to send is still a successful run).
	h.obsMu.Lock()
	h.lastRunAt = time.Now()
	h.lastRunDispatched = dispatched
	h.obsMu.Unlock()

	return dispatched, nil
}

// HandleDeadlineCheck is the asynq adapter — wraps RunDeadlineCheck in the
// signature the asynq mux expects. Returns nil unconditionally on per-row
// failures so asynq doesn't retry the whole task (the SQL dedup window
// gives us 20h of forgiveness for any rows we missed).
func (h *ScheduledHandler) HandleDeadlineCheck(ctx context.Context, _ *asynq.Task) error {
	_, err := h.RunDeadlineCheck(ctx)
	return err
}

// RegisterHandlers wires this handler's tasks onto the shared asynq mux.
// Called from cmd/server/main.go alongside worker.Handler.RegisterHandlers.
func (h *ScheduledHandler) RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskDeadlineCheck, h.HandleDeadlineCheck)
}

// HandleHealth is a public (unauthenticated, like /health) endpoint that
// reports the scheduler's last firing for monitoring. Returns:
//
//	{enabled: bool, last_run_at: RFC3339 string OR null, last_run_dispatched: int}
//
// `enabled=true` + `last_run_at=null` after the server has been up for >1h
// means the cron silently stopped firing (scheduler goroutine crash, Redis
// flap with asynq.Unique permanent rejection, etc). Ops dashboards should
// alert on `enabled=true && (now - last_run_at) > 2h`.
func (h *ScheduledHandler) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	h.obsMu.Lock()
	enabled := h.enabled
	lastRunAt := h.lastRunAt
	lastRunDispatched := h.lastRunDispatched
	h.obsMu.Unlock()

	resp := map[string]any{
		"enabled":             enabled,
		"last_run_dispatched": lastRunDispatched,
	}
	if lastRunAt.IsZero() {
		resp["last_run_at"] = nil
	} else {
		resp["last_run_at"] = lastRunAt.UTC().Format(time.RFC3339)
	}
	httputil.JSON(w, http.StatusOK, resp)
}

// HandleManualDeadlineCheck is the org owner/admin HTTP endpoint that runs the
// deadline check for the caller's org synchronously and returns a count.
// Useful for:
//   - Dev visibility (the cron is silent for up to 59 minutes after server
//     start; this lets ops + devs trigger it on demand)
//   - Ops debugging ("did the cron actually fire? was anyone deduped?")
//   - Smoke tests after deploy
//
// Sync (not enqueued) so the admin gets immediate feedback. The SQL dedup
// owns "did this user already get a notification this hour" — running
// manually + the cron firing in the same hour produces ONE notification,
// not two.
func (h *ScheduledHandler) HandleManualDeadlineCheck(w http.ResponseWriter, r *http.Request) {
	dispatched, err := h.RunDeadlineCheckForTenant(r.Context(), auth.TenantIDFromContext(r.Context()))
	if err != nil {
		httputil.ServerError(w, "run", "deadline check")
		return
	}
	httputil.Data(w, map[string]any{
		"dispatched": dispatched,
	})
}
