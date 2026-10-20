package errtrack_test

import (
	"context"
	"strings"
	"testing"

	"openloc/internal/errtrack"
	"openloc/internal/errtrack/errtracktest"
)

// panicDeepInside is the function the backtrace must point at. Named so the
// assertion below reads unambiguously.
func panicDeepInside() { panic("sweeper exploded") }

// TestMonitorReportsAndRepanics pins both halves of Monitor's contract. Report
// alone is not enough: a goroutine panic currently kills the process, and
// swallowing it instead would turn a loud restart into a silently dead worker.
func TestMonitorReportsAndRepanics(t *testing.T) {
	rec := errtracktest.Install(t)

	var repanicked any
	func() {
		defer func() { repanicked = recover() }()
		func() {
			defer errtrack.Monitor(context.Background(), "httputil", "ratelimit-sweeper")
			panicDeepInside()
		}()
	}()

	if repanicked == nil {
		t.Fatal("Monitor swallowed the panic — the process must still die")
	}
	if got, ok := repanicked.(string); !ok || got != "sweeper exploded" {
		t.Errorf("re-panicked with %#v, want the original value", repanicked)
	}

	n := rec.One(t)
	if n.ErrorClass != errtrack.ClassBackgroundPanic {
		t.Errorf("ErrorClass = %q, want %q", n.ErrorClass, errtrack.ClassBackgroundPanic)
	}
	if !strings.Contains(n.ErrorMessage, "sweeper exploded") {
		t.Errorf("ErrorMessage = %q, want the panic value", n.ErrorMessage)
	}
	if !strings.Contains(n.ErrorMessage, "ratelimit-sweeper") {
		t.Errorf("ErrorMessage = %q, want the event naming which goroutine died", n.ErrorMessage)
	}
}

// TestMonitorNoPanicIsInert — Monitor runs as a deferred call on every normal
// return too, so it must be free when nothing went wrong.
func TestMonitorNoPanicIsInert(t *testing.T) {
	rec := errtracktest.Install(t)

	func() {
		defer errtrack.Monitor(context.Background(), "chat", "sse-keepalive")
	}()

	if rec.Len() != 0 {
		t.Errorf("got %d notices on a clean return, want 0", rec.Len())
	}
}

// TestCapturePanicDoesNotRepanic — the deliberately-recovered sites keep
// serving the request. Adding reporting must not change that.
func TestCapturePanicDoesNotRepanic(t *testing.T) {
	rec := errtracktest.Install(t)

	survived := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				errtrack.CapturePanic(context.Background(), r, "notification", "broker-publish")
				survived = true
			}
		}()
		panicDeepInside()
	}()

	if !survived {
		t.Fatal("CapturePanic must not re-panic")
	}
	if rec.Len() != 1 {
		t.Fatalf("got %d notices, want 1", rec.Len())
	}
}

// TestPanicNoticePointsAtPanicSite is the one that earns its keep.
//
// The whole reason these notices are worth having is the backtrace — unlike
// HTTPServerError, a panic notice IS raised while the failing frames are still
// on the stack, so it can name the line that broke. That depends on
// panicStackSkip being right, and an off-by-one silently points every panic
// notice at this package instead. Nothing else would catch it.
func TestPanicNoticePointsAtPanicSite(t *testing.T) {
	// Both entry points, because they reach capturePanic through a different
	// intermediate frame and inlining could make their depths diverge.
	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"CapturePanic", func() {
			defer func() {
				if r := recover(); r != nil {
					errtrack.CapturePanic(context.Background(), r, "test", "skip-calibration")
				}
			}()
			panicDeepInside()
		}},
		{"Monitor", func() {
			defer func() { _ = recover() }()
			func() {
				defer errtrack.Monitor(context.Background(), "test", "skip-calibration")
				panicDeepInside()
			}()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := errtracktest.Install(t)
			tc.run()

			n := rec.One(t)
			if len(n.Backtrace) == 0 {
				t.Fatal("panic notice has no backtrace — it must, unlike HTTPServerError")
			}
			top := n.Backtrace[0]
			if !strings.Contains(top.Method, "panicDeepInside") {
				t.Errorf("backtrace starts at %s (%s:%s), want panicDeepInside — panicStackSkip is off",
					top.Method, top.File, top.Number)
			}
			// Neither the runtime's panic machinery nor errtrack's own frames
			// may sit above the frame that actually broke. Matched on the
			// package-qualified prefix, not the substring "errtrack", which the
			// test package's own path contains.
			if strings.Contains(top.Method, "gopanic") ||
				strings.Contains(top.Method, "internal/errtrack.") {
				t.Errorf("backtrace starts at %s, want the application frame", top.Method)
			}
		})
	}
}

// TestBackgroundPanicKeepsItsBacktrace guards the interaction with
// dropMisleadingBacktrace: HTTPServerError and WorkerTaskFailed have their
// stacks replaced because theirs are meaningless, and ClassBackgroundPanic must
// NOT get caught by that, since its stack is the entire point.
func TestBackgroundPanicKeepsItsBacktrace(t *testing.T) {
	rec := errtracktest.Install(t)

	func() {
		defer func() {
			if r := recover(); r != nil {
				errtrack.CapturePanic(context.Background(), r, "mcp", "audit-write")
			}
		}()
		panicDeepInside()
	}()

	n := rec.One(t)
	if len(n.Backtrace) == 1 && strings.Contains(n.Backtrace[0].File, "no backtrace") {
		t.Fatal("ClassBackgroundPanic was caught by dropMisleadingBacktrace — its stack is real")
	}
}

// TestPanicReportingDisabledSendsNothing — same fail-safe posture as Capture: a
// process that never called Init must not report, whatever the environment says.
func TestPanicReportingDisabledSendsNothing(t *testing.T) {
	rec := errtracktest.Install(t)
	errtrack.Disable()

	func() {
		defer func() { _ = recover() }()
		func() {
			defer errtrack.Monitor(context.Background(), "chat", "sse-keepalive")
			panicDeepInside()
		}()
	}()

	if rec.Len() != 0 {
		t.Errorf("got %d notices while disabled, want 0", rec.Len())
	}
}
