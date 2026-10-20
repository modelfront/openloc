// Package errtracktest captures Honeybadger notices in tests.
//
// It exists as a separate package (the net/http/httptest pattern) because two
// test suites need it — internal/errtrack's own, and internal/api's middleware
// tests — while production code must never link it.
//
// # Why a fake backend has to be built this way
//
// honeybadger.Backend cannot be implemented from outside the SDK: one of its
// two methods is `Event(events []*eventPayload) error`, and eventPayload is
// unexported, so the signature can't even be written here. The workaround is to
// embed honeybadger.TestBackend — which supplies a usable Event — and shadow
// Notify, whose signature uses only exported types.
//
// TestBackend alone is not enough: its own Notify is a no-op returning nil and
// records nothing (it only captures Insights events).
package errtracktest

import (
	"testing"

	"github.com/honeybadger-io/honeybadger-go"
	"openloc/internal/errtrack"
)

// Recorder captures notices instead of sending them.
type Recorder struct {
	honeybadger.TestBackend // supplies Event(); its Notify is shadowed below

	notices []*honeybadger.Notice
}

// Notify records the notice. Shadows TestBackend.Notify, which discards.
func (r *Recorder) Notify(_ honeybadger.Feature, payload honeybadger.Payload) error {
	if n, ok := payload.(*honeybadger.Notice); ok {
		r.notices = append(r.notices, n)
	}
	return nil
}

// Notices returns everything captured so far.
//
// No mutex: errtrack is initialised with Sync:true here, so notices are
// recorded on the calling goroutine and a test that reports concurrently would
// be racing its own assertions anyway. `go test -race` will say so if that
// assumption ever breaks.
func (r *Recorder) Notices() []*honeybadger.Notice { return r.notices }

// Len returns the number of captured notices.
func (r *Recorder) Len() int { return len(r.notices) }

// Reset clears captured notices, for tests that assert in phases.
func (r *Recorder) Reset() { r.notices = nil }

// Last returns the most recent notice, failing the test if there are none.
func (r *Recorder) Last(t testing.TB) *honeybadger.Notice {
	t.Helper()
	if len(r.notices) == 0 {
		t.Fatal("expected at least one notice, got none")
	}
	return r.notices[len(r.notices)-1]
}

// One returns the single captured notice, failing if the count isn't exactly 1.
// Most assertions want this rather than Last: "exactly one notice" is usually
// the property under test, and Last would happily pass on a double-report.
func (r *Recorder) One(t testing.TB) *honeybadger.Notice {
	t.Helper()
	if len(r.notices) != 1 {
		t.Fatalf("expected exactly 1 notice, got %d", len(r.notices))
	}
	return r.notices[0]
}

// Install initialises errtrack with a recording backend and restores the
// previous state via t.Cleanup. Sync:true so notices are captured before the
// call returns, instead of racing the SDK's buffered worker.
func Install(t testing.TB) *Recorder {
	t.Helper()
	rec := &Recorder{}
	errtrack.Init(errtrack.Options{
		APIKey:      "test-key",
		Environment: "test",
		Revision:    "testrev",
		Sync:        true,
		Backend:     rec,
	})
	t.Cleanup(errtrack.Disable)
	return rec
}
