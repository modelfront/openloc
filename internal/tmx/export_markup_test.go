package tmx

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// tmxInline are the elements TMX 1.4 allows inside <seg>.
var tmxInline = map[string]bool{"bpt": true, "ept": true, "ph": true, "it": true, "hi": true, "ut": true, "sub": true}

// segElements returns every element name used inside the <seg>s of a TMX
// document, failing on malformed XML.
func segElements(t *testing.T, doc string) []string {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(doc))
	var names []string
	inSeg := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatalf("export is not well-formed XML: %v\n%s", err, doc)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			if inSeg > 0 {
				names = append(names, el.Name.Local)
			}
			if el.Name.Local == "seg" {
				inSeg++
			}
		case xml.EndElement:
			if el.Name.Local == "seg" {
				inSeg--
			}
		}
	}
}

// An entry written back from the editor stores its tags as XLIFF chips, and
// editor bold as <strong>. The export writes the chips as TMX paired and
// placeholder codes, keeps the text of the editor formatting, and never puts
// an element TMX does not allow inside <seg>. TMX's own codes pass unchanged.
func TestExportWritesOnlyTMXInlineCodes(t *testing.T) {
	entries := []Entry{
		{SourceLang: "en", TargetLang: "de",
			SourceText:     "Click here now and save",
			SourceRichText: `Click <g id="1" ctype="bold">here</g><x id="2" ctype="lb"/>now and <strong>save</strong>`,
			TargetText:     "Klicken Sie hier jetzt und speichern",
			TargetRichText: `Klicken Sie <g id="1" ctype="x-html-a" original_markup="&lt;a href=&quot;/x&quot;&gt;">hier</g><x id="2" ctype="lb"/>jetzt und <em>speichern</em>`},
		{SourceLang: "en", TargetLang: "de",
			SourceText:     "Press OK",
			SourceRichText: `Press <bpt i="1">&lt;b&gt;</bpt>OK<ept i="1">&lt;/b&gt;</ept>`,
			TargetText:     "OK drücken",
			TargetRichText: `<bpt i="1">&lt;b&gt;</bpt>OK<ept i="1">&lt;/b&gt;</ept> drücken`},
	}
	var buf bytes.Buffer
	if err := Export(&buf, "en", entries); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, name := range segElements(t, out) {
		if !tmxInline[name] {
			t.Errorf("export puts <%s> inside a <seg>:\n%s", name, out)
		}
	}
	for _, want := range []string{
		`Click <bpt i="1">&lt;b&gt;</bpt>here<ept i="1">&lt;/b&gt;</ept><ph x="2">&lt;br/&gt;</ph>now and save`,
		`<bpt i="1">&lt;a href=&quot;/x&quot;&gt;</bpt>hier<ept i="1">&lt;/a&gt;</ept>`,
		`Press <bpt i="1">&lt;b&gt;</bpt>OK<ept i="1">&lt;/b&gt;</ept>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("export lacks %s:\n%s", want, out)
		}
	}

	// The export imports back with the source text and its tags.
	again, err := ParseEntries(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 2 || again[0].SourceText != "Click herenow and save" || !strings.Contains(again[0].SourceRichText, "<bpt") {
		t.Errorf("re-import = %+v", again)
	}
}

// Rich text holding an element TMX has no code for, or that is not
// well-formed, is written as the escaped plain text instead.
func TestExportFallsBackToPlainTextForOtherMarkup(t *testing.T) {
	entries := []Entry{
		{SourceLang: "en", TargetLang: "de", SourceText: "Hello <world>",
			SourceRichText: `Hello <span class="x">&lt;world&gt;</span>`, TargetText: "Hallo", TargetRichText: `Hallo <g id="1">Welt`},
	}
	var buf bytes.Buffer
	if err := Export(&buf, "en", entries); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, name := range segElements(t, out) {
		if !tmxInline[name] {
			t.Errorf("export puts <%s> inside a <seg>:\n%s", name, out)
		}
	}
	for _, want := range []string{"<seg>Hello &lt;world&gt;</seg>", "<seg>Hallo</seg>"} {
		if !strings.Contains(out, want) {
			t.Errorf("export lacks %s:\n%s", want, out)
		}
	}
}
