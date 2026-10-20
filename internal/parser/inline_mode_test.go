package parser

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// A file whose strings are plain text has no chips: the source string is all
// text, and the export writes the translation as is, tags and entities
// included. The same file read as HTML gets its chips.
func TestPlainTextStringsHaveNoChipsAndExportAsIs(t *testing.T) {
	const source = `Use <b>bold</b> or <a href="/x">a link</a> &amp; more`
	const translated = `Utilisez <b>gras</b> ou <img src=x onerror=alert(1)> &amp; plus`

	formats := []struct {
		format   string
		original []byte
		exported func(t *testing.T, out []byte) string
	}{
		{"txt", []byte(source + "\n"), func(t *testing.T, out []byte) string {
			return strings.TrimSuffix(string(out), "\n")
		}},
		{"json_i18n", mustJSON(t, map[string]string{"k": source}), func(t *testing.T, out []byte) string {
			var got map[string]string
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("decode export: %v\n%s", err, out)
			}
			return got["k"]
		}},
		{"po", []byte("msgid " + strconv.Quote(source) + "\nmsgstr " + strconv.Quote(source) + "\n"), func(t *testing.T, out []byte) string {
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
		{"xlsx", buildXLSX(t, []string{"en", "fr"}, [][]string{{source, source}}), func(t *testing.T, out []byte) string {
			return readCells(t, out, "B2")["B2"]
		}},
	}

	for _, f := range formats {
		t.Run(f.format, func(t *testing.T) {
			html, err := GetParserFor(f.format, true)
			if err != nil {
				t.Fatalf("GetParserFor: %v", err)
			}
			if segs, err := html.Parse(f.original); err != nil || len(segs) != 1 || segs[0].RichText == "" {
				t.Fatalf("HTML strings: want chips, got %+v (%v)", segs, err)
			}

			p, err := GetParserFor(f.format, false)
			if err != nil {
				t.Fatalf("GetParserFor: %v", err)
			}
			segs, err := p.Parse(f.original)
			if err != nil || len(segs) != 1 {
				t.Fatalf("parse: %+v (%v)", segs, err)
			}
			if segs[0].Text != source || segs[0].RichText != "" {
				t.Errorf("source segment = %q / rich %q, want the string as is and no chips", segs[0].Text, segs[0].RichText)
			}
			if f.format == "po" && (segs[0].Target != source || segs[0].TargetRichText != "") {
				t.Errorf("target = %q / rich %q, want the msgstr as is and no chips", segs[0].Target, segs[0].TargetRichText)
			}

			tr := map[string]string{segs[0].Key: translated}
			var out []byte
			if lp, ok := p.(LangAwareReconstructor); ok {
				out, err = lp.ReconstructForLang(context.Background(), f.original, tr, ExportLangs{Target: "fr"})
			} else {
				out, err = p.Reconstruct(f.original, tr)
			}
			if err != nil {
				t.Fatalf("reconstruct: %v", err)
			}
			if got := f.exported(t, out); got != translated {
				t.Errorf("exported %q, want it as is: %q", got, translated)
			}
		})
	}

	t.Run("xlsx targets", func(t *testing.T) {
		content := buildXLSX(t, []string{"en", "fr"}, [][]string{{source, source}})
		targets, err := XLSX{PlainText: true}.ExtractTargets(content, []string{"fr"})
		if err != nil {
			t.Fatalf("ExtractTargets: %v", err)
		}
		if got := targets["fr"]["row_2"]; got.Text != source || got.RichText != "" {
			t.Errorf("target = %+v, want the cell as is and no chips", got)
		}
	})
}

// The setting applies to the four text formats only.
func TestGetParserForOtherFormatsIgnoresTheSetting(t *testing.T) {
	for _, format := range []string{"html", "xliff", "android_xml", "markdown"} {
		if InlineHTMLFormat(format) {
			t.Errorf("%s: InlineHTMLFormat = true", format)
		}
		a, errA := GetParserFor(format, false)
		b, errB := GetParser(format)
		if errA != nil || errB != nil || a != b {
			t.Errorf("%s: GetParserFor(false) = %#v, GetParser = %#v", format, a, b)
		}
	}
}
