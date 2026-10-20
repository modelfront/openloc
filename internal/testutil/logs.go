package testutil

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"openloc/internal/logging"
)

// CaptureLogs routes the process logger into a buffer, in the ECS JSON the
// server writes, until the test ends.
func CaptureLogs(t testing.TB) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := logging.L()
	logging.Init(logging.Options{ECS: true, Writer: &buf, Level: slog.LevelDebug})
	t.Cleanup(func() { logging.SetLogger(prev) })
	return &buf
}

// ErrorRecords returns the captured records logged at error level.
func ErrorRecords(t testing.TB, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("captured log line is not JSON: %q", line)
		}
		if rec[logging.KeyLogLevel] == "error" {
			out = append(out, rec)
		}
	}
	return out
}
