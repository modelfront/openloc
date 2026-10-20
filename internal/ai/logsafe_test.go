package ai

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/logging"
)

// A provider's own error text can echo the request. ProviderError keeps it in
// Error() for callers and leaves it out of logs.
func TestProviderErrorDetailStaysOutOfLogs(t *testing.T) {
	err := &ProviderError{Provider: ProviderOpenAI, Status: 400, Kind: ProviderErrProvider, Detail: "bad input SECRET-SEGMENT-TEXT"}

	out := captureWarn(t, err)
	if !strings.Contains(out, "(status 400)") {
		t.Errorf("log lacks the status: %s", out)
	}
	if strings.Contains(out, "SECRET-SEGMENT-TEXT") {
		t.Errorf("provider detail reached the log: %s", out)
	}
}

// captureWarn logs err through logging.Warn into a buffer and returns the output.
func captureWarn(t *testing.T, err error) string {
	t.Helper()
	var buf bytes.Buffer
	prev := logging.L()
	logging.Init(logging.Options{ECS: true, Writer: &buf})
	t.Cleanup(func() { logging.SetLogger(prev) })
	logging.Warn(context.Background(), err, "provider failed", "x-failed")
	return buf.String()
}

// A non-2xx answer logs the provider's error code, parsed from the body, and
// not its message text.
func TestProviderErrorLogsCodeNotMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad input SECRET-SEGMENT-TEXT","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()
	req, err := http.NewRequest(http.MethodPost, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, callErr := (&HTTPTranslator{client: srv.Client()}).do(req, ProviderOpenAI)

	out := captureWarn(t, fmt.Errorf("translate: %w", callErr))
	if !strings.Contains(out, "openai provider provider (status 400): code invalid_request_error") {
		t.Errorf("log lacks the provider's error code: %s", out)
	}
	if strings.Contains(out, "SECRET-SEGMENT-TEXT") {
		t.Errorf("provider message reached the log: %s", out)
	}
}

// An unreachable provider has no response body; its Detail is the transport
// error, which the log keeps.
func TestProviderErrorUnreachableKeepsTransportDetail(t *testing.T) {
	err := &ProviderError{Provider: ProviderClaude, Kind: ProviderErrUnreachable, Detail: "dial tcp: connection refused"}
	if out := captureWarn(t, err); !strings.Contains(out, "dial tcp: connection refused") {
		t.Errorf("log lacks the transport error: %s", out)
	}
}

// callFailure's message quotes the provider body; a log carries Reason().
func TestCallFailureBodyStaysOutOfLogs(t *testing.T) {
	f := &callFailure{status: 500, msg: "provider returned status 500: SECRET-SEGMENT-TEXT"}
	out := captureWarn(t, fmt.Errorf("list models: %w", f))
	if !strings.Contains(out, "list models: "+f.Reason()) {
		t.Errorf("log lacks the client-safe reason: %s", out)
	}
	if strings.Contains(out, "SECRET-SEGMENT-TEXT") {
		t.Errorf("provider body reached the log: %s", out)
	}
}
