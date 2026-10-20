package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"openloc/internal/logging"
)

// A failed key check logs a masked key: enough to tell which key was pasted,
// never enough to use it.
func TestLogKeyTestFailure_MasksKey(t *testing.T) {
	var buf bytes.Buffer
	prev := logging.L()
	logging.Init(logging.Options{ECS: true, Writer: &buf, Level: slog.LevelDebug})
	t.Cleanup(func() { logging.SetLogger(prev) })

	const key = "sk-proj-FULL-bad-key-1234567890"
	logKeyTestFailure(context.Background(), errors.New("401"), ProviderOpenAI, key, "https://acme.openai.azure.com/openai", 42)

	out := buf.String()
	if strings.Contains(out, key) || strings.Contains(out, "FULL-bad-key") {
		t.Fatalf("log carries the key:\n%s", out)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, out)
	}
	if got, want := rec["openloc.debug.api_key"], "sk-p…7890 (31 chars)"; got != want {
		t.Errorf("api_key = %v, want %q", got, want)
	}
	if rec["openloc.debug.base_url"] != "https://acme.openai.azure.com/openai" || rec["openloc.debug.connection_id"] != float64(42) {
		t.Errorf("base_url / connection_id missing: %v", rec)
	}
}

func TestMaskKey(t *testing.T) {
	cases := map[string]string{
		"":                     "(0 chars)",
		"short-key":            "(9 chars)",
		"exactly-16-chars":     "exac…hars (16 chars)",
		"sk-ant-api03-abcdefg": "sk-a…defg (20 chars)",
	}
	for in, want := range cases {
		if got := maskKey(in); got != want {
			t.Errorf("maskKey(%q) = %q, want %q", in, got, want)
		}
	}
}
