package main

import (
	"context"
	"time"
)

// shutdownBudget bounds the shutdown from the signal to exit. It stays under
// the 30s the container runtime waits before SIGKILL (the pod's
// terminationGracePeriodSeconds, stop_grace_period in
// deploy/docker-compose.yml).
const shutdownBudget = 27 * time.Second

// The shutdown steps, in order, and how long each may take on its own.
const (
	asynqShutdownTimeout  = 8 * time.Second
	llmDebugFlushTimeout  = 10 * time.Second
	shutdownGrace         = 15 * time.Second
	notifBrokerCloseGrace = 2 * time.Second
	// reportFlushReserve is kept for sending the error reports.
	reportFlushReserve = 3 * time.Second
	// workerRequeueMargin keeps waiting for asynq past its own timeout, for
	// the requeue of the tasks it gave up on.
	workerRequeueMargin = 2 * time.Second
)

// shutdownLimits are the budget and each step's own limit.
type shutdownLimits struct {
	budget, workers, llmDebugFlush, httpGrace, brokerClose, reportFlush time.Duration
}

var serverShutdownLimits = shutdownLimits{
	budget:        shutdownBudget,
	workers:       asynqShutdownTimeout + workerRequeueMargin,
	llmDebugFlush: llmDebugFlushTimeout,
	httpGrace:     shutdownGrace,
	brokerClose:   notifBrokerCloseGrace,
	reportFlush:   reportFlushReserve,
}

// shutdownSteps are what the server does once it is told to stop, in order.
type shutdownSteps struct {
	// stopWorkers drains asynq. Its own ShutdownTimeout bounds the workers,
	// not the Redis calls around them, so the wait for it is bounded too, a
	// little past that timeout so the requeue of abandoned tasks completes.
	stopWorkers func()
	// flushLLMDebug is nil when the LLM debug log is not persisted.
	flushLLMDebug func(ctx context.Context)
	// drainHTTP returns an error when requests outlast ctx.
	drainHTTP    func(ctx context.Context) error
	closeBroker  func(ctx context.Context)
	flushReports func()
	closePool    func()
}

// shutdown runs the steps within l.budget and returns the HTTP drain's
// error. The worker drain, the LLM debug flush and the HTTP drain each end at
// their own limit or when only the broker's close and the reports are left of
// the budget, whichever comes first, so a slow step shortens the next instead
// of pushing the exit past SIGKILL. The reports go out before the pool closes:
// closing waits for every borrowed connection, and a handler still wedged
// after the drain may never give its back. The reports and the close share
// what is left of the budget. A step that outlasts its limit keeps running in
// the background until the process exits; asynq re-runs a task it gave up
// on.
func shutdown(l shutdownLimits, s shutdownSteps) error {
	return shutdownBy(time.Now().Add(l.budget), l, s)
}

// shutdownBy is shutdown with its budget ending at end.
func shutdownBy(end time.Time, l shutdownLimits, s shutdownSteps) error {
	brokerBy := end.Add(-l.reportFlush)
	drainBy := brokerBy.Add(-l.brokerClose)

	ctx, cancel := stepContext(drainBy, l.workers)
	runUntil(ctx, s.stopWorkers)
	cancel()
	if s.flushLLMDebug != nil {
		ctx, cancel := stepContext(drainBy, l.llmDebugFlush)
		s.flushLLMDebug(ctx)
		cancel()
	}
	ctx, cancel = stepContext(drainBy, l.httpGrace)
	httpErr := s.drainHTTP(ctx)
	cancel()
	ctx, cancel = stepContext(brokerBy, l.brokerClose)
	s.closeBroker(ctx)
	cancel()

	ctx, cancel = context.WithDeadline(context.Background(), end)
	defer cancel()
	runUntil(ctx, s.flushReports)
	runUntil(ctx, s.closePool)
	return httpErr
}

// runUntil runs f and returns when f does or when ctx ends, whichever comes
// first.
func runUntil(ctx context.Context, f func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// stepContext is the context of one shutdown step: it ends after the step's
// own limit, or at deadline if that comes first.
func stepContext(deadline time.Time, limit time.Duration) (context.Context, context.CancelFunc) {
	end := time.Now().Add(limit)
	if deadline.Before(end) {
		end = deadline
	}
	return context.WithDeadline(context.Background(), end)
}
