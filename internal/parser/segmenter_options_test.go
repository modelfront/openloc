package parser

import (
	"reflect"
	"strings"
	"testing"

	pt "openloc/internal/parser/pptxtest"
	"openloc/internal/segmentation"
)

// A file's rule set reaches every parser that splits sentences, for the parse
// and for the export's re-parse. The custom set breaks only after "!", so the
// sample splits differently from the conservative default.
const optionsSample = "One. Two! Three."

var (
	breakOnBang   = segmentation.MustCompile(segmentation.RuleSet{Rules: []segmentation.Rule{{Before: `!`, After: `\s`, Break: true}}})
	paragraphMode = segmentation.MustCompile(segmentation.RuleSet{Mode: segmentation.ModeParagraph})
	// japaneseToCaps "translates" the Japanese samples word by word.
	japaneseToCaps = strings.NewReplacer("注意しなさい", "NOTE", "注意", "NOTE", "次に進む", "NEXT", "次に", "NEXT", "進む", "GO")
)

func textsWith(t *testing.T, format, content string, seg *segmentation.Segmenter) []string {
	t.Helper()
	p, err := GetParserWith(format, ParseOptions{InlineHTML: true, Segmenter: seg})
	if err != nil {
		t.Fatal(err)
	}
	segs, err := p.Parse([]byte(content))
	if err != nil {
		t.Fatalf("parse %s: %v", format, err)
	}
	var out []string
	for _, s := range segs {
		out = append(out, s.Text)
	}
	return out
}

func TestParseOptions_SegmenterReachesEverySplittingParser(t *testing.T) {
	contents := map[string]string{
		"txt":       optionsSample + "\n",
		"json_i18n": `{"a": "` + optionsSample + `"}`,
		"html":      "<p>" + optionsSample + "</p>",
		"markdown":  optionsSample + "\n",
		"xml":       "<doc><p>" + optionsSample + "</p></doc>",
	}
	for format, content := range contents {
		t.Run(format, func(t *testing.T) {
			if got, want := textsWith(t, format, content, nil), []string{"One.", "Two!", "Three."}; !reflect.DeepEqual(got, want) {
				t.Errorf("no rule set (conservative) = %q, want %q", got, want)
			}
			if got, want := textsWith(t, format, content, breakOnBang), []string{"One. Two!", "Three."}; !reflect.DeepEqual(got, want) {
				t.Errorf("break after ! = %q, want %q", got, want)
			}
			if got, want := textsWith(t, format, content, paragraphMode), []string{optionsSample}; !reflect.DeepEqual(got, want) {
				t.Errorf("paragraph mode = %q, want %q", got, want)
			}
		})
	}
	// Plain-text mode wraps the same splitter.
	p, err := GetParserWith("txt", ParseOptions{InlineHTML: false, Segmenter: breakOnBang})
	if err != nil {
		t.Fatal(err)
	}
	segs, err := p.Parse([]byte(optionsSample + "\n"))
	if err != nil || len(segs) != 2 {
		t.Errorf("plain-text TXT with break after ! = %+v (%v), want 2 segments", segs, err)
	}
}

// Export joins the sentences of the rule set the file was parsed with: the
// keys of a parse with the custom set land on the right sentences only when
// the export re-parses with that set too.
func TestParseOptions_ExportJoinsWithTheFilesRuleSet(t *testing.T) {
	content := []byte("Intro. " + optionsSample + "\n")
	p, err := GetParserWith("txt", ParseOptions{InlineHTML: true, Segmenter: breakOnBang})
	if err != nil {
		t.Fatal(err)
	}
	segs, err := p.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	translations := map[string]string{}
	for _, s := range segs {
		translations[s.Key] = "[" + s.Text + "]"
	}
	out, err := p.Reconstruct(content, translations)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "[Intro. One. Two!] [Three.]\n"; got != want {
		t.Errorf("export with the file's rules = %q, want %q", got, want)
	}

	conservative, err := GetParserWith("txt", ParseOptions{InlineHTML: true})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := conservative.Reconstruct(content, translations); err != nil || string(out) == "[Intro. One. Two!] [Three.]\n" {
		t.Errorf("export with other rules = %q (%v); the keys should no longer line up", out, err)
	}
}

