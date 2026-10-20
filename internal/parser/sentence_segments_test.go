package parser

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func parseWith(t *testing.T, format, content string) []Segment {
	t.Helper()
	p, err := GetParser(format)
	if err != nil {
		t.Fatal(err)
	}
	segs, err := p.Parse([]byte(content))
	if err != nil {
		t.Fatalf("parse %s: %v", format, err)
	}
	return segs
}

func reconstructWith(t *testing.T, format, content string, tr map[string]string) string {
	t.Helper()
	p, err := GetParser(format)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Reconstruct([]byte(content), tr)
	if err != nil {
		t.Fatalf("reconstruct %s: %v", format, err)
	}
	return string(out)
}

type keyText struct{ key, text string }

func keyTexts(segs []Segment) []keyText {
	out := make([]keyText, len(segs))
	for i, s := range segs {
		out[i] = keyText{s.Key, s.Text}
	}
	return out
}

// A block of several sentences becomes one segment per sentence, keyed
// <block key>#n, in order; a one-sentence block keeps its key.
func TestSentenceSegments_SplitsBlocksIntoSentences(t *testing.T) {
	cases := []struct {
		format, content string
		want            []keyText
	}{
		{"txt", "First one.  Second one! Third?\nOnly one here.\n", []keyText{
			{"line:1#1", "First one."}, {"line:1#2", "Second one!"}, {"line:1#3", "Third?"}, {"line:2", "Only one here."},
		}},
		{"html", "<html><body><p>Every TMS was built early.  Pricing is per seat.</p><p>One.</p></body></html>", []keyText{
			{"seg:1#1", "Every TMS was built early."}, {"seg:1#2", "Pricing is per seat."}, {"seg:2", "One."},
		}},
		{"markdown", "# Title\n\nThe first sentence. The second sentence.\n", []keyText{
			{"block:1", "# Title"}, {"block:2#1", "The first sentence."}, {"block:2#2", "The second sentence."},
		}},
		{"json_i18n", `{"intro":"Save the file. Then close it.","short":"Close"}`, []keyText{
			{"intro#1", "Save the file."}, {"intro#2", "Then close it."}, {"short", "Close"},
		}},
	}
	for _, c := range cases {
		segs := parseWith(t, c.format, c.content)
		got := keyTexts(segs)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %v, want %v", c.format, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s segment %d = %v, want %v", c.format, i, got[i], c.want[i])
			}
			if segs[i].Position != i {
				t.Errorf("%s segment %d position = %d", c.format, i, segs[i].Position)
			}
		}
	}
}

// A sentence break never cuts through an inline element, and each sentence's
// chips are numbered from 1.
func TestSentenceSegments_TagsStayWhole(t *testing.T) {
	segs := parseWith(t, "html", `<p><b>Bold one.</b> Plain <a href="https://example.com">link</a> two. <em>One. Two.</em></p>`)
	got := keyTexts(segs)
	want := []keyText{{"seg:1#1", "Bold one."}, {"seg:1#2", "Plain link two."}, {"seg:1#3", "One. Two."}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d = %v, want %v", i, got[i], want[i])
		}
	}
	if segs[0].RichText != `<g id="1" ctype="bold">Bold one.</g>` {
		t.Errorf("sentence 1 rich = %q", segs[0].RichText)
	}
	if !strings.HasPrefix(segs[1].RichText, `Plain <g id="1" ctype="x-html-a"`) || !strings.HasSuffix(segs[1].RichText, `>link</g> two.`) {
		t.Errorf("sentence 2 rich = %q", segs[1].RichText)
	}
	if segs[2].RichText != `<g id="1" ctype="x-html-em">One. Two.</g>` {
		t.Errorf("sentence 3 rich = %q", segs[2].RichText)
	}
}

// Export puts each sentence's translation back with the source's spacing,
// shifts its chip ids back, and keeps the source for a sentence with none.
func TestSentenceSegments_ReconstructJoinsSentences(t *testing.T) {
	src := "<p><b>Bold one.</b>  Plain <i>two</i>. Three.</p>"
	out := reconstructWith(t, "html", src, map[string]string{
		"seg:1#1": `<g id="1" ctype="bold">Uno en negrita.</g>`,
		"seg:1#2": `Dos <g id="1" ctype="italic">sencillo</g>.`,
	})
	// HTML spacing is the rendered one: the source's double space is one.
	want := "<p><b>Uno en negrita.</b> Dos <i>sencillo</i>. Three.</p>"
	if !strings.Contains(out, want) {
		t.Errorf("html export = %s, want it to contain %s", out, want)
	}

	txt := reconstructWith(t, "txt", "  First one.  Second one.\nKeep.\n", map[string]string{
		"line:1#1": "Uno.", "line:1#2": "Dos.", "line:2": "Guardar.",
	})
	if txt != "  Uno.  Dos.\nGuardar.\n" {
		t.Errorf("txt export = %q", txt)
	}
}

