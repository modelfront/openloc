package parser

import (
	"strings"
	"testing"
)

// replaceAll applies each old→new pair once, failing when the fixture lacks one.
func replaceAll(t *testing.T, s string, pairs ...[2]string) string {
	t.Helper()
	for _, p := range pairs {
		if !strings.Contains(s, p[0]) {
			t.Fatalf("fixture lacks %q", p[0])
		}
		s = strings.Replace(s, p[0], p[1], 1)
	}
	return s
}

// Xcode ids are string keys: they may hold escaped characters, and the same id
// may appear in two <file> elements. Every unit gets its own key (the id, or
// "f<N>/<id>" for a repeat in file N) and its own translation on export.
func TestXLIFFEscapedAndRepeatedIDsRoundTrip(t *testing.T) {
	doc := string(readFixture(t, "testdata/xcode_export.xliff"))
	segs, err := XLIFF{}.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var keys []string
	for _, s := range segs {
		keys = append(keys, s.Key)
	}
	if got, want := strings.Join(keys, " | "), `Terms & Conditions | Title | Say "Hi" | f2/Title`; got != want {
		t.Fatalf("keys = %s, want %s", got, want)
	}

	out, err := XLIFF{}.Reconstruct([]byte(doc), map[string]string{
		"Terms & Conditions": "AGB",
		"Title":              "Willkommen",
		`Say "Hi"`:           `Sag „Hallo“`,
		"f2/Title":           "Beispiel-App",
	})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	want := replaceAll(t, doc,
		[2]string{"<source>Terms &amp; Conditions</source>", "<source>Terms &amp; Conditions</source><target>AGB</target>"},
		[2]string{"<source>Welcome</source>", "<source>Welcome</source><target>Willkommen</target>"},
		[2]string{`<source>Say "Hi"</source>`, `<source>Say "Hi"</source><target>Sag „Hallo“</target>`},
		[2]string{"<source>Example App</source>", "<source>Example App</source><target>Beispiel-App</target>"},
	)
	if string(out) != want {
		t.Fatalf("export:\n%s\nwant:\n%s", out, want)
	}
}

// A Trados unit with no <target>, an empty <target/>, or a self-closing target
// <mrk/> still gets its translation: the target is built from the <seg-source>
// (untranslated segments keep the source text) and a self-closing mrk opens.
func TestXLIFFSegmentedUnitsWithoutTargetRoundTrip(t *testing.T) {
	doc := string(readFixture(t, "testdata/untranslated.sdlxliff"))
	out, err := XLIFF{}.Reconstruct([]byte(doc), map[string]string{
		"a1f0:1": "Manual de uso.",
		"a1f1":   "Prólogo",
		"a1f2":   "Contenido",
	})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	want := replaceAll(t, doc,
		[2]string{`<mrk mtype="seg" mid="2">Bitte lesen.</mrk></seg-source>`,
			`<mrk mtype="seg" mid="2">Bitte lesen.</mrk></seg-source><target><mrk mtype="seg" mid="1">Manual de uso.</mrk> <mrk mtype="seg" mid="2">Bitte lesen.</mrk></target>`},
		[2]string{`<target><mrk mtype="seg" mid="3"/></target>`, `<target><mrk mtype="seg" mid="3">Prólogo</mrk></target>`},
		[2]string{`<target/>`, `<target><mrk mtype="seg" mid="4">Contenido</mrk></target>`},
	)
	if string(out) != want {
		t.Fatalf("export:\n%s\nwant:\n%s", out, want)
	}

	// The export parses back with every translation in its segment.
	segs, err := XLIFF{}.Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	targets := map[string]string{}
	for _, s := range segs {
		targets[s.Key] = s.Target
	}
	if targets["a1f0:1"] != "Manual de uso." || targets["a1f0:2"] != "Bitte lesen." || targets["a1f1"] != "Prólogo" || targets["a1f2"] != "Contenido" {
		t.Errorf("re-parsed targets = %v", targets)
	}
}

// An id written with entities in the file is matched on export.
func TestXLIFFEscapedIDGetsItsTranslation(t *testing.T) {
	doc := `<xliff version="1.2"><file source-language="en" target-language="de"><body>
<trans-unit id="R&amp;D &lt;team&gt;"><source>R&amp;D team</source></trans-unit>
</body></file></xliff>`
	out, err := XLIFF{}.Reconstruct([]byte(doc), map[string]string{"R&D <team>": "F&E-Team"})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if want := strings.Replace(doc, "<source>R&amp;D team</source>", "<source>R&amp;D team</source><target>F&amp;E-Team</target>", 1); string(out) != want {
		t.Fatalf("export:\n%s\nwant:\n%s", out, want)
	}
}

// A plain unit whose <target/> is empty gets the translation inside it.
func TestXLIFFEmptyPlainTargetIsFilled(t *testing.T) {
	doc := `<xliff version="1.2"><file source-language="en" target-language="de"><body>
<trans-unit id="1"><source>Hello</source><target/></trans-unit>
</body></file></xliff>`
	out, err := XLIFF{}.Reconstruct([]byte(doc), map[string]string{"1": "Hallo"})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if want := strings.Replace(doc, "<target/>", "<target>Hallo</target>", 1); string(out) != want {
		t.Fatalf("export:\n%s\nwant:\n%s", out, want)
	}
}
