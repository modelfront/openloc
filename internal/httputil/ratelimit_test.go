package httputil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestKeyedLimiter_TakeRefusesWithWait(t *testing.T) {
	kl := NewKeyedLimiter[int64](1, 3, 0, "test")
	for i := 0; i < 3; i++ {
		if ok, _ := kl.Take(7); !ok {
			t.Fatalf("call %d within burst was refused", i+1)
		}
	}
	ok, wait := kl.Take(7)
	if ok {
		t.Fatal("call past the burst was allowed")
	}
	if wait <= 0 || wait > time.Second {
		t.Fatalf("wait = %v, want (0, 1s] at 1 rps", wait)
	}
}

func TestKeyedLimiter_RefusalDoesNotSpendAToken(t *testing.T) {
	kl := NewKeyedLimiter[int64](1, 1, 0, "test")
	if ok, _ := kl.Take(1); !ok {
		t.Fatal("first call refused")
	}
	// A client hammering an empty bucket must not push its own recovery out.
	_, first := kl.Take(1)
	_, second := kl.Take(1)
	if second > first {
		t.Fatalf("refused calls deepened the debt: wait %v then %v", first, second)
	}
}

func TestTooManyRequests_RetryAfterAndCode(t *testing.T) {
	rec := httptest.NewRecorder()
	TooManyRequests(rec, 1500*time.Millisecond, "slow down")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want 2 (rounded up)", got)
	}
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "rate_limited" || body.Error.Message != "slow down" {
		t.Errorf("body = %+v", body.Error)
	}
}

func TestRateLimiter_MiddlewareRefusesWithRetryAfter(t *testing.T) {
	rl := NewRateLimiter(1, 1)
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	var last *httptest.ResponseRecorder
	do := func(remoteAddr string) int {
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.RemoteAddr = remoteAddr
		last = httptest.NewRecorder()
		h.ServeHTTP(last, r)
		return last.Code
	}
	if code := do("203.0.113.9:1111"); code != http.StatusOK {
		t.Fatalf("first call = %d", code)
	}
	// Another port on the same host is the same client.
	if code := do("203.0.113.9:2222"); code != http.StatusTooManyRequests {
		t.Fatalf("a new connection from the same host got a fresh bucket: %d", code)
	}
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.NewDecoder(last.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if last.Header().Get("Retry-After") == "" || body.Error.Code != "rate_limited" {
		t.Fatalf("refusal: Retry-After %q, code %q", last.Header().Get("Retry-After"), body.Error.Code)
	}
	if code := do("203.0.113.10:1111"); code != http.StatusOK {
		t.Fatalf("another host was throttled by the first: %d", code)
	}
}
