package chat

import (
	"strings"
	"testing"
)

func TestCleanUploadName(t *testing.T) {
	cases := map[string]string{
		"terms.tbx":             "terms.tbx",
		"../../etc/passwd":      "passwd",
		`C:\Users\x\memory.tmx`: "memory.tmx",
		"bad\x00name\n.csv":     "badname.csv",
		"":                      "upload",
		"/":                     "upload",
	}
	for in, want := range cases {
		if got := cleanUploadName(in); got != want {
			t.Errorf("cleanUploadName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := cleanUploadName(strings.Repeat("a", 300) + ".tmx"); len(got) != 200 {
		t.Errorf("name not capped at 200 bytes: %d bytes", len(got))
	}
}
