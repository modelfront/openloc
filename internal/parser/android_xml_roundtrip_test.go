package parser

import (
	"strings"
	"testing"
)

// A realistic strings.xml: namespaces on <resources>, comments, real tags,
// escaped markup for Html.fromHtml, an <xliff:g> placeholder, CDATA, arrays
// and plurals.
const androidRoundTripInput = `<?xml version="1.0" encoding="utf-8"?>
<resources xmlns:tools="http://schemas.android.com/tools" xmlns:xliff="urn:oasis:names:tc:xliff:document:1.2">
    <!-- Welcome screen -->
    <string name="app_name" tools:ignore="MissingTranslation">Tom &amp; Jerry</string>
    <string name="styled">Tom is <b>bold</b></string>
    <string name="escaped">Tom is &lt;b&gt;bold&lt;/b&gt; &amp; &lt;i&gt;tall&lt;/i&gt;</string>
    <string name="count">Tom has <xliff:g id="count" example="3">%d</xliff:g> cats</string>
    <string name="raw"><![CDATA[Tom <u>x</u>]]></string>
    <string name="quote">Tom\'s</string>
    <string name="said">Tom said \"hi\"\nBye</string>
    <string name="quoted">"Tom's  place"</string>
    <string name="link">Tom &lt;a href=\"https://example.com\"&gt;here&lt;/a&gt;</string>
    <string name="debug" translatable="false">dbg &amp; co</string>
    <string-array name="colors">
        <item>Tom red</item>
        <item>Blue</item>
    </string-array>
    <plurals name="items">
        <item quantity="one">Tom %d item</item>
        <item quantity="other">%d items</item>
    </plurals>
</resources>
`

// Export changes only the strings that were translated: every other byte —
// namespaces, comments, indentation, untouched strings — stays as uploaded.
// Each translation here is the segment's own rich text with "Tom" → "Ana", so
// the expected output is the input with the same substitution.
func TestAndroidXML_RoundTripChangesOnlyTranslatedText(t *testing.T) {
	p := AndroidXML{}
	segs, err := p.Parse([]byte(androidRoundTripInput))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	translations := map[string]string{}
	for _, s := range segs {
		if s.Key == "raw" {
			continue // CDATA comes back escaped, not as CDATA; covered below.
		}
		src := s.RichText
		if src == "" {
			src = s.Text
		}
		translations[s.Key] = strings.Replace(src, "Tom", "Ana", 1)
	}

	got, err := p.Reconstruct([]byte(androidRoundTripInput), translations)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	want := strings.NewReplacer(
		">Tom &amp; Jerry<", ">Ana &amp; Jerry<",
		">Tom is <b>", ">Ana is <b>",
		">Tom is &lt;b&gt;", ">Ana is &lt;b&gt;",
		">Tom has <xliff", ">Ana has <xliff",
		">Tom\\'s<", ">Ana\\'s<",
		">Tom said", ">Ana said",
		`>"Tom's  place"<`, `>"Ana's  place"<`,
		">Tom &lt;a", ">Ana &lt;a",
		">Tom red<", ">Ana red<",
		">Tom %d item<", ">Ana %d item<",
	).Replace(androidRoundTripInput)
	if string(got) != want {
		t.Errorf("export changed more than the translated text.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Escaped markup is markup the app renders (Html.fromHtml), so the translator
// gets a tag chip, the same as for a real tag.
func TestAndroidXML_EscapedMarkupIsATag(t *testing.T) {
	byKey := androidSegmentsByKey(t, androidRoundTripInput)

	esc := byKey["escaped"]
	if esc.Text != "Tom is bold & tall" {
		t.Errorf("escaped text = %q, want %q", esc.Text, "Tom is bold & tall")
	}
	for _, want := range []string{`ctype="bold">bold</g>`, `ctype="italic">tall</g>`} {
		if !strings.Contains(esc.RichText, want) {
			t.Errorf("escaped rich = %q, want it to contain %s", esc.RichText, want)
		}
	}
	if got := byKey["app_name"].Text; got != "Tom & Jerry" {
		t.Errorf("app_name text = %q, want %q", got, "Tom & Jerry")
	}
	if got := byKey["raw"].Text; got != "Tom x" || !strings.Contains(byKey["raw"].RichText, `ctype="underlined">x</g>`) {
		t.Errorf("CDATA segment = %q / %q, want text %q with an underline tag", got, byKey["raw"].RichText, "Tom x")
	}
}

// A CDATA string comes back as the same markup, escaped: Android reads both
// the same way.
func TestAndroidXML_CDATAExportsEscaped(t *testing.T) {
	raw := androidSegmentsByKey(t, androidRoundTripInput)["raw"]
	got, err := AndroidXML{}.Reconstruct([]byte(androidRoundTripInput), map[string]string{"raw": strings.Replace(raw.RichText, "Tom", "Ana", 1)})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if want := `<string name="raw">Ana &lt;u&gt;x&lt;/u&gt;</string>`; !strings.Contains(string(got), want) {
		t.Errorf("output missing %s\ngot:\n%s", want, got)
	}
}
