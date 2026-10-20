package mcp

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// A cursor survives the round trip to the client exactly, to the microsecond
// Postgres stores, with and without a deadline.
func TestJobsCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 30, 15, 123456000, time.UTC)
	for name, c := range map[string]jobsCursor{
		"with a deadline": jobsCursorAfter(42, pgtype.Timestamptz{Time: at, Valid: true}),
		"no deadline":     jobsCursorAfter(7, pgtype.Timestamptz{}),
	} {
		got, err := parseJobsCursor(c.String())
		if err != nil {
			t.Fatalf("%s: parse %q: %v", name, c.String(), err)
		}
		if got.id != c.id || got.afterDeadline() != c.afterDeadline() {
			t.Errorf("%s: %q came back as %+v, want %+v", name, c.String(), got.afterDeadline(), c.afterDeadline())
		}
	}
	if got := jobsCursorAfter(7, pgtype.Timestamptz{}).afterDeadline(); got.InfinityModifier != pgtype.Infinity {
		t.Errorf("a job with no deadline must continue from 'infinity', got %+v", got)
	}
}

// Anything that is not a cursor this server wrote is refused, never read as
// the first page.
func TestParseJobsCursorRejectsGarbage(t *testing.T) {
	if c, err := parseJobsCursor(""); err != nil || c.id != 0 {
		t.Fatalf("empty cursor = %+v, %v; want the first page", c, err)
	}
	for _, raw := range []string{"42", "abc", "none.", "none.0", "none.-3", "12.x", "x.12", "1729.0", "."} {
		if _, err := parseJobsCursor(raw); err == nil {
			t.Errorf("cursor %q was accepted", raw)
		}
	}
}
