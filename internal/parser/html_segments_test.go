package parser

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Segmentation: a fixed list of block elements starts and ends a segment,
// every other element is an inline tag, a block inside a block closes the outer
// segment, and translatable attributes are segments of their own.

// htmlFeed is what export hands Reconstruct for an untranslated file: rich
// text when the segment has chips, plain text otherwise.
func htmlFeed(segs []Segment, edit func(string) string) map[string]string {
	out := map[string]string{}
	for _, s := range segs {
		text := s.Text
		if s.RichText != "" {
			text = s.RichText
		}
		out[s.Key] = edit(text)
	}
	return out
}

func identity(s string) string { return s }

// bracket changes a translation without touching its chips.
func bracket(s string) string { return "T[" + s + "]" }

// parseAndReconstruct parses src, feeds every segment through edit and
// returns the segments and the exported file.
func parseAndReconstruct(t *testing.T, src []byte, edit func(string) string) ([]Segment, []byte) {
	t.Helper()
	segs, err := HTML{}.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := HTML{}.Reconstruct(src, htmlFeed(segs, edit))
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	return segs, out
}

// untouched renders src the way Reconstruct renders a file it changes nothing in.
func untouched(t *testing.T, src []byte) string {
	t.Helper()
	out, err := HTML{}.Reconstruct(src, nil)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	return string(out)
}

// bodyInner renders the children of doc's <body>, re-parsed independently of
// the parser under test.
func bodyInner(t *testing.T, doc []byte) string {
	t.Helper()
	root, err := html.Parse(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("html parse: %v", err)
	}
	var body *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" {
			body = n
			return
		}
		for c := n.FirstChild; c != nil && body == nil; c = c.NextSibling {
			find(c)
		}
	}
	find(root)
	var b bytes.Buffer
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			t.Fatalf("render: %v", err)
		}
	}
	return b.String()
}

func segTexts(segs []Segment) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.Text
	}
	return out
}

