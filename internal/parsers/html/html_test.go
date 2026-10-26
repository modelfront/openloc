package html

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestHTMLParseBasic(t *testing.T) {
	input := `<html><head><title>My Page</title></head><body>
<h1>Welcome</h1>
<p>Hello world</p>
<ul>
  <li>Item one</li>
  <li>Item two</li>
</ul>
</body></html>`

	p := HTML{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	expected := []struct {
		key  string
		text string
	}{
		{"seg:1", "My Page"},
		{"seg:2", "Welcome"},
		{"seg:3", "Hello world"},
		{"seg:4", "Item one"},
		{"seg:5", "Item two"},
	}

	if len(segments) != len(expected) {
		t.Fatalf("expected %d segments, got %d: %+v", len(expected), len(segments), segments)
	}
	for i, e := range expected {
		if segments[i].Key != e.key {
			t.Errorf("segment %d key = %q, want %q", i, segments[i].Key, e.key)
		}
		if segments[i].Text != e.text {
			t.Errorf("segment %d text = %q, want %q", i, segments[i].Text, e.text)
		}
		if segments[i].Position != i {
			t.Errorf("segment %d position = %d, want %d", i, segments[i].Position, i)
		}
	}
}

func TestHTMLParseInlineMarkup(t *testing.T) {
	input := `<html><body><p>This is <strong>bold</strong> and <em>italic</em> text with a <a href="http://example.com">link</a>.</p></body></html>`

	p := HTML{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	seg := segments[0]

	// Text should be plain (no HTML tags)
	if seg.Text != "This is bold and italic text with a link." {
		t.Errorf("Text = %q, want plain text", seg.Text)
	}

	// RichText should contain XLIFF <g> tags for the inline formatting
	if seg.RichText == "" {
		t.Fatal("RichText should not be empty")
	}
	if !strings.Contains(seg.RichText, `<g id="1" ctype="x-html-strong">bold</g>`) {
		t.Errorf("RichText should contain <g ctype=x-html-strong>, got %q", seg.RichText)
	}
	if !strings.Contains(seg.RichText, `<g id="2" ctype="x-html-em">italic</g>`) {
		t.Errorf("RichText should contain <g ctype=x-html-em>, got %q", seg.RichText)
	}
	// The <a> chip carries original_markup, so
	// match on the parts that identify it rather than the whole opening tag.
	if !strings.Contains(seg.RichText, `<g id="3" ctype="x-html-a"`) ||
		!strings.Contains(seg.RichText, `>link</g>`) {
		t.Errorf("RichText should contain <g ctype=x-html-a>, got %q", seg.RichText)
	}
	if !strings.Contains(seg.RichText, `original_markup="&lt;a href=&quot;http://example.com&quot;&gt;"`) {
		t.Errorf("RichText should preserve the link's href, got %q", seg.RichText)
	}
}

func TestHTMLParseSkipScriptStyle(t *testing.T) {
	input := `<html><body>
<script>var x = 1;</script>
<style>.foo { color: red; }</style>
<p>Visible text</p>
<pre>code block</pre>
<noscript>No JS</noscript>
</body></html>`

	p := HTML{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d: %+v", len(segments), segments)
	}
	if segments[0].Text != "Visible text" {
		t.Errorf("got text %q, want %q", segments[0].Text, "Visible text")
	}
}

func TestHTMLParseTranslateNo(t *testing.T) {
	input := `<html><body>
<p>Translate me</p>
<p translate="no">Do not translate</p>
<p>Also translate</p>
</body></html>`

	p := HTML{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d: %+v", len(segments), segments)
	}
	if segments[0].Text != "Translate me" {
		t.Errorf("segment 0 text = %q, want %q", segments[0].Text, "Translate me")
	}
	if segments[1].Text != "Also translate" {
		t.Errorf("segment 1 text = %q, want %q", segments[1].Text, "Also translate")
	}
}

func TestHTMLParseMetaDescription(t *testing.T) {
	input := `<html><head><meta name="description" content="A great page about cats"></head><body><p>Hello</p></body></html>`

	p := HTML{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d: %+v", len(segments), segments)
	}
	if segments[0].Text != "A great page about cats" {
		t.Errorf("meta segment text = %q", segments[0].Text)
	}
	if segments[0].Context != "meta:description" {
		t.Errorf("meta segment context = %q, want %q", segments[0].Context, "meta:description")
	}
}

func TestHTMLReconstruct(t *testing.T) {
	input := `<html><head><title>My Page</title></head><body><h1>Welcome</h1><p>Hello world</p></body></html>`
	translations := map[string]string{
		"seg:1": "Mi Pagina",
		"seg:2": "Bienvenidos",
		"seg:3": "Hola mundo",
	}

	p := HTML{}
	result, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	out := string(result)
	if !strings.Contains(out, "Mi Pagina") {
		t.Errorf("expected translated title, got:\n%s", out)
	}
	if !strings.Contains(out, "Bienvenidos") {
		t.Errorf("expected translated h1, got:\n%s", out)
	}
	if !strings.Contains(out, "Hola mundo") {
		t.Errorf("expected translated p, got:\n%s", out)
	}
	// Original text should not appear.
	if strings.Contains(out, "Welcome") {
		t.Errorf("original h1 text still present:\n%s", out)
	}
}

func TestHTMLParseInlineTagsAsRichText(t *testing.T) {
	input := `<html><body><p>Click <b>here</b> to <a href="http://example.com">continue</a>.</p></body></html>`

	p := HTML{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	seg := segments[0]

	// Text should be PLAIN (no HTML tags)
	wantText := "Click here to continue."
	if seg.Text != wantText {
		t.Errorf("Text = %q, want %q", seg.Text, wantText)
	}

	// RichText should contain XLIFF <g> tags wrapping the inline elements
	if seg.RichText == "" {
		t.Fatal("RichText should not be empty — has inline formatting")
	}
	if !strings.Contains(seg.RichText, `<g id="1" ctype="bold">here</g>`) {
		t.Errorf("RichText should contain <g ctype=bold>, got %q", seg.RichText)
	}
	if !strings.Contains(seg.RichText, `<g id="2" ctype="x-html-a"`) ||
		!strings.Contains(seg.RichText, `>continue</g>`) {
		t.Errorf("RichText should contain <g ctype=x-html-a>, got %q", seg.RichText)
	}
}

func TestHTMLParsePlainTextNoRichText(t *testing.T) {
	input := `<html><body><p>Just plain text, no formatting.</p></body></html>`

	p := HTML{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	if segments[0].Text != "Just plain text, no formatting." {
		t.Errorf("Text = %q", segments[0].Text)
	}
	if segments[0].RichText != "" {
		t.Errorf("RichText should be empty for plain text, got %q", segments[0].RichText)
	}
}

func TestHTMLReconstructWithXLIFFTags(t *testing.T) {
	// Reconstruct should convert XLIFF <g> tags back to HTML elements
	input := `<html><body><p>Click <b>here</b> to continue</p></body></html>`
	translations := map[string]string{
		"seg:1": `Haga clic <g id="1" ctype="bold">aqui</g> para continuar`,
	}

	p := HTML{}
	result, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	out := string(result)
	if !strings.Contains(out, "<b>aqui</b>") {
		t.Errorf("expected XLIFF <g> converted back to <b>, got:\n%s", out)
	}
	if strings.Contains(out, "<g ") {
		t.Errorf("XLIFF <g> tags should not remain in output:\n%s", out)
	}
}

// Markup typed into a plain translation is text, not an element. Links come
// back through chips (TestHTMLLinkAttributesSurviveChipRoundTrip).
func TestHTMLReconstructTypedMarkupStaysText(t *testing.T) {
	input := `<html><body><p>Click <a href="/here">here</a> now</p></body></html>`
	translations := map[string]string{
		"seg:1": `Haz clic <a href="/here">aqui</a> ahora`,
	}

	p := HTML{}
	result, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	out := string(result)
	if want := `<p>Haz clic &lt;a href=&#34;/here&#34;&gt;aqui&lt;/a&gt; ahora</p>`; !strings.Contains(out, want) {
		t.Errorf("typed markup not kept as text, want %s in:\n%s", want, out)
	}
}

// TestHTMLLinkAttributesSurviveChipRoundTrip: a translator edits the text
// between chips, so the translation that reaches Reconstruct is chip-form —
// `<g id="1" ctype="x-html-a">aquí</g>` — and the URL has to come from what
// the parser stored at import.
func TestHTMLLinkAttributesSurviveChipRoundTrip(t *testing.T) {
	input := []byte(`<html><body><p>Click <a href="https://example.com">here</a> now</p></body></html>`)

	segments, err := HTML{}.Parse(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(segments))
	}
	if segments[0].RichText == "" {
		t.Fatal("expected rich text with an <a> chip")
	}

	// Translate the text inside the chip, exactly as the editor would.
	translated := strings.Replace(segments[0].RichText, ">here<", ">aquí<", 1)
	if translated == segments[0].RichText {
		t.Fatalf("test setup: could not find the link text in %q", segments[0].RichText)
	}

	out, err := HTML{}.Reconstruct(input, map[string]string{segments[0].Key: translated})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}

	if !strings.Contains(string(out), `<a href="https://example.com">aquí</a>`) {
		t.Errorf("link lost its href on export.\n got: %s", out)
	}
}

// htmlEntitiesInput holds escaped markup, an escaped entity and a real inline
// tag next to escaped text.
const htmlEntitiesInput = `<html><head></head><body>` +
	`<p>Use the &lt;b&gt; element &amp; &quot;quotes&quot; &#169;</p>` +
	`<p>Write &amp;lt; for a less-than sign</p>` +
	`<p>Click <b>here</b> &amp; &lt;i&gt;now&lt;/i&gt;</p>` +
	`</body></html>`

// The translator sees characters, and export writes them back as text: an
// escaped `&lt;b&gt;` never becomes an element, `&amp;lt;` never collapses.
func TestHTMLEntityRoundTrip(t *testing.T) {
	src := []byte(htmlEntitiesInput)
	segs, err := HTML{}.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 3 {
		t.Fatalf("got %d segments, want 3", len(segs))
	}
	wantText := []string{
		`Use the <b> element & "quotes" ©`,
		`Write &lt; for a less-than sign`,
		`Click here & <i>now</i>`,
	}
	for i, want := range wantText {
		if segs[i].Text != want {
			t.Errorf("seg %d Text = %q, want %q", i, segs[i].Text, want)
		}
	}

	// Untranslated round trip, fed the way export feeds it: rich text when the
	// segment has chips, plain text otherwise.
	same := map[string]string{}
	for _, s := range segs {
		same[s.Key] = s.Text
		if s.RichText != "" {
			same[s.Key] = s.RichText
		}
	}
	out, err := HTML{}.Reconstruct(src, same)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if got, want := htmlBlocks(t, out), htmlBlocks(t, src); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("untranslated round trip changed the blocks:\n got %q\nwant %q\n%s", got, want, out)
	}

	translated := map[string]string{
		segs[0].Key: `Usa el elemento <b> & "comillas" ©`,
		segs[1].Key: `Escribe &lt; para el signo menor que`,
		segs[2].Key: strings.Replace(segs[2].RichText, ">here<", ">aquí<", 1),
	}
	out, err = HTML{}.Reconstruct(src, translated)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	want := []string{
		`Usa el elemento <b> & "comillas" ©`,
		`Escribe &lt; para el signo menor que`,
		`Click [b]aquí[/b] & <i>now</i>`,
	}
	if got := htmlBlocks(t, out); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("exported blocks =\n %q\nwant\n %q\n%s", got, want, out)
	}
	if !strings.Contains(string(out), `Usa el elemento &lt;b&gt; &amp; `) {
		t.Errorf("translation not escaped:\n%s", out)
	}
}

