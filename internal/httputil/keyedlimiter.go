package httputil

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"openloc/internal/errtrack"
)

// KeyedLimiter keeps one token bucket per key: burst calls at once, then rps
// per second. The per-IP, per-access-token and per-user limiters are all this
// type; they differ only in the key (client IP, access token, user). Every
// limiterSweepInterval, buckets that have refilled to full are dropped: a full
// bucket is the same as a new one, so the sweep frees memory without giving
// anyone budget back early. With maxKeys > 0 the map resets when it is full: a
// brief amnesty beats unbounded memory when keys can be minted by a client.
type KeyedLimiter[K comparable] struct {
	mu      sync.Mutex
	buckets map[K]*rate.Limiter
	rps     rate.Limit
	burst   int
	maxKeys int
}

const limiterSweepInterval = 5 * time.Minute

// NewKeyedLimiter builds the limiter and starts its sweep. subsystem names the
// owner in the sweeper's panic report.
func NewKeyedLimiter[K comparable](rps float64, burst, maxKeys int, subsystem string) *KeyedLimiter[K] {
	l := &KeyedLimiter[K]{
		buckets: make(map[K]*rate.Limiter),
		rps:     rate.Limit(rps),
		burst:   burst,
		maxKeys: maxKeys,
	}
	go func() {
		// Background context: this sweeper outlives every request, so there is
		// no scope to inherit. Monitor re-panics, so a bug here still takes the
		// process down, but is reported first.
		defer errtrack.Monitor(context.Background(), subsystem, "ratelimit-sweeper")
		for {
			time.Sleep(limiterSweepInterval)
			l.sweep(time.Now())
		}
	}()
	return l
}

// sweep drops the buckets that are full at now.
func (l *KeyedLimiter[K]) sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.buckets {
		if b.TokensAt(now) >= float64(l.burst) {
			delete(l.buckets, k)
		}
	}
}

// Allow takes one token from key's bucket and reports whether there was one.
func (l *KeyedLimiter[K]) Allow(key K) bool {
	ok, _ := l.Take(key)
	return ok
}

// Take is Allow that also says, when it refuses, how long until key's bucket
// has a token again: the Retry-After of a 429.
func (l *KeyedLimiter[K]) Take(key K) (bool, time.Duration) {
	res := l.bucket(key).Reserve()
	if !res.OK() {
		return false, time.Second
	}
	if d := res.Delay(); d > 0 {
		res.Cancel()
		return false, d
	}
	return true, 0
}

func (l *KeyedLimiter[K]) bucket(key K) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		if l.maxKeys > 0 && len(l.buckets) >= l.maxKeys {
			l.buckets = make(map[K]*rate.Limiter)
		}
		b = rate.NewLimiter(l.rps, l.burst)
		l.buckets[key] = b
	}
	return b
}