// Inline elements inside a block stay in the export, attributes and all,
// whether or not the translation changes the text.
func TestHTMLInlineElementsSurviveExport(t *testing.T) {
	cases := []struct {
		name, body string
		texts      []string
		translated string
	}{
		{
			name:       "img with alt",
			body:       `<p>Click <img src="i.png" alt="Icon"> now</p>`,
			texts:      []string{"Click  now", "Icon"},
			translated: `<p>T[Click <img src="i.png" alt="T[Icon]"/> now]</p>`,
		},
		{
			name:       "kbd",
			body:       `<p>Press <kbd>Ctrl</kbd>+<kbd>S</kbd> to save</p>`,
			texts:      []string{"Press Ctrl+S to save"},
			translated: `<p>T[Press <kbd>Ctrl</kbd>+<kbd>S</kbd> to save]</p>`,
		},
		{
			name:       "abbr with title",
			body:       `<p>Use the <abbr title="Application Programming Interface">API</abbr> key</p>`,
			texts:      []string{"Use the API key", "Application Programming Interface"},
			translated: `<p>T[Use the <abbr title="T[Application Programming Interface]">API</abbr> key]</p>`,
		},
		{
			name:       "small",
			body:       `<p>Price <small>excl. VAT</small></p>`,
			texts:      []string{"Price excl. VAT"},
			translated: `<p>T[Price <small>excl. VAT</small>]</p>`,
		},
		{
			name:       "mark",
			body:       `<p>The <mark>red</mark> wire</p>`,
			texts:      []string{"The red wire"},
			translated: `<p>T[The <mark>red</mark> wire]</p>`,
		},
		{
			name:       "span with class",
			body:       `<p>Model <span class="sku" data-id="7">X200</span> ships today</p>`,
			texts:      []string{"Model X200 ships today"},
			translated: `<p>T[Model <span class="sku" data-id="7">X200</span> ships today]</p>`,
		},
		{
			name:       "input placeholder",
			body:       `<p>Name: <input type="text" name="n" placeholder="Your name"> required</p>`,
			texts:      []string{"Name:  required", "Your name"},
			translated: `<p>T[Name: <input type="text" name="n" placeholder="T[Your name]"/> required]</p>`,
		},
		{
			name:       "empty icon element",
			body:       `<p><i class="icon-warn"></i> Hot surface</p>`,
			texts:      []string{" Hot surface"},
			translated: `<p>T[<i class="icon-warn"></i> Hot surface]</p>`,
		},
		{
			name:       "inline element marked translate no",
			body:       `<p>Call <span translate="no">ACME Support</span> now</p>`,
			texts:      []string{"Call  now"},
			translated: `<p>T[Call <span translate="no">ACME Support</span> now]</p>`,
		},
		{
			name:       "script inside a paragraph",
			body:       `<p>Hi<script>var a = 1;</script> there</p>`,
			texts:      []string{"Hi there"},
			translated: `<p>T[Hi<script>var a = 1;</script> there]</p>`,
		},
		{
			name:       "comment",
			body:       `<p>Hi <!-- note --> there</p>`,
			texts:      []string{"Hi  there"},
			translated: `<p>T[Hi <!-- note --> there]</p>`,
		},
		{
			name:       "line break",
			body:       `<p>a<br>b</p>`,
			texts:      []string{"a\nb"},
			translated: `<p>T[a<br/>b]</p>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(`<html><head></head><body>` + tc.body + `</body></html>`)

			segs, same := parseAndReconstruct(t, src, identity)
			if got := segTexts(segs); strings.Join(got, "|") != strings.Join(tc.texts, "|") {
				t.Errorf("segments = %q, want %q", got, tc.texts)
			}
			if want := untouched(t, src); string(same) != want {
				t.Errorf("untranslated export changed the file:\n got %s\nwant %s", same, want)
			}

			_, out := parseAndReconstruct(t, src, bracket)
			if got := bodyInner(t, out); got != tc.translated {
				t.Errorf("translated export:\n got %s\nwant %s", got, tc.translated)
			}
		})
	}
}

// A block inside a block closes the outer segment: the text before and after
// it are segments of their own, and the nested block keeps its place.
func TestHTMLNestedBlocksSplitSegments(t *testing.T) {
	cases := []struct {
		name, body string
		texts      []string
		translated string
	}{
		{
			name:       "list in a list item",
			body:       `<ul><li>A<ul><li>B</li></ul>C</li></ul>`,
			texts:      []string{"A", "B", "C"},
			translated: `<ul><li>T[A]<ul><li>T[B]</li></ul>T[C]</li></ul>`,
		},
		{
			name:       "paragraph in a blockquote",
			body:       `<blockquote><p>x</p></blockquote>`,
			texts:      []string{"x"},
			translated: `<blockquote><p>T[x]</p></blockquote>`,
		},
		{
			name:       "paragraph in a table cell",
			body:       `<table><tbody><tr><td><p>x</p></td></tr></tbody></table>`,
			texts:      []string{"x"},
			translated: `<table><tbody><tr><td><p>T[x]</p></td></tr></tbody></table>`,
		},
		{
			name:       "loose text around a paragraph",
			body:       `<div>loose<p>para</p>tail</div>`,
			texts:      []string{"loose", "para", "tail"},
			translated: `<div>T[loose]<p>T[para]</p>T[tail]</div>`,
		},
		{
			name:       "link around a block",
			body:       `<a href="/card" title="Open"><div>Card</div></a>`,
			texts:      []string{"Card", "Open"},
			translated: `<a href="/card" title="T[Open]"><div>T[Card]</div></a>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(`<html><head></head><body>` + tc.body + `</body></html>`)

			segs, same := parseAndReconstruct(t, src, identity)
			if got := segTexts(segs); strings.Join(got, "|") != strings.Join(tc.texts, "|") {
				t.Errorf("segments = %q, want %q", got, tc.texts)
			}
			if want := untouched(t, src); string(same) != want {
				t.Errorf("untranslated export changed the file:\n got %s\nwant %s", same, want)
			}

			_, out := parseAndReconstruct(t, src, bracket)
			if got := bodyInner(t, out); got != tc.translated {
				t.Errorf("translated export:\n got %s\nwant %s", got, tc.translated)
			}
		})
	}
}

