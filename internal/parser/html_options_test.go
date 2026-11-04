package parser

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func readHTMLFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/html/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func guillemets(s string) string { return "«" + s + "»" }

func withAttrs(extra map[string][]string) *HTMLOptions {
	o := DefaultHTMLOptions()
	for elem, attrs := range extra {
		o.Attributes[elem] = attrs
	}
	return &o
}

func segmentByContext(segs []Segment, context string) (Segment, bool) {
	for _, s := range segs {
		if s.Context == context {
			return s, true
		}
	}
	return Segment{}, false
}

// No options, the default options and the default options read back from
// JSON all give the segments, keys and exported bytes of the
// *.default-segments.json and *.default-export.html goldens.
func TestHTMLOptions_DefaultsMatchGoldens(t *testing.T) {
	def := DefaultHTMLOptions()
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var decoded HTMLOptions
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	options := map[string]*HTMLOptions{"nil": nil, "default": &def, "default via JSON": &decoded}

	for _, fixture := range []string{"help-page", "options"} {
		src := readHTMLFixture(t, fixture+".html")
		var wantSegs []Segment
		if err := json.Unmarshal(readHTMLFixture(t, fixture+".default-segments.json"), &wantSegs); err != nil {
			t.Fatal(err)
		}
		wantExport := readHTMLFixture(t, fixture+".default-export.html")
		for name, opts := range options {
			t.Run(fixture+"/"+name, func(t *testing.T) {
				p := HTML{Options: opts}
				segs, err := p.Parse(src)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(segs, wantSegs) {
					t.Fatalf("segments differ from the golden:\n got %+v\nwant %+v", segs, wantSegs)
				}
				out, err := p.Reconstruct(src, htmlFeed(segs, guillemets))
				if err != nil {
					t.Fatal(err)
				}
				if string(out) != string(wantExport) {
					t.Fatalf("export differs from the golden:\n got %s\nwant %s", out, wantExport)
				}
			})
		}
	}
}

func TestHTMLOptions_Attributes(t *testing.T) {
	src := readHTMLFixture(t, "options.html")
	tests := []struct {
		name        string
		opts        *HTMLOptions
		context     string
		text        string
		wantInFile  string
		absentCtxts []string
	}{
		{
			name:       "aria-label on button",
			opts:       withAttrs(map[string][]string{"button": {"aria-label"}}),
			context:    "button:aria-label",
			text:       "Close the dialog",
			wantInFile: `aria-label="«Close the dialog»"`,
		},
		{
			name:       "custom element, names in any case",
			opts:       withAttrs(map[string][]string{"My-Widget": {"LABEL"}}),
			context:    "my-widget:label",
			text:       "Custom label",
			wantInFile: `label="«Custom label»"`,
		},
		{
			name:        "a hidden input and a URL attribute stay untranslated",
			opts:        withAttrs(map[string][]string{"input": {"value"}, "a": {"title", "href"}}),
			context:     "input:value",
			text:        "Your name",
			wantInFile:  `<input type="hidden" value="secret-token"/>`,
			absentCtxts: []string{"a:href"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := HTML{Options: tt.opts}
			segs, err := p.Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			seg, ok := segmentByContext(segs, tt.context)
			if !ok || seg.Text != tt.text {
				t.Fatalf("segment %s = %+v (found %v), want text %q", tt.context, seg, ok, tt.text)
			}
			for _, c := range tt.absentCtxts {
				if s, ok := segmentByContext(segs, c); ok {
					t.Errorf("unexpected segment %+v", s)
				}
			}
			for _, s := range segs {
				if s.Text == "secret-token" {
					t.Errorf("hidden input value became a segment: %+v", s)
				}
			}
			out, err := p.Reconstruct(src, htmlFeed(segs, guillemets))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), tt.wantInFile) {
				t.Fatalf("export lacks %s:\n%s", tt.wantInFile, out)
			}
			if strings.Contains(string(out), `href="«`) {
				t.Fatalf("export translated a URL attribute:\n%s", out)
			}
		})
	}
}

func TestHTMLOptions_MetaDescription(t *testing.T) {
	src := readHTMLFixture(t, "options.html")
	opts := DefaultHTMLOptions()
	opts.MetaDescription = false
	p := HTML{Options: &opts}

	segs, err := p.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := segmentByContext(segs, "meta:description"); ok {
		t.Fatalf("meta description is a segment: %+v", s)
	}
	if title, ok := segmentByContext(segs, ""); !ok || title.Key != "seg:1" || title.Text != "Options page" {
		t.Fatalf("first segment = %+v, want the title as seg:1", title)
	}
	out, err := p.Reconstruct(src, htmlFeed(segs, guillemets))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`content="A page about import options."`, `<title>«Options page»</title>`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("export lacks %s:\n%s", want, out)
		}
	}
}

