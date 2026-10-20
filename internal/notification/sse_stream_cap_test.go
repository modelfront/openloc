package notification

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"openloc/internal/auth"
	"openloc/internal/httputil"
	"openloc/internal/httputil/ssetest"
)

// One user's notification streams stop at httputil.MaxStreamsPerUser with a
// 429. Another user's stream on the same handler still opens, and a closed
// stream frees its place.
func TestSSEHandler_CapsAUsersOpenStreams(t *testing.T) {
	handler := NewSSEHandler(NewInProcessBroker(), &stubCounter{}, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, err := strconv.ParseInt(r.URL.Query().Get("user"), 10, 64)
		if err != nil {
			http.Error(w, "bad user", http.StatusBadRequest)
			return
		}
		ctx := auth.WithClaims(r.Context(), &auth.Claims{UserID: userID, TenantID: 1})
		handler.HandleStream(w, r.WithContext(ctx))
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	open := func(userID int64) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s?user=%d", server.URL, userID), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET stream: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	status := func(userID int64) int {
		resp := open(userID)
		resp.Body.Close()
		return resp.StatusCode
	}

	streams := make([]*http.Response, 0, httputil.MaxStreamsPerUser)
	for range httputil.MaxStreamsPerUser {
		resp := open(42)
		if got := readSSEFrame(t, ssetest.Frames(resp.Body), 2*time.Second); !strings.Contains(got, "event: connected") {
			t.Fatalf("stream %d under the cap: first frame %q, want connected", len(streams)+1, got)
		}
		streams = append(streams, resp)
	}
	if got := status(42); got != http.StatusTooManyRequests {
		t.Fatalf("stream past the cap answered %d, want 429", got)
	}
	if got := status(43); got != http.StatusOK {
		t.Fatalf("another user's stream answered %d, want 200", got)
	}

	streams[0].Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for status(42) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("a closed stream did not free its place within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
