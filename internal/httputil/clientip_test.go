package httputil

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/logging"
)

// limited returns ClientIP(hops) in front of a 1-request limiter, the way
// NewRouter stacks them, and a helper that sends one request.
func limited(t *testing.T, hops int) func(remoteAddr string, headers map[string]string) int {
	t.Helper()
	rl := NewRateLimiter(0.001, 1)
	h := ClientIP(hops)(rl.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	return func(remoteAddr string, headers map[string]string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = remoteAddr
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
}

// With no proxy in front, every forwarding header is client-written: none of
// them may give the same connection a fresh bucket.
func TestClientIP_NoProxyIgnoresForwardingHeaders(t *testing.T) {
	do := limited(t, 0)
	if code := do("203.0.113.9:1111", nil); code != http.StatusOK {
		t.Fatalf("first request = %d", code)
	}
	for _, h := range []map[string]string{
		{"X-Forwarded-For": "198.51.100.1"},
		{"X-Forwarded-For": "198.51.100.2, 198.51.100.3"},
		{"X-Real-IP": "198.51.100.4"},
		{"True-Client-IP": "198.51.100.5"},
	} {
		if code := do("203.0.113.9:2222", h); code != http.StatusTooManyRequests {
			t.Errorf("headers %v got a fresh bucket: %d", h, code)
		}
	}
	if code := do("203.0.113.10:1111", nil); code != http.StatusOK {
		t.Errorf("another connection address was throttled by the first: %d", code)
	}
}

// One proxy: the last X-Forwarded-For entry is the one our proxy wrote.
func TestClientIP_OneProxyUsesLastForwardedEntry(t *testing.T) {
	do := limited(t, 1)
	proxy := "10.0.0.5:4000"
	if code := do(proxy, map[string]string{"X-Forwarded-For": "198.51.100.1, 203.0.113.9"}); code != http.StatusOK {
		t.Fatalf("first request = %d", code)
	}
	for _, h := range []map[string]string{
		{"X-Forwarded-For": "198.51.100.99, 203.0.113.9"},
		{"X-Forwarded-For": "203.0.113.9", "X-Real-IP": "198.51.100.7"},
	} {
		if code := do(proxy, h); code != http.StatusTooManyRequests {
			t.Errorf("headers %v got a fresh bucket: %d", h, code)
		}
	}
	if code := do(proxy, map[string]string{"X-Forwarded-For": "203.0.113.20"}); code != http.StatusOK {
		t.Errorf("another client behind the same proxy was throttled: %d", code)
	}
}

// Two proxies (CDN, then ingress): the client is the second entry from the right.
func TestClientIP_TwoProxiesUseSecondEntryFromRight(t *testing.T) {
	do := limited(t, 2)
	proxy := "10.0.0.5:4000"
	if code := do(proxy, map[string]string{"X-Forwarded-For": "203.0.113.9, 192.0.2.1"}); code != http.StatusOK {
		t.Fatalf("first request = %d", code)
	}
	if code := do(proxy, map[string]string{"X-Forwarded-For": "198.51.100.1, 203.0.113.9, 192.0.2.2"}); code != http.StatusTooManyRequests {
		t.Errorf("the same client through another CDN node got a fresh bucket: %d", code)
	}
	if code := do(proxy, map[string]string{"X-Forwarded-For": "203.0.113.20, 192.0.2.1"}); code != http.StatusOK {
		t.Errorf("another client behind the same CDN node was throttled: %d", code)
	}
}

// A chain shorter than the hop count, or an entry that is not an IP, cannot
// name a client: the request is keyed on the connection address instead.
func TestClientIP_UnusableChainFallsBackToConnection(t *testing.T) {
	do := limited(t, 2)
	if code := do("10.0.0.5:4000", map[string]string{"X-Forwarded-For": "203.0.113.9"}); code != http.StatusOK {
		t.Fatalf("first request = %d", code)
	}
	if code := do("10.0.0.5:4001", map[string]string{"X-Forwarded-For": "not-an-ip, 192.0.2.1"}); code != http.StatusTooManyRequests {
		t.Errorf("an unparseable entry got its own bucket: %d", code)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := logging.L()
	logging.SetLogger(logging.New(logging.Options{ECS: true, Writer: &buf, Level: slog.LevelDebug}))
	t.Cleanup(func() { logging.SetLogger(prev) })
	return &buf
}

// sender returns ClientIP(hops) in front of a no-op handler and a helper that
// sends one request with the given headers.
func sender(hops int) func(headers map[string]string) {
	h := ClientIP(hops)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	return func(headers map[string]string) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
}

// Behind a proxy with the setting still at 0, the server says so once —
// whichever forwarding header the proxy sends.
func TestClientIP_WarnsOnceWhenProxyHeaderIsIgnored(t *testing.T) {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "True-Client-IP"} {
		t.Run(header, func(t *testing.T) {
			buf := captureLog(t)
			send := sender(0)
			send(nil)
			if n := strings.Count(buf.String(), "client-ip-proxy-header-ignored"); n != 0 {
				t.Fatalf("warned without a forwarding header: %d lines", n)
			}
			send(map[string]string{header: "198.51.100.1"})
			send(map[string]string{header: "198.51.100.2"})
			if n := strings.Count(buf.String(), "client-ip-proxy-header-ignored"); n != 1 {
				t.Fatalf("warning lines = %d, want exactly 1\n%s", n, buf.String())
			}
			if !strings.Contains(buf.String(), "TRUSTED_PROXY_HOPS") {
				t.Errorf("warning does not name the setting:\n%s", buf.String())
			}
		})
	}
}

// With proxies configured, a proxy that sends X-Real-IP but no
// X-Forwarded-For leaves every user on the proxy's address: the server says
// so once. Requests with no forwarding header at all (health checks that
// reach the pod directly) stay quiet.
func TestClientIP_WarnsOnceWhenProxySendsNoForwardedFor(t *testing.T) {
	buf := captureLog(t)
	send := sender(1)
	send(nil)
	send(map[string]string{"X-Forwarded-For": "203.0.113.9"})
	if n := strings.Count(buf.String(), "client-ip-forwarded-for-missing"); n != 0 {
		t.Fatalf("warned on a health check or a usable chain: %d lines\n%s", n, buf.String())
	}
	send(map[string]string{"X-Real-IP": "203.0.113.9"})
	send(map[string]string{"True-Client-IP": "203.0.113.10"})
	if n := strings.Count(buf.String(), "client-ip-forwarded-for-missing"); n != 1 {
		t.Fatalf("warning lines = %d, want exactly 1\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "X-Forwarded-For") {
		t.Errorf("warning does not say which header to send:\n%s", buf.String())
	}
}
