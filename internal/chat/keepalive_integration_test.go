package chat_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"openloc/internal/chat"
)

// lateWriteRecorder counts writes and flushes that are still running after
// ServeHTTP returned. net/http forbids them; behind the compression middleware
// the writer is already recycled by then, so the write is a nil dereference
// that takes the whole process down.
type lateWriteRecorder struct {
	*httptest.ResponseRecorder
	// pingDelay keeps each keepalive write in flight for a while, so one is
	// still running when the turn's last event goes out.
	pingDelay time.Duration

	mu       sync.Mutex
	returned bool
	late     int
}

func (w *lateWriteRecorder) Write(p []byte) (int, error) {
	if bytes.HasPrefix(p, []byte(": ping")) {
		time.Sleep(w.pingDelay)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.returned {
		w.late++
	}
	return w.ResponseRecorder.Write(p)
}

func (w *lateWriteRecorder) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.returned {
		w.late++
	}
	w.ResponseRecorder.Flush()
}

func TestIntegrationChatKeepaliveStopsBeforeHandlerReturns(t *testing.T) {
	restore := chat.SetKeepaliveIntervalForTest(time.Millisecond)
	defer restore()

	f := newChatFixture(t, nil)
	f.handler.SetLLMFactory(func(_, _, _, _ string) chat.LLM {
		return &slowLLM{delay: 5 * time.Millisecond}
	})

	// Each turn ends while a ping is likely queued or in flight; several turns
	// make a miss on all of them practically impossible.
	const turns = 5
	recs := make([]*lateWriteRecorder, 0, turns)
	for range turns {
		rec := &lateWriteRecorder{ResponseRecorder: httptest.NewRecorder(), pingDelay: 10 * time.Millisecond}
		f.router.ServeHTTP(rec, ownerRequest("POST", "/api/chat", `{"message":"q"}`, f.ownerA, f.tenantA.ID))
		rec.mu.Lock()
		rec.returned = true
		rec.mu.Unlock()
		recs = append(recs, rec)
	}
	// Long enough for a ping that was in flight at return to finish.
	time.Sleep(50 * time.Millisecond)

	late := 0
	for i, rec := range recs {
		rec.mu.Lock()
		late += rec.late
		code, body := rec.Code, rec.Body.String()
		rec.mu.Unlock()
		if code != http.StatusOK || !strings.Contains(body, "event: done") {
			t.Fatalf("turn %d: status %d, stream without done:\n%s", i, code, body)
		}
	}
	if late > 0 {
		t.Fatalf("%d keepalive writes reached the response after the handler returned", late)
	}
}
