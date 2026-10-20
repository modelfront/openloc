package errtrack

import (
	"errors"
	"testing"
	"time"

	"github.com/honeybadger-io/honeybadger-go"
)

// A fingerprint that used up its burst stays refused until limiterWindow has
// passed since its window opened, then is allowed again.
func TestRateLimiterWindowResets(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	l := newRateLimiter(func() time.Time { return now })
	n := &honeybadger.Notice{Fingerprint: "windowed"}

	for i := range limiterBurst {
		if err := l.allow(n); err != nil {
			t.Fatalf("notice %d of the burst was refused: %v", i+1, err)
		}
	}
	if err := l.allow(n); !errors.Is(err, errRateLimited) {
		t.Fatalf("notice past the burst: want errRateLimited, got %v", err)
	}

	now = now.Add(limiterWindow - time.Nanosecond)
	if err := l.allow(n); !errors.Is(err, errRateLimited) {
		t.Fatalf("notice just before the window ends: want errRateLimited, got %v", err)
	}

	now = now.Add(time.Nanosecond)
	if err := l.allow(n); err != nil {
		t.Fatalf("notice once the window ended was refused: %v", err)
	}
}
