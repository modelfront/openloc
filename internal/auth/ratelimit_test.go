package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenRateLimiterKeysByToken(t *testing.T) {
	rl := NewTokenRateLimiter("test", 0.001, 2)
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	call := func(info *PATInfo) int {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if info != nil {
			req = req.WithContext(WithPATInfo(req.Context(), info))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	tokenA := &PATInfo{RateLimitKey: 1}
	for i := range 2 {
		if got := call(tokenA); got != http.StatusNoContent {
			t.Fatalf("token A call %d = %d, want 204", i+1, got)
		}
	}
	if got := call(tokenA); got != http.StatusTooManyRequests {
		t.Errorf("token A past its burst = %d, want 429", got)
	}
	if got := call(&PATInfo{RateLimitKey: 2}); got != http.StatusNoContent {
		t.Errorf("token B after A ran out = %d, want 204", got)
	}
	// A browser session carries no token; it is not limited here.
	for i := range 5 {
		if got := call(nil); got != http.StatusNoContent {
			t.Errorf("tokenless call %d = %d, want 204", i+1, got)
		}
	}
}
