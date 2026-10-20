package errtrack

import (
	"context"
	"fmt"

	"openloc/internal/logging"
)

// Panics outside an HTTP request are the codebase's largest reporting blind
// spot, and it splits in two.
//
// # Unrecovered goroutines
//
// A panic in a goroutine cannot be recovered by the code that spawned it, so
// neither middleware.Recoverer nor honeybadger.Handler ever sees it — the
// process dies and Kubernetes restarts the pod with nothing recorded anywhere.
// Goroutines spawned inside an HTTP request (SSE keepalives, request fan-outs)
// are the worst case: one malformed input takes down every in-flight request
// on the replica.
//
// Monitor closes that gap while preserving the crash: it reports, flushes, and
// re-panics. Deliberately NOT "report and swallow" — a dead sweeper goroutine
// that leaves the process running would trade a loud restart for silently
// unbounded memory growth, which is worse.
//
// # Deliberately recovered panics
//
// Some sites recover on purpose so the request survives and
// log the panic. CapturePanic adds the notice without changing that control
// flow, because a recovered panic is still a bug — it just isn't an outage.
//
// Neither uses honeybadger.Monitor(), the SDK's equivalent. That reports through
// the global client, bypassing the `enabled` gate that exists precisely so a
// test binary on a machine with HONEYBADGER_API_KEY set cannot fire live
// notices, and it leaves ErrorClass as the Go type — which for a panic value is
// usually `string`, collapsing every panic in the service into one issue.

// ClassBackgroundPanic covers every panic reported from a deferred recover,
// whether or not the process survives it. Distinct from ClassRequestPanic, which
// honeybadger.Handler raises for a panic that killed an HTTP request: these
// happen in goroutines and background loops where no request is being answered,
// and they want separate alerting.
const ClassBackgroundPanic = "BackgroundPanic"

// panicStackSkip is how many frames come off the top of the stack captured
// inside a deferred recover, so the backtrace starts at the line that panicked
// rather than in this package.
//
// The value is empirical, and TestPanicNoticePointsAtPanicSite is authoritative
// over this comment. It holds for both entry points only because Monitor
// delegates to CapturePanic: CapturePanic is small enough to be inlined into its
// caller, while Monitor cannot be (recover() blocks inlining), so calling
// capturePanic from each directly would put their panic sites one frame apart.
// That test exercises both paths for this reason.
//
// An off-by-one is invisible in production — the notice still arrives, its
// backtrace just starts at runtime.gopanic or inside errtrack. Only the test
// catches it.
const panicStackSkip = 6

// Monitor reports a panic and then re-panics, leaving the process to die exactly
// as it would have. Use it as the first deferred call in every goroutine:
//
//	go func() {
//		defer errtrack.Monitor(ctx, "chat", "sse-keepalive")
//		…
//	}()
//
// ctx may be a background context; the logging.Scope is used when present.
func Monitor(ctx context.Context, subsystem, event string) {
	rec := recover()
	if rec == nil {
		return
	}
	// Gated here as well as inside capturePanic so a disabled process never
	// touches the SDK at all — Flush would otherwise block on the global
	// client's worker on every panic in every test binary.
	if enabled.Load() {
		// Routed through CapturePanic rather than capturePanic so both entry
		// points sit at the same stack depth — see panicStackSkip.
		CapturePanic(ctx, rec, subsystem, event)

		// Delivery is asynchronous and this goroutine is about to take the
		// process down, so without a flush the notice is dropped — the one case
		// where it matters most. Same reasoning as cmd/server's fatal path.
		Flush()
	}

	panic(rec)
}

// CapturePanic reports a recovered panic without re-panicking. For the sites
// that recover on purpose and mean to keep running; call it from inside the
// deferred function, after recover() has returned non-nil.
func CapturePanic(ctx context.Context, rec any, subsystem, event string) {
	if rec == nil {
		return
	}
	capturePanic(ctx, rec, subsystem, event)
}

func capturePanic(ctx context.Context, rec any, subsystem, event string) {
	if !enabled.Load() {
		return
	}
	defer func() {
		// Reporting a panic must not panic. Without this a marshalling failure
		// inside the SDK would replace the original panic with a confusing one.
		if again := recover(); again != nil {
			logging.Error(ctx, nil, fmt.Sprintf("panic while reporting panic: %v", again), "errtrack-panic-report-failed")
		}
	}()

	notify(ctx, panicError(rec), CaptureOptions{
		Class:     ClassBackgroundPanic,
		Message:   fmt.Sprintf("panic in %s (%s)", subsystem, event),
		Subsystem: subsystem,
		Event:     event,
	}, panicStackSkip)
}

// panicError converts a recovered value to an error. A panic value is any, and
// is most often a string or a runtime.Error; fmt.Errorf keeps the text either
// way while giving notify something implementing error.
func panicError(rec any) error {
	if err, ok := rec.(error); ok {
		return err
	}
	return fmt.Errorf("%v", rec)
}
