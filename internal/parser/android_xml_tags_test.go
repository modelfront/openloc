package parser

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// wellFormed fails the test unless doc parses as XML with every tag matched.
func wellFormed(t *testing.T, doc []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(doc))
	for {
		if _, err := dec.Token(); err == io.EOF {
			return
		} else if err != nil {
			t.Fatalf("export is not well-formed XML: %v\n%s", err, doc)
		}
	}
}

func exportAndroid(t *testing.T, input string, translations map[string]string) string {
	t.Helper()
	out, err := AndroidXML{}.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	wellFormed(t, out)
	return string(out)
}

// A line break chip comes back as the self-closing element the file had.
func TestAndroidXML_LineBreakRoundTrip(t *testing.T) {
	input := `<resources><string name="a">Tom<br/>two</string></resources>`
	segs, err := AndroidXML{}.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := exportAndroid(t, input, map[string]string{"a": strings.Replace(segs[0].RichText, "Tom", "Ana", 1)})
	if want := `<string name="a">Ana<br/>two</string>`; !strings.Contains(out, want) {
		t.Errorf("output missing %s\ngot:\n%s", want, out)
	}
}

// Only the source string's own tags are written as elements. A tag the
// translation adds or changes — a new attribute, a different link — is text.
func TestAndroidXML_ExportWritesOnlyTheSourceTags(t *testing.T) {
	input := `<resources><string name="a">Tap <a href="https://example.com">here</a></string></resources>`
	out := exportAndroid(t, input, map[string]string{"a": `Tap <a href="https://evil.example">here</a>`})
	if strings.Contains(out, `<a href="https://evil.example">`) {
		t.Errorf("a link the source does not have was written as an element:\n%s", out)
	}
	if !strings.Contains(out, `&lt;a href=\"https://evil.example\"&gt;`) {
		t.Errorf("the changed link should be escaped text:\n%s", out)
	}

	out = exportAndroid(t, input, map[string]string{"a": `Tap <a href="https://example.com">aquí</a>`})
	if want := `<string name="a">Tap <a href="https://example.com">aquí</a></string>`; !strings.Contains(out, want) {
		t.Errorf("the source's own link should stay an element: want %s\ngot:\n%s", want, out)
	}
}

// Tags that would not nest — a stray end tag, a start tag left open — make the
// whole translation text, so the file stays well-formed.
func TestAndroidXML_ExportKeepsTheFileWellFormed(t *testing.T) {
	input := `<resources><string name="a">Tom <b>bold</b></string></resources>`
	for _, tr := range []string{"Ana </b> bold", "Ana <b>bold", "</b>Ana<b>"} {
		out := exportAndroid(t, input, map[string]string{"a": tr})
		if strings.Contains(out, "<b>") || strings.Contains(out, "</b>") {
			t.Errorf("translation %q: unbalanced tags written as elements:\n%s", tr, out)
		}
	}
}

// A tag written in another form than the export's — single quotes, a space
// before `>` or `/>`, as chips stored from the file keep it — is still the
// source's tag and stays an element.
func TestAndroidXML_ExportMatchesTagsWrittenAnotherWay(t *testing.T) {
	input := `<resources>
<string name="a">Tap <a href='https://example.com'>here</a></string>
<string name="c">Tom has <xliff:g id="n" />cats</string>
</resources>`
	out := exportAndroid(t, input, map[string]string{
		"a": `Ana <a href='https://example.com' >aquí</a>`,
		"c": `Ana tiene <xliff:g id="n" /> gatos`,
	})
	for _, want := range []string{
		`<string name="a">Ana <a href="https://example.com">aquí</a></string>`,
		`<string name="c">Ana tiene <xliff:g id="n"/> gatos</string>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %s\ngot:\n%s", want, out)
		}
	}
}

// A string whose source carries escaped markup is read with Html.fromHtml. Its
// own tags come back escaped once, so they still render; a tag the source does
// not have is escaped twice, so it shows as text after Html.fromHtml too.
func TestAndroidXML_HtmlStringKeepsAddedTagsAsText(t *testing.T) {
	input := `<resources><string name="a">Tap &lt;a href=\"https://example.com\"&gt;here&lt;/a&gt;</string></resources>`
	out := exportAndroid(t, input, map[string]string{
		"a": `Toca <a href="https://example.com">aquí</a> <a href="https://evil.example">más</a>`,
	})
	if want := `Toca &lt;a href=\"https://example.com\"&gt;aquí&lt;/a&gt;`; !strings.Contains(out, want) {
		t.Errorf("the source's own link should stay escaped once: want %s\ngot:\n%s", want, out)
	}
	if want := `&amp;lt;a href=\"https://evil.example\"&amp;gt;más`; !strings.Contains(out, want) {
		t.Errorf("an added link should be escaped twice: want %s\ngot:\n%s", want, out)
	}
	if strings.Contains(out, `&lt;a href=\"https://evil.example\"&gt;`) {
		t.Errorf("an added link would render through Html.fromHtml:\n%s", out)
	}
}
