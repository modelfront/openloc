package translation_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/httputil"
	"openloc/internal/translation"
)

// An unexpected error behind a segment write is a 500 that records its cause
// for the request's log line and keeps it from the client.
func TestWriteSegmentWriteError_RecordsTheCauseOfA500(t *testing.T) {
	cause := errors.New("conn busy: relation translation_holds")
	rec := httptest.NewRecorder()
	w := &httputil.ErrorRecordingWriter{ResponseWriter: rec}

	(&translation.Handler{}).WriteSegmentWriteError(context.Background(), w, fmt.Errorf("check hold: %w", cause), 1, 1, "unlock before editing")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !errors.Is(w.Err(), cause) {
		t.Errorf("recorded error = %v, want the cause", w.Err())
	}
	if strings.Contains(rec.Body.String(), "translation_holds") {
		t.Errorf("the error reached the client: %s", rec.Body.String())
	}
}