func TestHTMLOptions_SkipPre(t *testing.T) {
	fixture := readHTMLFixture(t, "options.html")
	const fixturePre = "line one\n  indented   line two"
	trailing := []byte("<html><body><pre>x := 1  </pre></body></html>")
	keep := func(s string) string { return s }
	tests := []struct {
		name    string
		src     []byte
		preText string
		skipPre bool
		edit    func(string) string
		want    string
	}{
		{"skipped", fixture, fixturePre, true, guillemets, "<pre>" + fixturePre + "</pre>"},
		{"translated", fixture, fixturePre, false, guillemets, "<pre>«" + fixturePre + "»</pre>"},
		{"translated, unchanged", fixture, fixturePre, false, keep, "<pre>" + fixturePre + "</pre>"},
		{"trailing whitespace kept", trailing, "x := 1  ", false, guillemets, "<pre>«x := 1  »</pre>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultHTMLOptions()
			opts.SkipPre = tt.skipPre
			p := HTML{Options: &opts}
			segs, err := p.Parse(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, s := range segs {
				found = found || s.Text == tt.preText
			}
			if found == tt.skipPre {
				t.Fatalf("<pre> segment found = %v with SkipPre %v: %+v", found, tt.skipPre, segs)
			}
			out, err := p.Reconstruct(tt.src, htmlFeed(segs, tt.edit))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Fatalf("export lacks %q:\n%s", tt.want, out)
			}
		})
	}
}

// The export parser built from stored sources applies the file's options.
func TestHTMLWithStoredSourceOptions(t *testing.T) {
	src := readHTMLFixture(t, "options.html")
	opts := withAttrs(map[string][]string{"button": {"aria-label"}})
	p := HTMLWithStoredSourceOptions(map[string]StoredSource{}, nil, opts)

	segs, err := p.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	seg, ok := segmentByContext(segs, "button:aria-label")
	if !ok {
		t.Fatalf("no button:aria-label segment: %+v", segs)
	}
	out, err := p.Reconstruct(src, map[string]string{seg.Key: "Fermer"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `aria-label="Fermer"`) {
		t.Fatalf("export lacks the translated aria-label:\n%s", out)
	}
}

func TestHTMLOptions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		attrs   map[string][]string
		wantErr bool
	}{
		{"default", DefaultHTMLOptions().Attributes, false},
		{"aria-label", map[string][]string{"button": {"aria-label"}}, false},
		{"event handler", map[string][]string{"button": {"onclick"}}, true},
		{"event handler, upper case", map[string][]string{"img": {"OnError"}}, true},
		{"href", map[string][]string{"a": {"href"}}, true},
		{"style", map[string][]string{"span": {"style"}}, true},
		{"data attribute", map[string][]string{"span": {"data-tooltip"}}, true},
		{"data-src", map[string][]string{"img": {"data-src"}}, true},
		{"value of input", map[string][]string{"input": {"value"}}, false},
		{"value of param", map[string][]string{"param": {"value"}}, true},
		{"value of option", map[string][]string{"option": {"value"}}, true},
		{"meta with a text attribute", map[string][]string{"meta": {"title"}}, true},
		{"link with a text attribute", map[string][]string{"link": {"title"}}, true},
		{"html with a text attribute", map[string][]string{"html": {"title"}}, true},
		{"label", map[string][]string{"option": {"label"}}, false},
		{"meta content", map[string][]string{"meta": {"content"}}, true},
		{"meta http-equiv", map[string][]string{"meta": {"http-equiv"}}, true},
		{"ping", map[string][]string{"a": {"ping"}}, true},
		{"imagesrcset", map[string][]string{"link": {"imagesrcset"}}, true},
		{"manifest", map[string][]string{"html": {"manifest"}}, true},
		{"longdesc", map[string][]string{"img": {"longdesc"}}, true},
		{"aria reference", map[string][]string{"div": {"aria-labelledby"}}, true},
		{"empty attribute", map[string][]string{"span": {" "}}, true},
		{"empty element", map[string][]string{"": {"title"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := HTMLOptions{Attributes: tt.attrs}.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

// A pick Validate refuses never reaches a parse either: options read back
// from a stored file that name a page-configuring element or a non-text
// attribute translate nothing there.
func TestHTMLOptions_ConfigDropsRefusedPicks(t *testing.T) {
	src := []byte(`<html><head><meta name="keywords" title="Meta title"></head><body><p data-tip="Tip text">Hello</p></body></html>`)
	opts := &HTMLOptions{Attributes: map[string][]string{"meta": {"title"}, "p": {"data-tip"}}, SkipPre: true}
	segs, err := HTML{Options: opts}.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range segs {
		if s.Text == "Meta title" || s.Text == "Tip text" {
			t.Errorf("segment %q comes from a refused pick", s.Text)
		}
	}
}
