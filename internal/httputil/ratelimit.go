package httputil

import (
	"math"
	"net/http"
	"strconv"
	"time"
)

// RateLimiter is a per-IP rate limiter middleware: `burst` requests at once,
// then `rps` per second (a KeyedLimiter keyed by client IP).
type RateLimiter struct {
	ips *KeyedLimiter[string]
}

// NewRateLimiter creates a rate limiter that allows `rps` requests per second
// with a burst of `burst` requests. Stale entries are cleaned up periodically.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	return &RateLimiter{ips: NewKeyedLimiter[string](rps, burst, maxTrackedIPs, "httputil")}
}

// TooManyRequests writes a 429 with a Retry-After header (whole seconds,
// rounded up) and the machine-readable code "rate_limited".
func TooManyRequests(w http.ResponseWriter, retryAfter time.Duration, message string) {
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	ErrorCode(w, http.StatusTooManyRequests, "rate_limited", message)
}

// maxTrackedIPs bounds the bucket map. A distributed spam wave (or leftover
// forgeable-key traffic) must not grow memory without limit; resetting is
// acceptable — brief amnesty beats OOM. Same strategy as the errtrack
// fingerprint limiter.
const maxTrackedIPs = 100_000

// Middleware returns an HTTP middleware that rate-limits by client IP.
// Returns 429 Too Many Requests when the limit is exceeded.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := rl.ips.Take(clientIP(r)); !ok {
			TooManyRequests(w, wait, "too many requests — please try again later")
			return
		}
		next.ServeHTTP(w, r)
	})
}
