package xml

import (
	"openloc/internal/parsers/common"
	"strings"
	"testing"
)

// The start-tag scan measures whole tags: a '>' inside a quoted value does
// not end one, and comments, CDATA and processing instructions are not tags.
func TestStartTagsWithin(t *testing.T) {
	long := strings.Repeat("x", 100)
	cases := []struct {
		name string
		src  string
		max  int
		want bool
	}{
		{"short tags", `<r><a b="1"/></r>`, 20, true},
		{"'>' in a quoted value keeps the tag going", `<a b="x>yyyyyyyyyy"/>`, 15, false},
		{"single quotes too", `<a b='x>yyyyyyyyyy'/>`, 15, false},
		{"the same tag fits a larger max", `<a b="x>yyyyyyyyyy"/>`, 30, true},
		{"long comment", `<!-- <` + long + `> --><r/>`, 20, true},
		{"long CDATA", `<r><![CDATA[<` + long + `]]></r>`, 20, true},
		{"long processing instruction", `<?xml version="1.0" ` + long + `?><r/>`, 20, true},
		{"many attributes", `<a ` + strings.Repeat(`a="" `, 30) + `/>`, 64, false},
	}
	for _, tc := range cases {
		if got := common.StartTagsWithin([]byte(tc.src), tc.max); got != tc.want {
			t.Errorf("%s: startTagsWithin = %v, want %v", tc.name, got, tc.want)
		}
	}
}
