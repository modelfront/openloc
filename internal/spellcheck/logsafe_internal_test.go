package spellcheck

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/logging"
)

// A non-200 from LanguageTool can echo the request, which is segment text. The
// error text keeps it for callers; a log line carries the status only.
func TestStatusErrorBodyStaysOutOfLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("unknown language for text SECRET-SEGMENT-TEXT"))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).Check(context.Background(), "SECRET-SEGMENT-TEXT", "en-US")
	if err == nil {
		t.Fatal("expected an error")
	}

	var buf bytes.Buffer
	prev := logging.L()
	logging.Init(logging.Options{ECS: true, Writer: &buf})
	t.Cleanup(func() { logging.SetLogger(prev) })
	logging.Error(context.Background(), err, "spell check failed", "spellcheck-failed")

	out := buf.String()
	if !strings.Contains(out, "LanguageTool returned 400") {
		t.Errorf("log lacks the status: %s", out)
	}
	if strings.Contains(out, "SECRET-SEGMENT-TEXT") {
		t.Errorf("response body reached the log: %s", out)
	}
}
