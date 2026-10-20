package hold_test

import (
	"context"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/testutil"
)

// One user's hold streams stop at httputil.MaxStreamsPerUser with a 429.
// Another user's stream still opens, and a closed stream frees its place.
func TestIntegrationHoldStream_CapsAUsersOpenStreams(t *testing.T) {
	f, cleanup := newHoldFixture(t)
	defer cleanup()
	srv := streamServer(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streams := make([]*http.Response, 0, httputil.MaxStreamsPerUser)
	for range httputil.MaxStreamsPerUser {
		resp := streamRequest(t, ctx, srv, f, f.pm, f.job.ID)
		t.Cleanup(func() { resp.Body.Close() })
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d under the cap answered %d, want 200", len(streams)+1, resp.StatusCode)
		}
		expectEvent(t, eventNames(resp), "connected")
		streams = append(streams, resp)
	}

	status := func(u db.User) int {
		resp := streamRequest(t, ctx, srv, f, u, f.job.ID)
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := status(f.pm); got != http.StatusTooManyRequests {
		t.Fatalf("stream past the cap answered %d, want 429", got)
	}
	if got := status(f.linguist); got != http.StatusOK {
		t.Fatalf("another user's stream answered %d, want 200", got)
	}

	streams[0].Body.Close()
	testutil.WaitFor(t, func() bool { return status(f.pm) == http.StatusOK }, "a closed stream frees its place")
}
