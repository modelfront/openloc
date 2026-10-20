package parser

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// The JSON, PO, TXT and XLSX exports keep a tag only when the source string
// has it: the source's own link comes back as in the source, and a tag the
// translation adds — typed as text or forged into a chip — is written as text.
func TestTextFormatExportKeepsOnlySourceTags(t *testing.T) {
	const source = `Click <a href="https://example.com/">here</a> now`
	const typed = ` <img src=x onerror=alert(1)><b>bold</b>`

	formats := []struct {
		name, format string
		original     []byte
		export       func(t *testing.T, original []byte, translations map[string]string) string
	}{
		{"txt", "txt", []byte(source + "\n"), func(t *testing.T, original []byte, tr map[string]string) string {
			out, err := TXT{}.Reconstruct(original, tr)
			if err != nil {
				t.Fatalf("reconstruct: %v", err)
			}
			return strings.TrimSuffix(string(out), "\n")
		}},
		{"json", "json_i18n", mustJSON(t, map[string]string{"k": source}), func(t *testing.T, original []byte, tr map[string]string) string {
			out, err := JSONi18n{}.Reconstruct(original, tr)
			if err != nil {
				t.Fatalf("reconstruct: %v", err)
			}
			var got map[string]string
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("decode export: %v\n%s", err, out)
			}
			return got["k"]
		}},
		{"po", "po", []byte("msgid " + strconv.Quote(source) + "\nmsgstr \"\"\n"), func(t *testing.T, original []byte, tr map[string]string) string {
			out, err := PO{}.Reconstruct(original, tr)
			if err != nil {
				t.Fatalf("reconstruct: %v", err)
			}
			for _, line := range strings.Split(string(out), "\n") {
				if rest, ok := strings.CutPrefix(line, "msgstr "); ok {
					v, err := strconv.Unquote(rest)
					if err != nil {
						t.Fatalf("unquote msgstr %s: %v", rest, err)
					}
					return v
				}
			}
			t.Fatalf("no msgstr in export:\n%s", out)
			return ""
		}},
		{"xlsx", "xlsx", buildXLSX(t, []string{"en", "fr"}, [][]string{{source, ""}}), func(t *testing.T, original []byte, tr map[string]string) string {
			out, err := exportXLSX(t, original, tr, "fr")
			if err != nil {
				t.Fatalf("reconstruct: %v", err)
			}
			return readCells(t, out, "B2")["B2"]
		}},
	}

	for _, f := range formats {
		t.Run(f.name, func(t *testing.T) {
			p, err := GetParser(f.format)
			if err != nil {
				t.Fatalf("GetParser: %v", err)
			}
			segs, err := p.Parse(f.original)
			if err != nil || len(segs) != 1 || !strings.Contains(segs[0].RichText, "original_markup=") {
				t.Fatalf("source segment has no link chip: %+v (%v)", segs, err)
			}
			forged := `&lt;a href=&quot;javascript:alert(1)&quot;&gt;`
			rich := originalMarkupAttrRE.ReplaceAllString(segs[0].RichText, `original_markup="`+forged+`"`)
			rich = strings.Replace(rich, ">here<", ">ici<", 1) + typed

			got := f.export(t, f.original, map[string]string{segs[0].Key: rich})
			want := `Click <a href="https://example.com/">ici</a> now &lt;img src=x onerror=alert(1)&gt;&lt;b&gt;bold&lt;/b&gt;`
			if got != want {
				t.Errorf("exported string =\n %q\nwant\n %q", got, want)
			}
		})
	}
}

// A '<' that opens markup but is not a whole source tag is text, so a
// malformed tag cannot slip past the rule; one that cannot open markup stays.
func TestSourceInlineMarkupEscapesMalformedTags(t *testing.T) {
	const source = "Click here"
	for _, typed := range []string{
		`<svg/onload=alert(1)>`,
		`<img src=x onerror="alert(1)//<">`,
	} {
		out := sourceInlineMarkup("Klick "+typed, source)
		if strings.Contains(out, "<svg") || strings.Contains(out, "<img") {
			t.Errorf("%s: markup reached the export: %q", typed, out)
		}
		if len(ForeignTags("Klick "+typed, source)) == 0 {
			t.Errorf("%s: ForeignTags reported nothing", typed)
		}
	}
	if out := sourceInlineMarkup("a < b, 3<4", source); out != "a &lt; b, 3&lt;4" {
		t.Errorf("prose with '<': export wrote %q, want each '<' as an entity", out)
	}
}

// Markup the source has that is not a letter-named tag — i18next's `<0>…</0>`,
// a comment — is the source's own and stays as it is.
func TestSourceInlineMarkupKeepsSourceOwnMarkup(t *testing.T) {
	for _, c := range []struct{ source, translated string }{
		{`<0>Hello</0> world`, `<0>Hallo</0> Welt`},
		{`Hello <!-- note --> world`, `Hallo <!-- note --> Welt`},
	} {
		if out := sourceInlineMarkup(c.translated, c.source); out != c.translated {
			t.Errorf("source %q: export changed %q to %q", c.source, c.translated, out)
		}
		if foreign := ForeignTags(c.translated, c.source); len(foreign) != 0 {
			t.Errorf("source %q: reported %q as foreign", c.source, foreign)
		}
	}
}

