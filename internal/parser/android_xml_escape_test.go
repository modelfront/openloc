package parser

import (
	"strings"
	"testing"
)

// androidSegmentsByKey parses a strings.xml document and indexes its
// segments by key.
func androidSegmentsByKey(t *testing.T, doc string) map[string]Segment {
	t.Helper()
	segs, err := AndroidXML{}.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := map[string]Segment{}
	for _, s := range segs {
		out[s.Key] = s
	}
	return out
}

// androidSegments is androidSegmentsByKey for the elements of one <resources>.
func androidSegments(t *testing.T, elements string) map[string]Segment {
	t.Helper()
	return androidSegmentsByKey(t, `<resources>`+elements+`</resources>`)
}

// The translator sees what the app shows: Android's backslash escapes and
// quoting are decoded, and whitespace collapses the way aapt2 collapses it.
func TestAndroidXML_BackslashEscapesDecoded(t *testing.T) {
	segs := androidSegments(t, `
		<string name="esc">It\'s \"quoted\" a\\b\nnext\ttab é \@home</string>
		<string name="at">\@string/not_a_ref</string>
		<string name="q">\?attr</string>
		<string name="ws">  Hello   
		      world  </string>
		<string name="quoted">"  It's  spaced  "</string>
		<string name="part">Say "  two  " now</string>
		<string name="trail">Line\n</string>`)

	for key, want := range map[string]string{
		"esc":    "It's \"quoted\" a\\b\nnext\ttab é @home",
		"at":     "@string/not_a_ref",
		"q":      "?attr",
		"ws":     "Hello world",
		"quoted": "  It's  spaced  ",
		"part":   "Say   two   now",
		"trail":  "Line\n",
	} {
		if got := segs[key].Text; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// Export escapes what Android needs escaped, once: an unquoted string gets
// \' \" \\ \n \t and a leading \@ or \?; a string the developer wrapped in
// quotes stays wrapped, with ' left as it is.
func TestAndroidXML_BackslashEscapesEncoded(t *testing.T) {
	input := `<resources>
<string name="plain">x</string>
<string name="at">x</string>
<string name="quoted">"It's"</string>
</resources>`
	out, err := AndroidXML{}.Reconstruct([]byte(input), map[string]string{
		"plain":  "It's \"x\" a\\b\nc\td",
		"at":     "@home? yes",
		"quoted": "L'été \"chaud\"",
	})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	for _, want := range []string{
		`<string name="plain">It\'s \"x\" a\\b\nc\td</string>`,
		`<string name="at">\@home? yes</string>`,
		`<string name="quoted">"L'été \"chaud\""</string>`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %s\ngot:\n%s", want, out)
		}
	}

	// What was written reads back as the translation.
	segs, err := AndroidXML{}.Parse(out)
	if err != nil {
		t.Fatalf("Parse exported: %v", err)
	}
	for _, s := range segs {
		want := map[string]string{"plain": "It's \"x\" a\\b\nc\td", "at": "@home? yes", "quoted": "L'été \"chaud\""}[s.Key]
		if s.Text != want {
			t.Errorf("re-read %s = %q, want %q", s.Key, s.Text, want)
		}
	}
}

// Escaped markup carries its attribute quotes as \" in the file; the link
// still becomes a tag, and its URL survives the round trip.
func TestAndroidXML_EscapedLinkIsATag(t *testing.T) {
	segs := androidSegments(t, `<string name="link">See &lt;a href=\"https://example.com\"&gt;here&lt;/a&gt;</string>`)
	link := segs["link"]
	if link.Text != "See here" {
		t.Errorf("text = %q, want %q", link.Text, "See here")
	}
	if !strings.Contains(link.RichText, `ctype="x-html-a"`) || !strings.Contains(link.RichText, "https://example.com") {
		t.Errorf("rich = %q, want a link tag keeping its URL", link.RichText)
	}
}
