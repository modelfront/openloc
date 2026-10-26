package html

import "testing"

func TestStripHTMLFormatting(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"plain text", "plain text"},
		{"This is <strong>bold</strong> text", "This is bold text"},
		{"<em>italic</em> and <strong>bold</strong>", "italic and bold"},
		{"H<sub>2</sub>O", "H2O"},
		{"x<sup>2</sup> + y<sup>2</sup>", "x2 + y2"},
		{"<p>wrapped in p</p>", "wrapped in p"},
		{"<strong><em>bold italic</em></strong>", "bold italic"},
		{"no tags here", "no tags here"},
		{"entities: &amp; &lt; &gt; &quot;", `entities: & < > "`},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := StripHTMLFormatting(tc.input)
			if got != tc.want {
				t.Errorf("StripHTMLFormatting(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestHasHTMLFormatting(t *testing.T) {
	if !HasHTMLFormatting("This is <strong>bold</strong>") {
		t.Error("expected true for <strong>")
	}
	if HasHTMLFormatting("plain text") {
		t.Error("expected false for plain text")
	}
	if !HasHTMLFormatting("H<sub>2</sub>O") {
		t.Error("expected true for <sub>")
	}
}
