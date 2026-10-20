package notification

import (
	"strings"
	"testing"
	"time"

	"openloc/internal/httputil/ssetest"
)

// One user id spans every org the person belongs to, so the broker routes a
// user's events from all of them to every stream that user has open. The
// stream is tenant-scoped like the REST list: a session in org 1 gets org 1's
// notifications only, never another org's project or file names.
//
// The org-2 event is published first and the org-1 event second. The channel
// keeps order, so the first frame read is the org-2 one unless the stream
// drops it — no timeout decides the result.
func TestSSEHandler_OtherOrgsEventsAreNotDelivered(t *testing.T) {
	broker := NewInProcessBroker()
	server := newSSETestServer(t, broker, 0, 42) // session claims: user 42, tenant 1

	resp, ctx := getStream(t, server, 3*time.Second)

	frames := ssetest.Frames(resp.Body)
	if got := readSSEFrame(t, frames, 2*time.Second); !strings.Contains(got, "event: connected") {
		t.Fatalf("stream did not open: %q", got)
	}

	if err := broker.Publish(ctx, Event{
		Type: EventAssignment, TenantID: 2, RecipientID: 42, JobID: 200,
		ProjectName: "Other org project", FileName: "other-org.docx", TargetLang: "de",
	}); err != nil {
		t.Fatalf("Publish org 2: %v", err)
	}
	if err := broker.Publish(ctx, Event{
		Type: EventAssignment, TenantID: 1, RecipientID: 42, JobID: 100,
		ProjectName: "Own org project", FileName: "own-org.docx", TargetLang: "fr",
	}); err != nil {
		t.Fatalf("Publish org 1: %v", err)
	}

	got := readSSEFrame(t, frames, 2*time.Second)
	if strings.Contains(got, `"job_id":200`) || strings.Contains(got, "Other org project") || strings.Contains(got, "other-org.docx") {
		t.Fatalf("session in tenant 1 received tenant 2's notification: %q", got)
	}
	if !strings.Contains(got, "event: notification") || !strings.Contains(got, `"job_id":100`) {
		t.Fatalf("expected tenant 1's notification (job_id 100), got %q", got)
	}
}