// A tag or comment cut off at the end of a string is not refused, but its
// '<' is written as an entity: markup after the string in the customer's page
// would otherwise close it. In an HTML string the entity reads the same.
func TestSourceInlineMarkupUnclosedIsText(t *testing.T) {
	for _, c := range []struct{ source, translated, want string }{
		{"if x<y then", "si x<y entonces", "si x&lt;y entonces"},
		{"Click here", "Klick <img src=x onerror=alert(1)", "Klick &lt;img src=x onerror=alert(1)"},
		{"Click here", "Klick <!-- hidden", "Klick &lt;!-- hidden"},
		{"Click here", "Klick </b", "Klick &lt;/b"},
		{"Click here", "Klick </", "Klick &lt;/"},
		{"Click here", "Klick <!-- <img src=x onerror=alert(1)", "Klick &lt;!-- &lt;img src=x onerror=alert(1)"},
		{"Click here", `Klick <a title="<img src=x onerror=alert(1) `, `Klick &lt;a title="&lt;img src=x onerror=alert(1) `},
		{"a < b", "a < b and c <", "a &lt; b and c &lt;"},
	} {
		if out := sourceInlineMarkup(c.translated, c.source); out != c.want {
			t.Errorf("%q: export wrote %q, want %q", c.translated, out, c.want)
		}
		if foreign := ForeignTags(c.translated, c.source); len(foreign) != 0 {
			t.Errorf("%q: reported %q as foreign", c.translated, foreign)
		}
	}
	// A plain-text string is written as it is.
	if out := exportString("si x<y entonces <img", "if x<y then", true); out != "si x<y entonces <img" {
		t.Errorf("plain text changed to %q", out)
	}
}

// A source tag that parses is reused whatever its quotes hold: an apostrophe
// in a double-quoted title is common English.
func TestSourceInlineMarkupReusesTagWithApostrophe(t *testing.T) {
	const source = `See <a href="/help" title="What's new">news</a>`
	const translated = `Siehe <a href="/help" title="What's new">Neues</a>`
	if out := sourceInlineMarkup(translated, source); out != translated {
		t.Errorf("export changed %q to %q", translated, out)
	}
	if foreign := ForeignTags(translated, source); len(foreign) != 0 {
		t.Errorf("reported %q as foreign", foreign)
	}
}

// Broken or dangerous source markup is never a template a translation can
// reuse: an unclosed token, a tag that does not parse, a script or style.
func TestSourceInlineMarkupDoesNotReuseBrokenSourceMarkup(t *testing.T) {
	for _, c := range []struct{ source, translated, bad string }{
		{"<img src=x", "<img src=x<1 onerror=alert(1)>", "<img"},
		{`<a href="x>here`, `<a href="x>" onmouseover=alert(1) >hier`, "<a "},
		{"Put it in a <script> tag", "<script>alert(1)</script>", "<script>"},
		{`<style>p{}</style> Hi`, `<style>body{display:none}</style> Hallo`, "<style>"},
		{`<a/title="x>Hi`, `<a/title="x>" onfocus="alert(1)" autofocus x="`, `<a/`},
		{`<style/x>Hi`, `<style/x>*{color:red}`, `<style/`},
		{`<a b=x" c="d>Hi`, `<a b=x" c="d>" onmouseover="alert(1)" >`, `<a b`},
	} {
		if out := sourceInlineMarkup(c.translated, c.source); strings.Contains(out, c.bad) {
			t.Errorf("source %q: %q reached the export: %q", c.source, c.bad, out)
		}
		if len(ForeignTags(c.translated, c.source)) == 0 {
			t.Errorf("source %q: ForeignTags reported nothing for %q", c.source, c.translated)
		}
	}
}

// A source element with attributes does not allow the same element bare: a
// source `<script src>` must not let a translation add an inline script.
func TestSourceInlineMarkupBareTagOnlyForChipElements(t *testing.T) {
	const source = `<script src="/a.js"></script> Hello`
	const typed = `<script>alert(1)</script> Hallo`
	if out := sourceInlineMarkup(typed, source); strings.Contains(out, "<script>") {
		t.Errorf("bare script reached the export: %q", out)
	}
	if len(ForeignTags(typed, source)) == 0 {
		t.Error("ForeignTags did not report the bare script")
	}
}

// Shift+Enter in a segment with chips saves a line-break chip; it is a line
// break, not a tag the source lacks.
func TestSourceInlineMarkupKeepsLineBreakChip(t *testing.T) {
	const source = `Click <a href="https://example.com/">here</a> now`
	_, sourceRich := DetectInlineTags(source)
	translated := strings.Replace(sourceRich, " now", `<x id="9" ctype="lb" original_tag="br"/>jetzt`, 1)
	out := sourceInlineMarkup(translated, source)
	if !strings.Contains(out, "<br>jetzt") {
		t.Errorf("line break lost: %q", out)
	}
	if foreign := ForeignTags(XLIFFInlineToHTML(translated), source); len(foreign) != 0 {
		t.Errorf("line break reported as a foreign tag: %q", foreign)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
