package httputil

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The writer the request logger wraps is where "what failed" is decided: a
// 500's cause is recorded, a 404 or a client error records nothing, and a
// writer wrapped again by middleware is still found.
func TestErrorRecordingWriterRecordsOnlyTheCauseOfA500(t *testing.T) {
	cause := errors.New("connection reset")
	notFound := fmt.Errorf("get label: %w", pgx.ErrNoRows)
	for name, tc := range map[string]struct {
		write func(http.ResponseWriter)
		want  error
	}{
		"ServiceError 500":        {func(w http.ResponseWriter) { ServiceError(w, cause, "list", "labels") }, cause},
		"ServiceError not found":  {func(w http.ResponseWriter) { ServiceError(w, notFound, "get", "label") }, nil},
		"ServiceError client 400": {func(w http.ResponseWriter) { ServiceError(w, NewClientError("bad"), "get", "label") }, nil},
		"DBError 500":             {func(w http.ResponseWriter) { DBError(w, cause, "label") }, cause},
		"DBError not found":       {func(w http.ResponseWriter) { DBError(w, notFound, "label") }, nil},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &ErrorRecordingWriter{ResponseWriter: httptest.NewRecorder()}
			tc.write(wrapped{rec})
			if !errors.Is(rec.Err(), tc.want) || (tc.want == nil && rec.Err() != nil) {
				t.Errorf("recorded %v, want %v", rec.Err(), tc.want)
			}
		})
	}
}

// wrapped stands in for a middleware that wraps the writer again.
type wrapped struct{ http.ResponseWriter }

func (w wrapped) Unwrap() http.ResponseWriter { return w.ResponseWriter }
