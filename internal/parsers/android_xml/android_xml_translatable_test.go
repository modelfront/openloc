package android_xml

import (
	"strings"
	"testing"
)

// translatable="false" on a <string-array> or <plurals> covers its items: they
// are not imported, and export leaves them as uploaded even when a translation
// arrives under their key.
func TestAndroidXML_NonTranslatableArraysAndPluralsAreSkipped(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string-array name="units_values" translatable="false">
        <item>metric</item>
        <item>imperial</item>
    </string-array>
    <string-array name="units">
        <item>Metric</item>
    </string-array>
    <plurals name="debug_count" translatable="false">
        <item quantity="other">%d debug</item>
    </plurals>
</resources>
`
	p := AndroidXML{}
	segs, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(segs) != 1 || segs[0].Key != "units[0]" {
		t.Fatalf("segments = %+v, want only units[0]", segs)
	}
	got, err := p.Reconstruct([]byte(input), map[string]string{
		"units[0]": "Métrico", "units_values[0]": "métrico", "debug_count[other]": "%d depurar",
	})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if want := strings.Replace(input, ">Metric<", ">Métrico<", 1); string(got) != want {
		t.Fatalf("export:\n%s\nwant:\n%s", got, want)
	}
}
