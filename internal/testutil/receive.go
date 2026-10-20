package testutil

import (
	"testing"
	"time"
)

// Within is how long a test waits for something that should arrive;
// generous for a loaded CI machine.
const Within = 5 * time.Second

// QuietFor is long enough for something that was sent to arrive.
const QuietFor = 500 * time.Millisecond

// Receive returns the next value from ch. The test fails when ch closes or
// nothing arrives within Within.
func Receive[T any](t testing.TB, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v, open := <-ch:
		if !open {
			t.Fatalf("%s: the channel closed", what)
		}
		return v
	case <-time.After(Within):
		t.Fatalf("%s: nothing within %s", what, Within)
	}
	var zero T
	return zero
}

// ReceiveNone fails the test when ch delivers a value or closes within
// QuietFor.
func ReceiveNone[T any](t testing.TB, ch <-chan T, what string) {
	t.Helper()
	select {
	case v, open := <-ch:
		if open {
			t.Fatalf("%s: unexpected %v", what, v)
		}
		t.Fatalf("%s: the channel closed", what)
	case <-time.After(QuietFor):
	}
}

// Closed fails the test unless ch closes within Within, with nothing sent
// before.
func Closed[T any](t testing.TB, ch <-chan T, what string) {
	t.Helper()
	select {
	case v, open := <-ch:
		if open {
			t.Fatalf("%s: got %v, want the channel closed", what, v)
		}
	case <-time.After(Within):
		t.Fatalf("%s: still open after %s", what, Within)
	}
}

// WaitFor polls cond until it holds, failing the test after Within.
func WaitFor(t testing.TB, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(Within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %s", what, Within)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
