package pipeline_test

import (
	"testing"

	"openloc/internal/pipeline"
)

// TestNewServicePanicsOnNilTxRunner pins that NewService panics on a nil
// TxRunner: SaveSegmentToTM needs it for upsert + history-insert atomicity,
// and a nil txr would silently mean "no tx wrapping".
//
// Anti-tautology: removing the `if txr == nil { panic(...) }` check in
// NewService makes this test fail with "expected NewService to panic, but
// it didn't" — proving the guard is what catches the misuse.
func TestNewServicePanicsOnNilTxRunner(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected NewService to panic on nil txr, but it didn't")
		}
	}()
	pipeline.NewService(nil, nil, nil)
}

// TestNewPgxTxRunnerPanicsOnNilPool enforces the same guard at the runner
// constructor: NewPgxTxRunner with a nil pool is a programming error
// (production wiring should never hit this).
func TestNewPgxTxRunnerPanicsOnNilPool(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected NewPgxTxRunner to panic on nil pool, but it didn't")
		}
	}()
	pipeline.NewPgxTxRunner(nil)
}
