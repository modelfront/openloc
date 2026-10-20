package extension

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"openloc/pkg/extsdk"
)

// Calls running at once with one extension and one key share the manifest's
// requests_per_second: four concurrent predicts at 2 per second spread over
// about 1.5 seconds instead of arriving together.
func TestInvoke_ConcurrentCallsShareTheRateLimit(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		var rows []extsdk.PredictRow
		_ = json.Unmarshal(body, &rows)
		_ = json.NewEncoder(w).Encode(extsdk.PredictResponse{Rows: []extsdk.PredictResult{{ID: rows[0].ID, Approved: true}}})
	}))
	defer srv.Close()

	p := mockPackage(t)
	p.Manifest.Limits.RequestsPerSecond = 2
	rt := NewRuntime(srv.Client(), nil)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out extsdk.PredictResponse
			if err := rt.Invoke(context.Background(), predictCall(p, "tok", srv.URL, extsdk.PredictRow{ID: string(rune('1' + i)), Target: "x"}), &out); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(times) != 4 {
		t.Fatalf("vendor got %d requests, want 4", len(times))
	}
	first, last := times[0], times[0]
	for _, ts := range times {
		if ts.Before(first) {
			first = ts
		}
		if ts.After(last) {
			last = ts
		}
	}
	if span := last.Sub(first); span < 1200*time.Millisecond {
		t.Errorf("4 requests within %v at 2 per second, want them spread over about 1.5s", span)
	}
}
