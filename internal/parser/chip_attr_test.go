package parser

import "testing"

// A chip's id and ctype are read by whole attribute name: "id" never matches
// the end of "xid" or "ref_id", whichever attribute comes first.
func TestScanChipTagAttributes(t *testing.T) {
	cases := []struct {
		tag   string
		id    int
		ctype string
	}{
		{`<g xid="5" id="1">`, 1, ""},
		{`<x ref_id="3" id="2" ctype="lb"/>`, 2, "lb"},
		{`<x id="4" ctype="x-xml-fld"/>`, 4, "x-xml-fld"},
		{`<g id="7" original_markup="&lt;b id=&quot;9&quot;&gt;">`, 7, ""},
	}
	for _, tc := range cases {
		tag, _, ok := scanChipTag(tc.tag, 0)
		if !ok || tag.id != tc.id || tag.ctype != tc.ctype {
			t.Errorf("scanChipTag(%s) = id %d ctype %q (ok %v), want id %d ctype %q", tc.tag, tag.id, tag.ctype, ok, tc.id, tc.ctype)
		}
	}
}