// Attributes are translated only where the filter says so: a hidden, file,
// image or password input keeps its value, and other attributes are copied.
func TestHTMLTranslatableAttributes(t *testing.T) {
	src := []byte(`<html><head></head><body><form>` +
		`<input type="hidden" name="a" value="secret" title="Hidden title">` +
		`<input type="password" placeholder="Password">` +
		`<input type="submit" value="Send" alt="Send form">` +
		`</form><table><tbody><tr><th abbr="Qty">Quantity</th><td abbr="Ten">10 pieces</td></tr></tbody></table>` +
		`<p><a href="/x" title="More" class="c">Read</a> on</p></body></html>`)
	segs, out := parseAndReconstruct(t, src, bracket)

	want := []string{"Send", "Send form", "Quantity", "Qty", "10 pieces", "Ten", "Read on", "More"}
	if got := segTexts(segs); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("segments = %q, want %q", got, want)
	}
	wantBody := `<form>` +
		`<input type="hidden" name="a" value="secret" title="Hidden title"/>` +
		`<input type="password" placeholder="Password"/>` +
		`<input type="submit" value="T[Send]" alt="T[Send form]"/>` +
		`</form><table><tbody><tr><th abbr="T[Qty]">T[Quantity]</th><td abbr="T[Ten]">T[10 pieces]</td></tr></tbody></table>` +
		`<p>T[<a class="c" href="/x" title="T[More]">Read</a> on]</p>`
	if got := bodyInner(t, out); got != wantBody {
		t.Errorf("translated export:\n got %s\nwant %s", got, wantBody)
	}
}

