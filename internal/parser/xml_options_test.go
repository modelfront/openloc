package parser

import (
	"encoding/json"
	"reflect"
	"testing"
)

// xmlTranslateAll translates every segment by prefixing its text, keeping its
// chips, so export touches every segment range.
func xmlTranslateAll(segments []Segment) map[string]string {
	out := make(map[string]string, len(segments))
	for _, s := range segments {
		text := s.Text
		if s.RichText != "" {
			text = s.RichText
		}
		out[s.Key] = "T: " + text
	}
	return out
}

func xmlSegmentTexts(segments []Segment) []string {
	out := make([]string, len(segments))
	for i, s := range segments {
		out[i] = s.Text
	}
	return out
}

func xmlParseAndExport(t *testing.T, p XML, src []byte) ([]Segment, []byte) {
	t.Helper()
	segments, err := p.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := p.Reconstruct(src, xmlTranslateAll(segments))
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	return segments, out
}

// TestXMLDefaultOptionsMatchNil — nil options are pinned by the reference
// tests (TestXMLParseMatchesReference, TestXMLExportMatchesDeliveredFile), so
// the explicit defaults, also after a JSON round trip, must give the same
// segments and export byte for byte.
func TestXMLDefaultOptionsMatchNil(t *testing.T) {
	def := DefaultXMLOptions()
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var stored XMLOptions
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	sources := map[string][]byte{
		"st4 source":    readFixture(t, st4SourcePath),
		"st4 delivered": readFixture(t, st4DeliveredPath),
		"every rule": []byte(`<doc><p>Say <b>hi</b> to <meta-ref>Eco</meta-ref> and <ph linkid="x_1">Boost</ph>.</p>` +
			`<p translate="no">Skip</p><p translatable="false">Skip</p><list><a>One</a> <span>Two</span></list></doc>`),
	}
	// Each default rule shows in the snippet: <b>, <a> and <span> are chips,
	// both references are placeholders, both markers skip.
	snippetTexts := []string{"Say hi to  and .", "One Two"}

	for name, src := range sources {
		wantSegs, wantOut := xmlParseAndExport(t, XML{}, src)
		if len(wantSegs) == 0 {
			t.Fatalf("%s: no segments", name)
		}
		if got := xmlSegmentTexts(wantSegs); name == "every rule" && !reflect.DeepEqual(got, snippetTexts) {
			t.Fatalf("every rule: texts = %q, want %q", got, snippetTexts)
		}
		for optName, opts := range map[string]*XMLOptions{"defaults": &def, "json round trip": &stored} {
			gotSegs, gotOut := xmlParseAndExport(t, XML{Options: opts}, src)
			if !reflect.DeepEqual(gotSegs, wantSegs) {
				t.Errorf("%s, %s: segments differ from nil options", name, optName)
			}
			if string(gotOut) != string(wantOut) {
				t.Errorf("%s, %s: export differs from nil options", name, optName)
			}
		}
	}
}

