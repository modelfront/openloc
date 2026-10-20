package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"openloc/internal/errtrack/errtracktest"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// The error a handler hands to httputil reaches the request's "request failed"
// line, so a 500 says what failed. It never reaches the Honeybadger notice: a
// database error can quote the row it choked on.
func TestHandlerErrorReachesRequestLog(t *testing.T) {
	cause := errors.New("relation \"labels\" is busy: value Acme Confidential")
	for name, write := range map[string]func(http.ResponseWriter){
		"ServiceError": func(w http.ResponseWriter) { httputil.ServiceError(w, fmt.Errorf("list labels: %w", cause), "list", "labels") },
		"DBError":      func(w http.ResponseWriter) { httputil.DBError(w, fmt.Errorf("get label: %w", cause), "label") },
	} {
		t.Run(name, func(t *testing.T) {
			rec := errtracktest.Install(t)
			buf := captureLogs(t)
			r := testRouter(t, func(r chi.Router) {
				r.Get("/api/labels", func(w http.ResponseWriter, _ *http.Request) { write(w) })
			})
			res := httptest.NewRecorder()
			r.ServeHTTP(res, httptest.NewRequest("GET", "/api/labels", nil))
			if res.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", res.Code)
			}

			lines := decodeLines(t, buf)
			last := lines[len(lines)-1]
			if got, _ := last[logging.KeyErrorMessage].(string); !strings.Contains(got, "is busy") {
				t.Errorf("request log error.message = %q, want the handler's error", got)
			}
			if strings.Contains(res.Body.String(), "is busy") {
				t.Errorf("the error reached the client: %s", res.Body.String())
			}
			n := rec.One(t)
			if strings.Contains(fmt.Sprint(n.ErrorMessage, n.Context), "Acme Confidential") {
				t.Errorf("the error text reached the Honeybadger notice: %q %v", n.ErrorMessage, n.Context)
			}
		})
	}
}

// A 404 is control flow, not a failure: its log line is info level and carries
// no error. That nothing is recorded for it is pinned in httputil, where the
// writer decides (TestErrorRecordingWriterRecordsOnlyTheCauseOfA500).
func TestNotFoundLogsNoError(t *testing.T) {
	buf := captureLogs(t)
	r := testRouter(t, func(r chi.Router) {
		r.Get("/api/labels/{id}", func(w http.ResponseWriter, _ *http.Request) {
			httputil.ServiceError(w, fmt.Errorf("get label: %w", pgx.ErrNoRows), "get", "label")
		})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/labels/7", nil))
	lines := decodeLines(t, buf)
	if _, ok := lines[len(lines)-1][logging.KeyErrorMessage]; ok {
		t.Errorf("a 404 logged an error: %v", lines[len(lines)-1])
	}
}

// The chat and notification streams assert http.Flusher on the writer they
// get; the request logger's writer must keep that working.
func TestStreamingHandlersStillFlush(t *testing.T) {
	captureLogs(t)
	var flushed bool
	r := testRouter(t, func(r chi.Router) {
		r.Get("/api/stream", func(w http.ResponseWriter, _ *http.Request) {
			f, ok := w.(http.Flusher)
			if !ok {
				t.Error("the handler's writer is not an http.Flusher")
				return
			}
			if _, err := w.Write([]byte("data: hi\n\n")); err != nil {
				t.Error(err)
			}
			f.Flush()
			flushed = true
		})
	})
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest("GET", "/api/stream", nil))
	if !flushed || !res.Flushed {
		t.Errorf("flushed = %v, recorder flushed = %v, want both", flushed, res.Flushed)
	}
}
