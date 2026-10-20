package parser

import "testing"

// The trans-unit id must parse back to the exact segment_key, or the
// re-import cannot match the unit to its row. PO keys are the raw msgid, so
// a key can hold a backslash or a no-break space.
func TestSerializeBilingualXLIFF_KeyRoundTripsVerbatim(t *testing.T) {
	keys := []string{
		`say "hi" & <b>'s`,
		"line1\nline2\r\n\tindented",
		"10" + string(rune(0x202F)) + "MB",
		"a" + string(rune(0x200F)) + string(rune(0x200D)) + "b",
		`C:\Users`,
		"10\u00a0MB",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			out, err := SerializeBilingualXLIFF("messages.po", "en", "es", []BilingualSegment{
				{Key: key, SourceText: key, TargetText: "x", Status: "approved"},
			})
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			parsed, err := XLIFF{}.Parse(out)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(parsed) != 1 {
				t.Fatalf("parsed %d segments, want 1", len(parsed))
			}
			if parsed[0].Key != key {
				t.Errorf("key parsed back as %q, want %q", parsed[0].Key, key)
			}
		})
	}
}
