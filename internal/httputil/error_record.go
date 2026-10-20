package httputil

import "net/http"

// ErrorRecordingWriter is the response writer the request logger hands to the
// handlers. ServiceError and DBError record the unexpected error behind a 500
// on it, so the request's log line says what failed; the client still gets
// only the generic message.
type ErrorRecordingWriter struct {
	http.ResponseWriter
	err error
}

// Err is the first error recorded for the request, or nil.
func (w *ErrorRecordingWriter) Err() error { return w.err }

// Unwrap lets http.ResponseController and recordError reach the writer inside.
func (w *ErrorRecordingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush keeps the streaming handlers working: they assert http.Flusher on the
// writer they are given.
func (w *ErrorRecordingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// recordError stores err on the ErrorRecordingWriter under w, if there is one.
// Middleware between the logger and the handler may wrap the writer again, so
// it follows Unwrap.
func recordError(w http.ResponseWriter, err error) {
	for w != nil {
		if rw, ok := w.(*ErrorRecordingWriter); ok {
			if rw.err == nil {
				rw.err = err
			}
			return
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = u.Unwrap()
	}
}
