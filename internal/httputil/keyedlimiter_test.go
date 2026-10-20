package httputil

import (
	"testing"
	"time"
)

// spend takes n tokens from key and fails the test if any is refused.
func spend(t *testing.T, l *KeyedLimiter[int64], key int64, n int) {
	t.Helper()
	for i := range n {
		if !l.Allow(key) {
			t.Fatalf("key %d: call %d of the burst refused", key, i+1)
		}
	}
}

func TestKeyedLimiterKeepsOneBucketPerKey(t *testing.T) {
	l := NewKeyedLimiter[int64](1.0/60, 3, 0, "test")
	spend(t, l, 1, 3)
	if l.Allow(1) {
		t.Error("key 1 allowed past its burst")
	}
	spend(t, l, 2, 3)
}

// A full map resets: every key starts again with a full bucket.
func TestKeyedLimiterResetsWhenFull(t *testing.T) {
	l := NewKeyedLimiter[int64](1.0/60, 1, 2, "test")
	spend(t, l, 1, 1)
	spend(t, l, 2, 1)
	if l.Allow(1) {
		t.Fatal("key 1 allowed past its burst before the map is full")
	}
	spend(t, l, 3, 1) // the third key resets the map
	spend(t, l, 1, 1)
}

// The invite email budget refills in 20 minutes, longer than a bucket stays
// idle before a sweep. A spent bucket must survive the sweep, or idling
// restores the whole burst early.
func TestKeyedLimiterSweepKeepsASpentBudget(t *testing.T) {
	l := NewKeyedLimiter[int64](1.0/60, 20, 0, "test")
	spend(t, l, 1, 20)
	// Eleven idle minutes refill 11 of the 20 tokens.
	l.sweep(time.Now().Add(11 * time.Minute))
	if l.Allow(1) {
		t.Error("the sweep reset a spent budget: a call right after it was allowed")
	}
}

// A bucket that has refilled is the same as a new one, so the sweep drops it.
func TestKeyedLimiterSweepDropsFullBuckets(t *testing.T) {
	l := NewKeyedLimiter[int64](1.0/60, 20, 0, "test")
	spend(t, l, 1, 1)
	spend(t, l, 2, 20)
	l.sweep(time.Now().Add(2 * time.Minute))
	l.mu.Lock()
	_, kept1 := l.buckets[1]
	_, kept2 := l.buckets[2]
	l.mu.Unlock()
	if kept1 {
		t.Error("key 1 refilled to full but was kept")
	}
	if !kept2 {
		t.Error("key 2 is still spent but was dropped")
	}
}
