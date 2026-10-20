package chat

import (
	"encoding/json"
	"os"
	"testing"
)

// The stored shape of a screenshot message, shared with the panel's
// chat-history.test.ts, which must strip exactly this marker.
func TestScreenshotMessageShape(t *testing.T) {
	raw, err := os.ReadFile("testdata/screenshot_message.json")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		UploadID string `json:"upload_id"`
		Sent     string `json:"sent"`
		Stored   string `json:"stored"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if got := withScreenshotMarker(c.Sent, c.UploadID); got != c.Stored {
		t.Errorf("stored message =\n%q\nwant (testdata/screenshot_message.json)\n%q", got, c.Stored)
	}
}