// A translation stored under the whole block's key is written as it is.
func TestSentenceSegments_ReconstructHonoursBlockKeys(t *testing.T) {
	out := reconstructWith(t, "txt", "First one. Second one.\n", map[string]string{"line:1": "Todo junto."})
	if out != "Todo junto.\n" {
		t.Errorf("export = %q, want the block translation", out)
	}
}

// A block whose sentence keys would clash with a key the file already has
// stays whole.
func TestSentenceSegments_KeyClashKeepsBlockWhole(t *testing.T) {
	segs := parseWith(t, "json_i18n", `{"a":"One here. Two here.","a#1":"Other"}`)
	got := keyTexts(segs)
	if len(got) != 2 || got[0] != (keyText{"a", "One here. Two here."}) || got[1] != (keyText{"a#1", "Other"}) {
		t.Errorf("segments = %v, want the clashing block kept whole", got)
	}
}

// DOCX segments come from the sidecar and go through the same splitting; the
// sidecar's export gets one paragraph back, with the paragraph's chip ids.
func TestSentenceSegments_DocxThroughSidecar(t *testing.T) {
	var gotTranslations map[string]string
	useSidecar(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/parse/docx":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"segments": []map[string]any{{
				"key": "para:0", "text": "Save the file. Then close it.", "position": 0,
				"rich_text": `Save the file. Then <g id="1" ctype="bold">close</g> it.`,
			}}})
		case "/reconstruct/docx":
			if err := json.Unmarshal([]byte(r.FormValue("translations")), &gotTranslations); err != nil {
				t.Errorf("translations: %v", err)
			}
			w.Write([]byte("docx"))
		}
	})

	segs := parseWith(t, "docx", "fake")
	if len(segs) != 2 || segs[0].Key != "para:0#1" || segs[1].Key != "para:0#2" {
		t.Fatalf("segments = %v", keyTexts(segs))
	}
	if segs[1].RichText != `Then <g id="1" ctype="bold">close</g> it.` {
		t.Errorf("sentence 2 rich = %q", segs[1].RichText)
	}

	reconstructWith(t, "docx", "fake", map[string]string{
		"para:0#1": "Guarde el archivo.",
		"para:0#2": `Luego <g id="1" ctype="bold">ciérrelo</g>.`,
	})
	// The sidecar rebuilds formatting and links from the chips.
	want := `Guarde el archivo. Luego <g id="1" ctype="bold">ciérrelo</g>.`
	if len(gotTranslations) != 1 || gotTranslations["para:0"] != want {
		t.Errorf("sidecar got %v, want para:0 = %q", gotTranslations, want)
	}

	// An untranslated sentence with bold keeps its source chips.
	reconstructWith(t, "docx", "fake", map[string]string{"para:0#1": "Guarde el archivo."})
	if want := `Guarde el archivo. Then <g id="1" ctype="bold">close</g> it.`; gotTranslations["para:0"] != want {
		t.Errorf("partly translated: sidecar got %q, want %q", gotTranslations["para:0"], want)
	}
}

// PDF blocks from the sidecar are split too, and keep their context.
func TestSentenceSegments_PdfThroughSidecar(t *testing.T) {
	useSidecar(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"segments": []map[string]any{
			{"key": "page:0:block:0", "text": "Page one starts here. It has two sentences.", "position": 0, "context": "page:0"},
			{"key": "page:0:block:1", "text": "One more.", "position": 1, "context": "page:0"},
		}})
	})

	segs := parseWith(t, "pdf", "fake")
	want := []keyText{
		{"page:0:block:0#1", "Page one starts here."}, {"page:0:block:0#2", "It has two sentences."}, {"page:0:block:1", "One more."},
	}
	got := keyTexts(segs)
	if len(got) != len(want) {
		t.Fatalf("segments = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] || segs[i].Context != "page:0" || segs[i].Position != i {
			t.Errorf("segment %d = %v (context %q, position %d), want %v", i, got[i], segs[i].Context, segs[i].Position, want[i])
		}
	}
}

// "Strings contain HTML" off reads a JSON or TXT file as plain text; its
// values are still split into sentences.
func TestSentenceSegments_PlainTextModeSplits(t *testing.T) {
	for _, c := range []struct{ format, content, firstKey string }{
		{"json_i18n", `{"intro":"Save the <b>file</b>. Then close it."}`, "intro#1"},
		{"txt", "Save the <b>file</b>. Then close it.\n", "line:1#1"},
	} {
		p, err := GetParserFor(c.format, false)
		if err != nil {
			t.Fatal(err)
		}
		segs, err := p.Parse([]byte(c.content))
		if err != nil {
			t.Fatal(err)
		}
		if len(segs) != 2 || segs[0].Key != c.firstKey || segs[0].Text != "Save the <b>file</b>." || segs[0].RichText != "" {
			t.Errorf("%s plain text: %+v", c.format, segs)
		}
	}
}