// Bold, italic, sub- and superscript applied in the editor arrive as the
// editor's own HTML, already escaped, and stay formatting.
func TestHTMLReconstructKeepsEditorFormatting(t *testing.T) {
	src := []byte(`<html><head></head><body><p>Hello world</p></body></html>`)
	out, err := HTML{}.Reconstruct(src, map[string]string{"seg:1": `<strong>Hola</strong> &amp; <em>mundo</em>`})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if want := `<p><strong>Hola</strong> &amp; <em>mundo</em></p>`; !strings.Contains(string(out), want) {
		t.Errorf("editor formatting lost, want %s in:\n%s", want, out)
	}
}

// htmlBlocks decodes doc with the HTML tokenizer, independently of the parser
// under test, and returns each <p> as its text with elements shown as [name].
func htmlBlocks(t *testing.T, doc []byte) []string {
	t.Helper()
	root, err := html.Parse(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("html parse: %v", err)
	}
	var blocks []string
	var cur *strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.ElementNode && n.Data == "p":
			cur = &strings.Builder{}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			blocks = append(blocks, cur.String())
			cur = nil
			return
		case n.Type == html.TextNode && cur != nil:
			cur.WriteString(n.Data)
		case n.Type == html.ElementNode && cur != nil:
			cur.WriteString("[" + n.Data + "]")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			cur.WriteString("[/" + n.Data + "]")
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return blocks
}
