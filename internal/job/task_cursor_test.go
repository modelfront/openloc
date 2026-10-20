package job

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestTaskCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 6, 14, 30, 15, 123456000, time.UTC)
	cases := []struct {
		name     string
		deadline pgtype.Timestamptz
	}{
		{"finite deadline", pgtype.Timestamptz{Time: at, Valid: true}},
		{"no deadline", pgtype.Timestamptz{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeTaskCursor(encodeTaskCursor(tc.deadline, 42))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.JobID != 42 {
				t.Errorf("job id = %d, want 42", got.JobID)
			}
			if !got.Deadline.Valid {
				t.Fatal("decoded deadline must be valid (a real time or infinity)")
			}
			if tc.deadline.Valid {
				if got.Deadline.InfinityModifier != pgtype.Finite || !got.Deadline.Time.Equal(at) {
					t.Errorf("deadline = %+v, want %v", got.Deadline, at)
				}
			} else if got.Deadline.InfinityModifier != pgtype.Infinity {
				t.Errorf("missing deadline must decode to infinity, got %+v", got.Deadline)
			}
		})
	}
}

func TestDecodeTaskCursorRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "abc", "123", "inf:", ":5", "inf:0", "inf:-3", "x:5", "1.5:5", "inf:5:6"} {
		if _, err := decodeTaskCursor(s); err == nil {
			t.Errorf("decodeTaskCursor(%q) = nil error, want error", s)
		}
	}
}
