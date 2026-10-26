package android_xml

import (
	"strings"
	"testing"
)

const androidStoredRawInput = `<resources>
<string name="a">It\'s &amp; co</string>
<string name="q">"Hola"</string>
<string name="m">Tom &lt;b&gt;bold&lt;/b&gt;</string>
<string name="r">Tom <b>bold</b></string>
<string name="x">A and B</string>
<string name="p">Plain</string>
</resources>`

// A file whose stored segments hold the raw string content — escapes, quotes
// and entities as written — differs from a fresh parse; one stored decoded
// does not.
func TestAndroidStoredRaw(t *testing.T) {
	cases := []struct {
		name   string
		stored map[string]string
		want   bool
	}{
		{"raw", map[string]string{"a": `It\'s &amp; co`, "q": `"Hola"`, "p": "Plain"}, true},
		{"decoded", map[string]string{"a": "It's & co", "q": "Hola", "p": "Plain"}, false},
		{"nothing to tell apart", map[string]string{"p": "Plain"}, false},
	}
	for _, c := range cases {
		got, err := AndroidStoredRaw([]byte(androidStoredRawInput), c.stored)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: AndroidStoredRaw = %v, want %v", c.name, got, c.want)
		}
	}
}

// Translations stored as raw string content are decoded before export, so
// they are escaped once: no doubled backslash, no quote shown in the app, and
// an entity the translator copied from the source is not escaped again.
func TestAndroidXML_RawTextExport(t *testing.T) {
	out, err := AndroidXML{RawText: true}.Reconstruct([]byte(androidStoredRawInput), map[string]string{
		"a": `C\'est &amp; co`,
		"q": `"Adiós"`,
		"m": `Ana &lt;b&gt;negrita&lt;/b&gt;`,
		"r": `Ana <g id="1" ctype="bold">negrita</g>`,
		"x": `A & B`,
	})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	wellFormed(t, out)
	for _, want := range []string{
		`<string name="a">C\'est &amp; co</string>`,
		`<string name="q">"Adiós"</string>`,
		`<string name="m">Ana &lt;b&gt;negrita&lt;/b&gt;</string>`,
		`<string name="r">Ana <b>negrita</b></string>`,
		`<string name="x">A &amp; B</string>`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %s\ngot:\n%s", want, out)
		}
	}
}
