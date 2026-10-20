package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack"
	"openloc/internal/logging"
)

// ErrorHandler returns the asynq.ErrorHandler wired into the server in
// cmd/server/main.go.
//
// Only terminal failures are reported. asynq retries on a 1m/2m/4m backoff, so
// reporting every attempt would multiply one broken task into four notices —
// and Honeybadger counts every occurrence with no server-side dedup, against a
// quota shared with every other service in the org. Transient attempts are
// logged and left alone; the notice fires when retries are exhausted, or
// immediately for a panic, which no retry will fix.
//
// Deliberately no honeybadger.Monitor() in the task handlers themselves:
// Monitor re-panics by design, asynq's own recovery converts that into an error
// which arrives here with IsPanicError true, and the panic would be reported
// twice. This handler alone is sufficient.
//
// q resolves the triggering user's email for the notice (user_id + user_email
// in Honeybadger, matching the HTTP paths). Nil-safe like the other optional
// worker wiring: a nil querier skips the email, never the notice.
func ErrorHandler(q db.Querier) asynq.ErrorHandler {
	return asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
		reportTaskError(ctx, q, t, err)
	})
}

func reportTaskError(ctx context.Context, q db.Querier, t *asynq.Task, err error) {
	retried, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	isPanic := asynq.IsPanicError(err)
	terminal := terminalFailure(retried, maxRetry, err)

	// Tasks run on their own goroutines from a background context, so there is
	// no request scope to inherit — create one here so the task's identity
	// lands on both the log lines and the notice.
	ctx, scope := logging.WithScope(ctx)
	scope.Set(logging.TaskType(t.Type()))
	tenantID, triggeredBy := identityFromPayload(t.Payload())
	if tenantID != 0 {
		scope.Set(logging.TenantID(tenantID))
	}
	if triggeredBy != 0 {
		scope.Set(logging.UserID(triggeredBy))
	}
	if id, ok := asynq.GetTaskID(ctx); ok {
		scope.Set(logging.Label("task_id", id))
	}

	if !terminal {
		logging.Warn(ctx, err, "task failed, will retry", "worker-task-retry",
			logging.Label("retry", fmt.Sprintf("%d/%d", retried, maxRetry)))
		return
	}

	// Terminal only: resolve the triggering user's email for the notice
	// (user_email next to user_id in Honeybadger, matching the HTTP paths).
	// Best-effort — a failed lookup drops the email, never the notice. GetUser
	// is unscoped by design: triggered_by comes from a server-generated payload
	// (never a client), same justification as the worker's audit events.
	if q != nil && triggeredBy != 0 {
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if u, uErr := q.GetUser(lookupCtx, triggeredBy); uErr == nil {
			scope.SetUserEmail(u.Email)
		}
	}

	action := "worker-task-failed"
	message := fmt.Sprintf("task %s failed permanently after %d retries", t.Type(), retried)
	if isPanic {
		action = "worker-task-panic"
		message = fmt.Sprintf("task %s panicked", t.Type())
	}

	logging.Error(ctx, err, message, action)
	errtrack.Capture(ctx, err, errtrack.CaptureOptions{
		Class:     errtrack.ClassWorkerTaskFail,
		Message:   message,
		Subsystem: "worker",
		Event:     t.Type(),
	})
}

// terminalFailure reports whether asynq is done with a task that returned err
// on its retried-th retry. A SkipRetry error ends it on any attempt.
func terminalFailure(retried, maxRetry int, err error) bool {
	return asynq.IsPanicError(err) || errors.Is(err, asynq.SkipRetry) || retried >= maxRetry
}

// identityFromPayload pulls tenant_id and triggered_by out of any task payload.
// Every payload type carries tenant_id under that JSON key (PipelinePayload,
// AnalysisPayload) and every handler already validates it non-zero, so one
// shared shape covers all task types without a switch that would need editing
// per new task. triggered_by is optional (system-triggered tasks omit it).
//
// Returns zeros on any decode failure: a notice with a missing identity is far
// better than no notice at all.
func identityFromPayload(payload []byte) (tenantID, triggeredBy int64) {
	var p struct {
		TenantID    int64  `json:"tenant_id"`
		TriggeredBy *int64 `json:"triggered_by"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return 0, 0
	}
	if p.TriggeredBy != nil {
		triggeredBy = *p.TriggeredBy
	}
	return p.TenantID, triggeredBy
}
