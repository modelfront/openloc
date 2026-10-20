package mcp_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/*.golden from the current tool list")

// toolGoldenLines renders tools/list as "<name> <gate>[ open-world]" lines,
// gate being read, write or destructive, and open-world marking a tool that
// reaches outside our systems (from the protocol annotations clients see).
func toolGoldenLines(t *testing.T, s *sdk.ClientSession) string {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var b strings.Builder
	for _, tool := range res.Tools {
		gate := "write"
		switch {
		case tool.Annotations == nil:
			t.Errorf("tool %q has no annotations", tool.Name)
		case tool.Annotations.ReadOnlyHint:
			gate = "read"
		case tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint:
			gate = "destructive"
		}
		line := tool.Name + " " + gate
		if a := tool.Annotations; a != nil {
			// Unset means "open world" to clients, so every tool states it.
			if a.OpenWorldHint == nil {
				t.Errorf("tool %q has no openWorldHint", tool.Name)
			} else if *a.OpenWorldHint {
				line += " open-world"
			}
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func checkToolGolden(t *testing.T, tier string, s *sdk.ClientSession) {
	t.Helper()
	got := toolGoldenLines(t, s)
	path := filepath.Join("testdata", "tools_"+tier+".golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run with -update to create it): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("%s tool list changed. If intended, run with -update and review the diff.\n--- want\n%s--- got\n%s", tier, want, got)
	}
}
