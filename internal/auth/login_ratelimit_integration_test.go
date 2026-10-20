package auth_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"openloc/internal/api"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The sign-in limiter allows 5 requests a second per client, with a burst of
// 10. Through the real router, headers the client can write must not give it
// a new client identity.
func TestIntegration_LoginRateLimitKeysOnTrustedClientIP(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := func(hops int) chi.Router {
		return api.NewRouter(api.Deps{
			Queries:          db.New(pool),
			DB:               pool,
			JWTSecret:        testutil.TestJWTSecret,
			BaseURL:          "http://localhost:5173",
			TrustedProxyHops: hops,
		})
	}
	// An empty body gets a 400 before any password check, so each request is
	// fast and only the limiter decides whether it is refused.
	login := func(r chi.Router, remoteAddr string, headers map[string]string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remoteAddr
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	// refusedWithin sends 30 requests and fails unless the limiter refused one.
	// With a burst of 10 and 5 more a second, 30 requests from one client
	// would need 4 seconds to all pass.
	refusedWithin := func(t *testing.T, send func(i int) int) {
		t.Helper()
		for i := 0; i < 30; i++ {
			if send(i) == http.StatusTooManyRequests {
				return
			}
		}
		t.Fatal("30 requests, none refused: forged headers got a fresh bucket each time")
	}

	t.Run("no proxy", func(t *testing.T) {
		r := router(0)
		refusedWithin(t, func(i int) int {
			return login(r, "203.0.113.9:4000", map[string]string{
				"X-Forwarded-For": fmt.Sprintf("198.51.100.%d", i+1),
				"X-Real-IP":       fmt.Sprintf("198.51.101.%d", i+1),
				"True-Client-IP":  fmt.Sprintf("198.51.102.%d", i+1),
			})
		})
	})

	t.Run("one proxy", func(t *testing.T) {
		r := router(1)
		refusedWithin(t, func(i int) int {
			return login(r, "10.0.0.5:4000", map[string]string{
				"X-Forwarded-For": fmt.Sprintf("198.51.100.%d, 203.0.113.9", i+1),
				"X-Real-IP":       fmt.Sprintf("198.51.101.%d", i+1),
			})
		})
		if code := login(r, "10.0.0.5:4000", map[string]string{"X-Forwarded-For": "203.0.113.20"}); code == http.StatusTooManyRequests {
			t.Fatalf("another client behind the proxy was refused")
		}
	})
}
