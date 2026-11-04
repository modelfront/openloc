package parser

import (
	"reflect"
	"testing"
)

// FileOptions decodes a stored file's filter profile snapshot into the HTML
// and XML options its re-parses use; no snapshot is the defaults.
func TestFileOptionsReadsFilterSnapshot(t *testing.T) {
	snap := []byte(`{"id":7,"name":"Buttons","format":"html","builtin":false,"options":{"html":{"attributes":{"button":["aria-label"]},"meta_description":false,"skip_pre":true}}}`)
	o, err := FileOptions(true, nil, nil, snap)
	if err != nil {
		t.Fatal(err)
	}
	want := &HTMLOptions{Attributes: map[string][]string{"button": {"aria-label"}}, SkipPre: true}
	if !reflect.DeepEqual(o.HTML, want) || o.XML != nil {
		t.Errorf("FileOptions = html %+v, xml %+v; want html %+v", o.HTML, o.XML, want)
	}

	xmlSnap := []byte(`{"options":{"xml":{"inline_elements":["term"],"no_translate":[],"ref_elements":[],"ref_attributes":[],"skip_elements":["code"]}}}`)
	if o, err = FileOptions(true, nil, nil, xmlSnap); err != nil || o.XML == nil || !reflect.DeepEqual(o.XML.SkipElements, []string{"code"}) {
		t.Errorf("xml snapshot: %+v, %v", o.XML, err)
	}

	for _, empty := range [][]byte{nil, []byte("null")} {
		if o, err := FileOptions(true, nil, nil, empty); err != nil || o.HTML != nil || o.XML != nil {
			t.Errorf("no snapshot (%q): html %v xml %v err %v, want the defaults", empty, o.HTML, o.XML, err)
		}
	}
}

// The parser GetParserWith returns for an HTML file uses the file's options:
// a custom attribute becomes a segment.
func TestGetParserWithHTMLOptions(t *testing.T) {
	src := []byte(`<html><body><button aria-label="Close the dialog">X</button></body></html>`)
	p, err := GetParserWith("html", ParseOptions{InlineHTML: true, HTML: &HTMLOptions{Attributes: map[string][]string{"button": {"aria-label"}}}})
	if err != nil {
		t.Fatal(err)
	}
	segs, err := p.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range segs {
		found = found || s.Text == "Close the dialog"
	}
	if !found {
		t.Errorf("segments %+v hold no aria-label text", segs)
	}
	def, err := GetParserWith("html", ParseOptions{InlineHTML: true})
	if err != nil {
		t.Fatal(err)
	}
	segs, _ = def.Parse(src)
	for _, s := range segs {
		if s.Text == "Close the dialog" {
			t.Error("the default options translate aria-label")
		}
	}
}