func TestParseOptions_SegmenterReachesPPTX(t *testing.T) {
	deck := pt.Build(pt.Deck{Slides: []pt.Slide{{
		Shapes: pt.TextBox(2, pt.P(pt.R("", optionsSample))) + pt.Picture(3, optionsSample),
		Image:  pt.PNG,
	}}})
	parse := func(seg *segmentation.Segmenter) []Segment {
		t.Helper()
		p, err := GetParserWith("pptx", ParseOptions{Segmenter: seg})
		if err != nil {
			t.Fatal(err)
		}
		segs, err := p.Parse(deck)
		if err != nil {
			t.Fatal(err)
		}
		return segs
	}
	if got, want := pptxTexts(parse(nil)), []string{"One.", "Two!", "Three.", "One.", "Two!", "Three."}; !reflect.DeepEqual(got, want) {
		t.Errorf("no rule set = %q, want %q", got, want)
	}
	segs := parse(breakOnBang)
	if got, want := pptxTexts(segs), []string{"One. Two!", "Three.", "One. Two!", "Three."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("break after ! (paragraph, then alt text) = %q, want %q", got, want)
	}

	translations := map[string]string{}
	for _, s := range segs {
		translations[s.Key] = strings.ToUpper(s.Text)
	}
	out, err := PPTX{Segmenter: breakOnBang}.Reconstruct(deck, translations)
	if err != nil {
		t.Fatal(err)
	}
	back, err := PPTX{Segmenter: breakOnBang}.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pptxTexts(back), []string{"ONE. TWO!", "THREE.", "ONE. TWO!", "THREE."}; !reflect.DeepEqual(got, want) {
		t.Errorf("exported deck reads back as %q, want %q", got, want)
	}
}

// A full-width terminator breaks with no space after it, so a tag can close
// or open right at the break. Each sentence keeps its own tags, and export
// puts the translations back.
func TestSentenceSegments_BreakWithoutSpaceNextToATag(t *testing.T) {
	anyLanguage, ok := segmentation.BuiltinByID(segmentation.DefaultID)
	if !ok {
		t.Fatal("no any-language built-in")
	}
	seg := segmentation.MustCompile(anyLanguage.RuleSet)
	for name, tc := range map[string]struct{ content, want string }{
		"closing tag": {"<p><b>注意。</b>次に進む。</p>", "<p><b>NOTE。</b>NEXT。</p>"},
		"opening tag": {"<p>注意。<b>次に</b>進む。</p>", "<p>NOTE。<b>NEXT</b>GO。</p>"},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := GetParserWith("html", ParseOptions{InlineHTML: true, Segmenter: seg})
			if err != nil {
				t.Fatal(err)
			}
			segs, err := p.Parse([]byte(tc.content))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := pptxTexts(segs), []string{"注意。", "次に進む。"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("segments = %q, want %q", got, want)
			}
			out, err := p.Reconstruct([]byte(tc.content), translateAll(segs, japaneseToCaps))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("export = %s, want it to hold %s", out, tc.want)
			}
		})
	}
}

