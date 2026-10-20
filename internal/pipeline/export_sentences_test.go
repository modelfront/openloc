package pipeline

import (
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parser"
)

// A block split into sentences exports through the same path as an approved
// file: exportTranslations picks each sentence's stored form, the format's
// parser joins them, and the inline tags around and between the sentences
// come back. One sentence is left untranslated and keeps its source.
func TestExportJoinsSentenceSegments(t *testing.T) {
	cases := []struct {
		format, source, want string
		inlineHTML           bool
	}{
		{"txt", "Open <b>this</b>. Then <i>close</i> it.\n", "Abra <b>esto</b>. Then <i>close</i> it.\n", true},
		{"json_i18n", `{"k":"Open <b>this</b>. Then <i>close</i> it."}`, `{"k":"Abra <b>esto</b>. Then <i>close</i> it."}`, true},
		{"html", "<p>Open <b>this</b>. Then <i>close</i> it.</p>", "<p>Abra <b>esto</b>. Then <i>close</i> it.</p>", true},
	}
	for _, c := range cases {
		p, err := parser.GetParserFor(c.format, c.inlineHTML)
		if err != nil {
			t.Fatal(err)
		}
		segs, err := p.Parse([]byte(c.source))
		if err != nil || len(segs) != 2 {
			t.Fatalf("%s: parse = %+v, %v", c.format, segs, err)
		}
		// The first sentence is approved with its chip; the second has none.
		key, text, rich := segs[0].Key, "Abra esto.", strings.Replace(segs[0].RichText, ">this<", ">esto<", 1)
		rich = strings.Replace(rich, "Open ", "Abra ", 1)
		rows := []db.ListApprovedForExportRow{{SegmentKey: &key, ActiveText: &text, ActiveRichText: &rich, SourceText: segs[0].Text}}
		ep, err := exportParser(c.format, parser.ParseOptions{InlineHTML: c.inlineHTML}, []byte(c.source), rows)
		if err != nil {
			t.Fatal(err)
		}
		out, err := ep.Reconstruct([]byte(c.source), exportTranslations(rows, c.format, c.inlineHTML))
		if err != nil {
			t.Fatalf("%s: reconstruct: %v", c.format, err)
		}
		if !strings.Contains(string(out), c.want) {
			t.Errorf("%s: export = %s, want it to contain %s", c.format, out, c.want)
		}
	}
}