// TestXMLOptions pins each option against the defaults on the same source:
// the default column shows what the option changes.
func TestXMLOptions(t *testing.T) {
	with := func(edit func(*XMLOptions)) *XMLOptions {
		o := DefaultXMLOptions()
		edit(&o)
		return &o
	}

	cases := []struct {
		name         string
		src          string
		opts         *XMLOptions
		defaultTexts []string
		wantTexts    []string
		wantRich     []string
		translations map[string]string
		wantExport   string
	}{
		{
			name:         "skipped element is left out and kept on export",
			src:          `<doc><p>Hello there.</p><codeblock><line>x = 1</line></codeblock></doc>`,
			opts:         with(func(o *XMLOptions) { o.SkipElements = []string{"codeblock"} }),
			defaultTexts: []string{"Hello there.", "x = 1"},
			wantTexts:    []string{"Hello there."},
			wantRich:     []string{""},
			translations: map[string]string{"/doc/p": "Hallo."},
			wantExport:   `<doc><p>Hallo.</p><codeblock><line>x = 1</line></codeblock></doc>`,
		},
		{
			name:         "custom inline element is a chip, not a split",
			src:          `<doc><p><term>Pump</term> <term>speed</term></p></doc>`,
			opts:         with(func(o *XMLOptions) { o.InlineElements = append(o.InlineElements, "term") }),
			defaultTexts: []string{"Pump", "speed"},
			wantTexts:    []string{"Pump speed"},
			wantRich:     []string{`<g id="1" ctype="x-xml-term">Pump</g> <g id="2" ctype="x-xml-term">speed</g>`},
			translations: map[string]string{"/doc/p": `<g id="2">Drehzahl</g> der <g id="1">Pumpe</g>`},
			wantExport:   `<doc><p><term>Drehzahl</term> der <term>Pumpe</term></p></doc>`,
		},
		{
			name:         "custom reference attribute is a placeholder",
			src:          `<doc><p>See <ph conref="lib.dita#t/name">Product</ph> for details.</p></doc>`,
			opts:         with(func(o *XMLOptions) { o.RefAttributes = append(o.RefAttributes, "conref") }),
			defaultTexts: []string{"See Product for details."},
			wantTexts:    []string{"See  for details."},
			wantRich:     []string{`See <x id="1" ctype="x-xml-ph" original_markup="&lt;ph conref=&quot;lib.dita#t/name&quot;&gt;"/> for details.`},
			translations: map[string]string{"/doc/p": `Siehe <x id="1"/> für Details.`},
			wantExport:   `<doc><p>Siehe <ph conref="lib.dita#t/name">Product</ph> für Details.</p></doc>`,
		},
		{
			name: "custom no-translate marker skips the subtree",
			src:  `<doc><p>Open text.</p><section status="locked"><p>Locked text.</p></section></doc>`,
			opts: with(func(o *XMLOptions) {
				o.NoTranslate = append(o.NoTranslate, XMLAttrValue{Name: "status", Value: "locked"})
			}),
			defaultTexts: []string{"Open text.", "Locked text."},
			wantTexts:    []string{"Open text."},
			wantRich:     []string{""},
			translations: map[string]string{"/doc/p": "Offener Text."},
			wantExport:   `<doc><p>Offener Text.</p><section status="locked"><p>Locked text.</p></section></doc>`,
		},
		{
			name:         "unnamed no-translate marker matches nothing",
			src:          `<doc><p>Open text.</p></doc>`,
			opts:         &XMLOptions{NoTranslate: []XMLAttrValue{{}}},
			defaultTexts: []string{"Open text."},
			wantTexts:    []string{"Open text."},
			wantRich:     []string{""},
			translations: map[string]string{"/doc/p": "Offener Text."},
			wantExport:   `<doc><p>Offener Text.</p></doc>`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)

			defSegs, err := XML{}.Parse(src)
			if err != nil {
				t.Fatalf("parse with defaults: %v", err)
			}
			if got := xmlSegmentTexts(defSegs); !reflect.DeepEqual(got, tc.defaultTexts) {
				t.Errorf("default texts = %q, want %q", got, tc.defaultTexts)
			}

			p := XML{Options: tc.opts}
			segments, err := p.Parse(src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := xmlSegmentTexts(segments); !reflect.DeepEqual(got, tc.wantTexts) {
				t.Fatalf("texts = %q, want %q", got, tc.wantTexts)
			}
			for i, s := range segments {
				if s.RichText != tc.wantRich[i] {
					t.Errorf("segment %d rich = %q, want %q", i, s.RichText, tc.wantRich[i])
				}
			}

			out, err := p.Reconstruct(src, tc.translations)
			if err != nil {
				t.Fatalf("reconstruct: %v", err)
			}
			if string(out) != tc.wantExport {
				t.Errorf("export =\n%s\nwant\n%s", out, tc.wantExport)
			}
		})
	}
}