func TestXMLAndPPTX_BreakWithoutSpaceNextToATag(t *testing.T) {
	anyLanguage, _ := segmentation.BuiltinByID(segmentation.DefaultID)
	seg := segmentation.MustCompile(anyLanguage.RuleSet)
	// XML breaks only after text outside any element, so a terminator inside
	// <b> keeps its block whole.
	for name, tc := range map[string]struct {
		content string
		want    []string
	}{
		"closing tag": {"<doc><p><b>注意。</b>次に進む。</p></doc>", []string{"注意。次に進む。"}},
		"opening tag": {"<doc><p>注意。<b>次に</b>進む。</p></doc>", []string{"注意。", "次に進む。"}},
	} {
		content := tc.content
		t.Run("xml "+name, func(t *testing.T) {
			x := XML{Segmenter: seg}
			segs, err := x.Parse([]byte(content))
			if err != nil {
				t.Fatal(err)
			}
			if got := pptxTexts(segs); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("segments = %q, want %q", got, tc.want)
			}
			out, err := x.Reconstruct([]byte(content), translateAll(segs, japaneseToCaps))
			if err != nil {
				t.Fatal(err)
			}
			if strings.ContainsAny(string(out), "注次") || !strings.Contains(string(out), "<b>") {
				t.Errorf("export = %s", out)
			}
		})
	}

	deck := pt.Build(pt.Deck{Slides: []pt.Slide{{Shapes: pt.TextBox(2, pt.P(pt.R("", "注意しなさい。"), pt.R(`b="1"`, "次に。")))}}})
	p := PPTX{Segmenter: seg}
	segs, err := p.Parse(deck)
	if err != nil {
		t.Fatal(err)
	}
	if got := pptxTexts(segs); !reflect.DeepEqual(got, []string{"注意しなさい。", "次に。"}) {
		t.Fatalf("pptx segments = %q", got)
	}
	out, err := p.Reconstruct(deck, translateAll(segs, japaneseToCaps))
	if err != nil {
		t.Fatal(err)
	}
	back, err := p.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := pptxTexts(back); !reflect.DeepEqual(got, []string{"NOTE。", "NEXT。"}) {
		t.Errorf("exported deck reads back as %q", got)
	}
}

// With the default (conservative) rules too: a sentence that starts inside an
// inline element after a space takes the element's start tag, so the export
// writes both translations and the element.
func TestXML_SentenceOpeningWithAnElementExportsWhole(t *testing.T) {
	content := []byte("<doc><p>Done here.<b> Next one.</b></p></doc>")
	segs, err := XML{}.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %+v, want 2", segs)
	}
	tr := map[string]string{segs[0].Key: "Fertig.", segs[1].Key: strings.ReplaceAll(segs[1].RichText, "Next one.", "Weiter.")}
	out, err := XML{}.Reconstruct(content, tr)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "<doc><p>Fertig.<b> Weiter.</b></p></doc>"; got != want {
		t.Errorf("export = %s, want %s", got, want)
	}
}

// HTML and XML exports re-parse with the file's rule set too, inline tags
// included.
func TestParseOptions_HTMLAndXMLExportWithTheFilesRuleSet(t *testing.T) {
	translate := func(segs []Segment) map[string]string {
		return translateAll(segs, strings.NewReplacer("Intro", "INTRO", "One", "ONE", "Two", "TWO", "Three", "THREE"))
	}
	wantTexts := []string{"Intro. One. Two!", "Three."}

	t.Run("html", func(t *testing.T) {
		content := []byte("<p>Intro. <b>One.</b> Two! Three.</p>")
		p, err := GetParserWith("html", ParseOptions{InlineHTML: true, Segmenter: breakOnBang})
		if err != nil {
			t.Fatal(err)
		}
		segs, err := p.Parse(content)
		if err != nil {
			t.Fatal(err)
		}
		if got := pptxTexts(segs); !reflect.DeepEqual(got, wantTexts) {
			t.Fatalf("segments = %q, want %q", got, wantTexts)
		}
		stored := map[string]StoredSource{}
		for _, s := range segs {
			stored[s.Key] = StoredSource{Text: s.Text, RichText: s.RichText}
		}
		out, err := HTMLWithStoredSource(stored, breakOnBang).Reconstruct(content, translate(segs))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "<p>INTRO. <b>ONE.</b> TWO! THREE.</p>") {
			t.Errorf("export = %s", out)
		}
	})

	t.Run("xml", func(t *testing.T) {
		content := []byte("<doc><p>Intro. <b>One.</b> Two! Three.</p></doc>")
		x := XML{Segmenter: breakOnBang}
		segs, err := x.Parse(content)
		if err != nil {
			t.Fatal(err)
		}
		if got := pptxTexts(segs); !reflect.DeepEqual(got, wantTexts) {
			t.Fatalf("segments = %q, want %q", got, wantTexts)
		}
		out, err := x.Reconstruct(content, translate(segs))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(out), "<doc><p>INTRO. <b>ONE.</b> TWO! THREE.</p></doc>"; got != want {
			t.Errorf("export = %s, want %s", got, want)
		}
	})
}