// With strings as HTML, a JSON export joins the sentences' rich text, and a
// sentence with no translation keeps its source tags.
func TestSentenceSegments_JSONRichExportKeepsSourceTags(t *testing.T) {
	src := `{"intro":"Open <b>this</b>. Then <i>close</i> it."}`
	segs := parseWith(t, "json_i18n", src)
	if len(segs) != 2 || segs[1].RichText != `Then <g id="1" ctype="italic">close</g> it.` {
		t.Fatalf("segments = %+v", segs)
	}
	out := reconstructWith(t, "json_i18n", src, map[string]string{
		"intro#1": `Abra <g id="1" ctype="bold">esto</g>.`,
	})
	if want := `{"intro":"Abra <b>esto</b>. Then <i>close</i> it."}`; out != want {
		t.Errorf("export = %s, want %s", out, want)
	}
}

// An ICU plural or select keeps its braces whole: no sentence ends inside {…}.
func TestSentenceSegments_NoBreakInsideBraces(t *testing.T) {
	segs := parseWith(t, "json_i18n", `{"files":"{count, plural, one {One file. Open it.} other {# files. Open them.}}","hi":"Hello {name}. Welcome back."}`)
	got := keyTexts(segs)
	want := []keyText{
		{"files", "{count, plural, one {One file. Open it.} other {# files. Open them.}}"},
		{"hi#1", "Hello {name}."}, {"hi#2", "Welcome back."},
	}
	if len(got) != len(want) {
		t.Fatalf("segments = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// A PDF cannot be exported; the message comes back without the sidecar
// parsing the file first.
func TestSentenceSegments_PdfExportSkipsTheParse(t *testing.T) {
	useSidecar(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("sidecar called: %s", r.URL.Path)
	})

	p, err := GetParser("pdf")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Reconstruct([]byte("fake"), map[string]string{"page:0:block:0#1": "Hola."})
	var ee *ExportError
	if !errors.As(err, &ee) {
		t.Errorf("export error = %v, want an ExportError", err)
	}
}

// Without sentence keys there is nothing to join, so a file stored as whole
// blocks costs no second parse.
func TestSentenceSegments_BlockKeysSkipThePlan(t *testing.T) {
	parses := 0
	useSidecar(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/parse/docx" {
			parses++
		}
		w.Write([]byte("docx"))
	})

	reconstructWith(t, "docx", "fake", map[string]string{"para:0": "Todo."})
	if parses != 0 {
		t.Errorf("export parsed the file %d times, want 0", parses)
	}
}

// In HTML, a sentence with the editor's own formatting does not let markup
// typed as text in another sentence of the same paragraph become an element.
func TestSentenceSegments_HTMLEscapesEachSentence(t *testing.T) {
	src := `<p>One plain sentence. Two says &lt;a href="https://x.example"&gt;here&lt;/a&gt;.</p>`
	segs := parseWith(t, "html", src)
	if len(segs) != 2 || segs[1].Text != `Two says <a href="https://x.example">here</a>.` {
		t.Fatalf("segments = %+v", keyTexts(segs))
	}
	out := reconstructWith(t, "html", src, map[string]string{"seg:1#1": "Una frase <strong>simple</strong>."})
	if strings.Contains(out, `<a href`) || !strings.Contains(out, `Two says &lt;a href=&#34;https://x.example&#34;&gt;here&lt;/a&gt;.`) {
		t.Errorf("export = %s, want the second sentence's markup as text", out)
	}
	if !strings.Contains(out, "<strong>simple</strong>") {
		t.Errorf("export = %s, want the editor's bold kept", out)
	}
}

// The other sentences of a block with editor formatting keep their inline
// elements: a link in an untranslated sentence, a chip in a translated one,
// and a line break between two sentences.
func TestSentenceSegments_HTMLEditorFormattingKeepsNeighbourChips(t *testing.T) {
	src := `<p>One is <b>bold</b>. Two has <a href="/x">a link</a>.<br>Three is <i>here</i>.</p>`
	segs := parseWith(t, "html", src)
	if len(segs) != 3 {
		t.Fatalf("segments = %+v", keyTexts(segs))
	}
	third := strings.NewReplacer("Three is", "Tres está", ">here<", ">aquí<").Replace(segs[2].RichText)
	if !strings.Contains(third, "aquí</g>") {
		t.Fatalf("third sentence rich text = %q", segs[2].RichText)
	}
	out := reconstructWith(t, "html", src, map[string]string{
		"seg:1#1": "Uno es <strong>negrita</strong>.",
		"seg:1#3": third,
	})
	for _, want := range []string{
		"Uno es <strong>negrita</strong>.",
		`Two has <a href="/x">a link</a>.`,
		"<br/>",
		"Tres está <i>aquí</i>.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("export = %s, want it to contain %s", out, want)
		}
	}
	if strings.Contains(out, "chip-") {
		t.Errorf("export = %s, a chip placeholder leaked as text", out)
	}
}

