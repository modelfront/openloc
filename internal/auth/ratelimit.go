package auth

import (
	"net/http"

	"openloc/internal/httputil"
)

// TokenRateLimiter rate-limits by credential (access key or OAuth token)
// rather than by IP. An office behind one NAT shares an IP, while one runaway
// integration on its own IP would get a full allowance to itself, so the
// credential is the unit of accounting for API and MCP traffic.
type TokenRateLimiter struct {
	*httputil.KeyedLimiter[int64]
}

// NewTokenRateLimiter builds a per-credential limiter. name labels its
// sweeper in error reports. Credential ids come from our own database, so the
// key space is bounded and the map needs no cap.
func NewTokenRateLimiter(name string, rps float64, burst int) *TokenRateLimiter {
	return &TokenRateLimiter{httputil.NewKeyedLimiter[int64](rps, burst, 0, name)}
}

// Middleware returns 429 when the calling credential exceeds its budget.
//
// Must be chained AFTER Middleware, which puts the credential in the
// context. A browser session has no credential key and passes through
// untouched: the app's own request patterns are not the runaway-loop risk.
func (rl *TokenRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := PATInfoFromContext(r.Context())
		if info == nil {
			next.ServeHTTP(w, r)
			return
		}
		if ok, wait := rl.Take(info.RateLimitKey); !ok {
			httputil.TooManyRequests(w, wait, "too many requests for this key — slow down and retry")
			return
		}
		next.ServeHTTP(w, r)
	})
}
