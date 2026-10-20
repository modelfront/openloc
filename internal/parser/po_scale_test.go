package parser

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// Export of a large PO file takes memory in proportion to the file, not to
// entries × lines: 5,000 translated entries stay far under 100 MB allocated.
func TestPOReconstructScalesWithTheFile(t *testing.T) {
	var b strings.Builder
	translations := map[string]string{}
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "#: src/app.c:%d\nmsgid \"Message %d\"\nmsgstr \"\"\n\n", i, i)
		translations[fmt.Sprintf("Message %d", i)] = fmt.Sprintf("Nachricht %d", i)
	}
	content := []byte(b.String())

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	out, err := PO{PlainText: true}.Reconstruct(content, translations)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "msgstr \"Nachricht 4999\"") {
		t.Fatalf("export lacks the last translation")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 100<<20 {
		t.Errorf("Reconstruct allocated %d MB for a %d KB file", allocated>>20, len(content)>>10)
	}
}