// Only a translation can be editor HTML. An untranslated sentence whose
// source text holds an editor closing tag as characters stays text, whether
// or not a translated neighbour carries editor formatting, and the plain
// translations next to it are escaped.
func TestSentenceSegments_HTMLUntranslatedSourceIsNeverEditorHTML(t *testing.T) {
	src := `<p>Close italics with &lt;/em&gt; here. Two is plain.</p>`
	segs := parseWith(t, "html", src)
	if len(segs) != 2 || segs[0].Text != "Close italics with </em> here." {
		t.Fatalf("segments = %+v", keyTexts(segs))
	}
	cases := map[string]struct{ translation, want string }{
		"bold neighbour":  {"Dos es <strong>negrita</strong>.", "Dos es <strong>negrita</strong>."},
		"plain neighbour": {"Dos & <tres>.", "Dos &amp; &lt;tres&gt;."},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out := reconstructWith(t, "html", src, map[string]string{"seg:1#2": c.translation})
			for _, want := range []string{"Close italics with &lt;/em&gt; here.", c.want} {
				if !strings.Contains(out, want) {
					t.Errorf("export = %s, want it to contain %s", out, want)
				}
			}
		})
	}
}

// An attribute split into sentences is written as plain text and escaped
// once, even when one of its translated sentences has editor formatting.
func TestSentenceSegments_HTMLAttributeIsEscapedOnce(t *testing.T) {
	src := `<p><img alt="Tom &amp; Jerry run. Two here." src="x.png"> Text.</p>`
	segs := parseWith(t, "html", src)
	var key string
	for _, sg := range segs {
		if sg.Text == "Two here." {
			key = sg.Key
		}
	}
	if key == "" {
		t.Fatalf("segments = %+v", keyTexts(segs))
	}
	out := reconstructWith(t, "html", src, map[string]string{key: "Dos <strong>aquí</strong>."})
	if !strings.Contains(out, "Tom &amp; Jerry run.") || strings.Contains(out, "&amp;amp;") {
		t.Errorf("export = %s, want the untranslated sentence escaped once", out)
	}
}

// A block stays whole when splitting it could lose or corrupt anything.
func TestSentenceSegments_KeepsUnsafeBlocksWhole(t *testing.T) {
	cases := map[string]Segment{
		"has a target":       {Key: "a", Text: "One here. Two here.", Target: "Uno. Dos."},
		"rich does not fit":  {Key: "b", Text: "One here. Two here.", RichText: `One here. Two <x id="1"/>there.`},
		"chip in the gap":    {Key: "c", Text: "One here.  Two here.", RichText: `One here. <g id="1" ctype="bold"></g> Two here.`},
		"unbalanced chip":    {Key: "d", Text: "One here. Two here.", RichText: `One here. Two here.</g>`},
		"chip across a stop": {Key: "e", Text: "One here. Two here.", RichText: `<g id="1" ctype="bold">One here. Two</g> here.`},
	}
	for name, b := range cases {
		if plans := sentencePlans([]Segment{b}); len(plans) != 0 {
			t.Errorf("%s: block split into %+v", name, plans[b.Key].parts)
		}
	}
	// The same text with nothing in the way splits.
	if plans := sentencePlans([]Segment{{Key: "f", Text: "One here. Two here."}}); len(plans["f"].parts) != 2 {
		t.Errorf("a plain two-sentence block did not split: %+v", plans)
	}
}

// TXT exports a sentence's rich text when its strings are HTML; a sentence
// with no translation keeps its source tags.
func TestSentenceSegments_TXTRichExportKeepsSourceTags(t *testing.T) {
	src := "Open <b>this</b>. Then <i>close</i> it.\n"
	out := reconstructWith(t, "txt", src, map[string]string{"line:1#1": `Abra <g id="1" ctype="bold">esto</g>.`})
	if want := "Abra <b>esto</b>. Then <i>close</i> it.\n"; out != want {
		t.Errorf("export = %q, want %q", out, want)
	}
}

// useSidecar points the parser at a fake sidecar for the rest of the test.
func useSidecar(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	old := SidecarURL
	SidecarURL = server.URL
	t.Cleanup(func() {
		SidecarURL = old
		server.Close()
	})
}
