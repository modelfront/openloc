package httputil

import (
	"net"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5/middleware"

	"openloc/internal/logging"
)

// ClientIP returns the middleware that decides each request's client IP, which
// the per-IP rate limiters key on. trustedProxyHops is TRUSTED_PROXY_HOPS: how
// many reverse proxies sit between the internet and this server.
//
//   - 0: no proxy. The TCP peer is the client and every forwarding header is
//     ignored, because the client wrote it.
//   - N: the last N X-Forwarded-For entries were added by our proxies, and the
//     N-th from the right is the client. Other forwarding headers are ignored.
//
// Each misconfiguration logs one warning per process: at 0, the first request
// that carries any forwarding header (a proxy the setting doesn't know about);
// at N, the first request that carries X-Real-IP or True-Client-IP without a
// usable X-Forwarded-For chain (a proxy that doesn't send X-Forwarded-For).
// Requests with no forwarding header, such as health checks that reach the
// server directly, never warn.
func ClientIP(trustedProxyHops int) func(http.Handler) http.Handler {
	var warned atomic.Bool
	if trustedProxyHops > 0 {
		fromXFF := middleware.ClientIPFromXFFTrustedProxies(trustedProxyHops)
		return func(next http.Handler) http.Handler {
			return fromXFF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if middleware.GetClientIP(r.Context()) == "" && hasOtherForwardingHeader(r) && warned.CompareAndSwap(false, true) {
					logging.Warn(r.Context(), nil,
						"request has X-Real-IP or True-Client-IP but no usable X-Forwarded-For, so rate limits key on the proxy's address; configure the proxy to send X-Forwarded-For",
						"client-ip-forwarded-for-missing")
				}
				next.ServeHTTP(w, r)
			}))
		}
	}
	return func(next http.Handler) http.Handler {
		h := middleware.ClientIPFromRemoteAddr(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if (r.Header.Get("X-Forwarded-For") != "" || hasOtherForwardingHeader(r)) && warned.CompareAndSwap(false, true) {
				logging.Warn(r.Context(), nil,
					"request has a forwarding header but TRUSTED_PROXY_HOPS is 0, so rate limits key on the proxy's address; behind a reverse proxy, set TRUSTED_PROXY_HOPS to the number of proxies and have them send X-Forwarded-For",
					"client-ip-proxy-header-ignored")
			}
			h.ServeHTTP(w, r)
		})
	}
}

// hasOtherForwardingHeader reports whether r carries a single-IP forwarding
// header, which ClientIP never trusts.
func hasOtherForwardingHeader(r *http.Request) bool {
	return r.Header.Get("X-Real-IP") != "" || r.Header.Get("True-Client-IP") != ""
}

// clientIP is the rate-limit key: the IP that ClientIP stored, or the
// connection's host when it stored none (no ClientIP middleware, or an
// X-Forwarded-For chain too short or unparseable to name a client). Keying on
// the host, not host:port, keeps the limit per IP rather than per connection.
func clientIP(r *http.Request) string {
	if ip := middleware.GetClientIP(r.Context()); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
