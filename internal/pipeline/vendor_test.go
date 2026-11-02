package pipeline

import (
	"fmt"
	"strings"
	"testing"

	"openloc/internal/ai"
	"openloc/internal/logging"
)

// What is logged and reported for an outage is the vendor, kind and status:
// never the message, which can carry the extension's text, and through it the
// vendor's body.
func TestVendorOutageLogTextLeavesOutTheMessage(t *testing.T) {
	const marker = "SEGMENT-TEXT-MARKER"
	err := fmt.Errorf("ai step: %w", &VendorOutageError{
		Message: "The AI step could not complete: QE: " + marker + ". Try again in a few minutes.",
		Err:     &ai.ProviderError{Provider: "ext:qe", Status: 503, Kind: ai.ProviderErrProvider},
	})
	logged := logging.SafeErrorMessage(err)
	if strings.Contains(logged, marker) {
		t.Errorf("the message reached the log text: %q", logged)
	}
	if !strings.Contains(logged, "ext:qe provider provider (status 503)") {
		t.Errorf("the log text lost the vendor and status: %q", logged)
	}
}
