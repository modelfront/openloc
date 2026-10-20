package mt

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"openloc/internal/errtrack"
	"openloc/internal/errtrack/errtracktest"
)

// A vendor body can echo the segment text it was sent, and errtrack notices go
// to a third party, so the notice carries the status and never the body.
func TestAPIErrorBodyStaysOutOfErrtrackNotice(t *testing.T) {
	const marker = "SEGMENT-TEXT-MARKER"
	rec := errtracktest.Install(t)
	err := fmt.Errorf("translate batch: %w", &apiError{vendor: "deepl: API", status: 400, body: "bad text " + marker})

	errtrack.Capture(context.Background(), err, errtrack.CaptureOptions{
		Class: errtrack.ClassWorkerTaskFail, Message: "task failed", Subsystem: "worker", Event: "translate",
	})

	msg := rec.One(t).ErrorMessage
	if strings.Contains(msg, marker) {
		t.Errorf("vendor body reached the notice: %q", msg)
	}
	if !strings.Contains(msg, "task failed: translate batch: deepl: API returned 400") {
		t.Errorf("notice lost its context: %q", msg)
	}
}