// A segment whose text and place are what they were keeps its key, so the
// translations already stored for it still land; new segments get keys that
// sit between the old ones.
func TestHTMLSegmentKeysStayStable(t *testing.T) {
	src := []byte(`<html><head></head><body>` +
		`<p>One</p>` +
		`<div>loose<p>Two</p></div>` +
		`<p><img src="a.png" alt="Pic">Three</p>` +
		`<ul><li>A<ul><li>B</li></ul></li></ul>` +
		`<blockquote><p>Quote</p></blockquote>` +
		`<p>Four</p>` +
		`</body></html>`)
	segs, err := HTML{}.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{
		"seg:1=One",
		"seg:1.1=loose",
		"seg:2=Two",
		"seg:3=Three",
		"seg:3.1=Pic",
		"seg:4=A",
		"seg:4.1=B",
		"seg:5=Quote",
		"seg:6=Four",
	}
	var got []string
	for i, s := range segs {
		got = append(got, s.Key+"="+s.Text)
		if s.Position != i {
			t.Errorf("segment %s position = %d, want %d", s.Key, s.Position, i)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("keys:\n got %q\nwant %q", got, want)
	}
}

// A realistic help page: nothing is lost on export, every translatable piece
// is a segment, and code, scripts and hidden values stay as they are.
func TestHTMLHelpPageRoundTrip(t *testing.T) {
	src, err := os.ReadFile("testdata/html/help-page.html")
	if err != nil {
		t.Fatal(err)
	}

	// The export differs from the source only in markup whitespace at the edges
	// of a segment, which the browser does not render; a space between inline
	// elements stays.
	segs, same := parseAndReconstruct(t, src, identity)
	want, err := os.ReadFile("testdata/html/help-page.identity-export.html")
	if err != nil {
		t.Fatal(err)
	}
	if string(same) != string(want) {
		t.Errorf("untranslated export changed the file:\n got %s\nwant %s", same, want)
	}

	keys := map[string]string{}
	for _, s := range segs {
		if prev, dup := keys[s.Key]; dup {
			t.Errorf("key %s used by %q and %q", s.Key, prev, s.Text)
		}
		keys[s.Key] = strings.Join(strings.Fields(s.Text), " ")
	}
	wantKeys := map[string]string{
		"seg:1":    "How to set up your router in five minutes.",
		"seg:2":    "Router setup guide",
		"seg:2.1":  "Back to the home page",
		"seg:2.2":  "Example Networks",
		"seg:3":    "Support",
		"seg:4":    "Downloads",
		"seg:4.1":  "Firmware and manuals",
		"seg:5":    "Set up your router",
		"seg:6":    "Plug the WAN cable into the yellow port, then press Reset for 5 seconds.",
		"seg:6.1":  "Wide Area Network",
		"seg:7":    "Look for this icon on the front panel.",
		"seg:7.1":  "Wi-Fi icon",
		"seg:7.2":  "Wireless status",
		"seg:7.3":  "Before you start:",
		"seg:8":    "Keep the box and the X200 manual nearby.",
		"seg:8.1":  "Prices may vary.",
		"seg:9":    "Open the app.",
		"seg:9.1":  "On Android, use Settings.",
		"seg:9.2":  "On iOS, use the Home tab.",
		"seg:9.3":  "Then sign in.",
		"seg:10":   "Choose a network name.",
		"seg:11":   "The setup took me two minutes.",
		"seg:12":   "Light",
		"seg:13":   "What it means",
		"seg:13.1": "Meaning",
		"seg:14":   "Green",
		"seg:15":   "Connected to the internet.",
		"seg:15.1": "Search the help",
		"seg:15.2": "Type a question",
		"seg:15.3": "Search field",
		"seg:15.4": "Search",
		"seg:16":   "Last updated October 1.",
		"seg:17":   "© Example Networks. Privacy",
	}
	for k, want := range wantKeys {
		if keys[k] != want {
			t.Errorf("%s = %q, want %q", k, keys[k], want)
		}
	}
	for k, got := range keys {
		if _, ok := wantKeys[k]; !ok {
			t.Errorf("unexpected segment %s = %q", k, got)
		}
	}

	_, out := parseAndReconstruct(t, src, bracket)
	if got, want := elementOutline(t, out), elementOutline(t, []byte(untouched(t, src))); got != want {
		t.Errorf("translated export changed the element structure:\n got %s\nwant %s", got, want)
	}
	s := string(out)
	for _, want := range []string{
		`alt="T[Example Networks]"`,
		`title="T[Back to the home page]"`,
		`<abbr title="T[Wide Area Network]">WAN</abbr>`,
		`<kbd>Reset</kbd>`,
		`<mark>yellow</mark>`,
		`<img src="/img/wifi.png" alt="T[Wi-Fi icon]" title="T[Wireless status]"/>`,
		`placeholder="T[Type a question]"`,
		`<input type="hidden" name="lang" value="en"/>`,
		`<input type="submit" value="T[Search]"/>`,
		`<time datetime="2026-10-01">October 1</time>`,
		`<pre>ping 192.168.0.1</pre>`,
		`<script>window.dataLayer = window.dataLayer || [];</script>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("export lacks %s:\n%s", want, s)
		}
	}
}

// elementOutline lists every element of doc in document order with its
// attribute names, so a lost or added element shows up whatever the text.
func elementOutline(t *testing.T, doc []byte) string {
	t.Helper()
	root, err := html.Parse(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("html parse: %v", err)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			b.WriteString("<" + n.Data)
			for _, a := range n.Attr {
				b.WriteString(" " + a.Key)
			}
			b.WriteString(">")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return b.String()
}

// Chips are matched by id, so a translation that reorders them gets each
// source element back in its new place.
func TestHTMLReorderedChipsKeepTheirElements(t *testing.T) {
	src := []byte(`<html><head></head><body><p>Read <a href="/a">the guide</a> and <img src="i.png" alt="tip"> <kbd>F1</kbd></p></body></html>`)
	segs, err := HTML{}.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %+v, want the run and the alt", segs)
	}
	translated := `<g id="3" ctype="x-html-kbd">F1</g> <x id="2" ctype="x-html-img"/> und <g id="1" ctype="x-html-a">die Anleitung</g> lesen`
	out, err := HTML{}.Reconstruct(src, map[string]string{segs[0].Key: translated, segs[1].Key: "Tipp"})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	want := `<p><kbd>F1</kbd> <img src="i.png" alt="Tipp"/> und <a href="/a">die Anleitung</a> lesen</p>`
	if got := bodyInner(t, out); got != want {
		t.Errorf("export:\n got %s\nwant %s", got, want)
	}
}

// A translated attribute is text: quotes and markup in it stay inside the
// attribute.
func TestHTMLTranslatedAttributeIsEscaped(t *testing.T) {
	src := []byte(`<html><head></head><body><p>See <img src="i.png" alt="Logo"></p></body></html>`)
	segs, err := HTML{}.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 2 || segs[1].Context != "img:alt" {
		t.Fatalf("segments = %+v, want the run and the alt", segs)
	}
	out, err := HTML{}.Reconstruct(src, map[string]string{segs[1].Key: `"><script>alert(1)</script>`})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if strings.Contains(string(out), "<script>") {
		t.Errorf("attribute translation became markup:\n%s", out)
	}
	want := `<p>See <img src="i.png" alt="&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"/></p>`
	if got := bodyInner(t, out); got != want {
		t.Errorf("export:\n got %s\nwant %s", got, want)
	}
}
