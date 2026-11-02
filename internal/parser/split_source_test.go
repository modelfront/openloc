package parser

import (
	"errors"
	"testing"
)

func TestSplitSourceAt_CutsAndJoinsBack(t *testing.T) {
	cases := []struct {
		name, text, rich string
		off              int
		want             [2]Piece
	}{
		{
			name: "plain, at the end of a sentence",
			text: "This is an example. This is a second example.", off: 19,
			want: [2]Piece{{Text: "This is an example."}, {Text: "This is a second example.", Gap: " "}},
		},
		{
			name: "plain, after the space",
			text: "This is an example. This is a second example.", off: 20,
			want: [2]Piece{{Text: "This is an example."}, {Text: "This is a second example.", Gap: " "}},
		},
		{
			name: "plain, inside a word",
			text: "This is an example.", off: 13,
			want: [2]Piece{{Text: "This is an ex"}, {Text: "ample."}},
		},
		{
			name: "rich, the second piece's chips renumbered",
			text: "Click here now. And there.",
			rich: `Click <g id="1">here</g> now. And <g id="2">there</g>.`, off: 15,
			want: [2]Piece{
				{Text: "Click here now.", Rich: `Click <g id="1">here</g> now.`},
				{Text: "And there.", Rich: `And <g id="1">there</g>.`, ChipBase: 1, Gap: " "},
			},
		},
		{
			name: "rich, a line break between the pieces",
			text: "Line one\nLine two",
			rich: `Line one<x id="1" ctype="lb"/>Line two`, off: 8,
			want: [2]Piece{{Text: "Line one"}, {Text: "Line two", Gap: `<x id="1" ctype="lb"/>`}},
		},
		{
			name: "rich, right before a paired tag",
			text: "Helloworld",
			rich: `Hello<g id="1">world</g>`, off: 5,
			want: [2]Piece{{Text: "Hello"}, {Text: "world", Rich: `<g id="1">world</g>`}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SplitSourceAt(c.text, c.rich, c.off)
			if err != nil {
				t.Fatalf("SplitSourceAt: %v", err)
			}
			if got != c.want {
				t.Fatalf("pieces = %+v\nwant     %+v", got, c.want)
			}
			text, rich := JoinPieces(got[:])
			if text != c.text {
				t.Errorf("joined text = %q, want %q", text, c.text)
			}
			if c.rich != "" && rich != c.rich {
				t.Errorf("joined rich = %q, want %q", rich, c.rich)
			}
		})
	}
}

func TestSplitSourceAt_Refuses(t *testing.T) {
	cases := []struct {
		name, text, rich string
		off              int
	}{
		{"at the start", "One. Two.", "", 0},
		{"at the end", "One. Two.", "", 9},
		{"past the end", "One. Two.", "", 40},
		{"inside a character", "Café au lait", "", 4},
		{"leaves only whitespace before", "   One.", "", 2},
		{"leaves only whitespace after", "One.   ", "", 5},
		{"inside a paired tag", "A. B", `<g id="1">A. B</g>`, 2},
		{"inside braces", "{n, plural, one {a. b}}", "", 19},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := SplitSourceAt(c.text, c.rich, c.off); !errors.Is(err, ErrSplitPosition) {
				t.Errorf("err = %v, want ErrSplitPosition", err)
			}
		})
	}

	tags := []struct{ name, text, rich string }{
		{"rich that does not line up", "One. Two.", `<g id="1">Other</g>`},
		{"XLIFF codes with escaped markup", "Click here. Then go.", `Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept>. Then go.`},
	}
	for _, c := range tags {
		t.Run(c.name, func(t *testing.T) {
			if _, err := SplitSourceAt(c.text, c.rich, 4); !errors.Is(err, ErrSplitTags) {
				t.Errorf("err = %v, want ErrSplitTags", err)
			}
		})
	}
}

// A translation of two pieces goes back into the numbering of the segment
// they came from, with the source's gap between them.
func TestJoinPieces_ShiftsTranslationChipsBack(t *testing.T) {
	pieces := []Piece{
		{Text: "Klicken Sie hier.", Rich: `Klicken Sie <g id="1">hier</g>.`},
		{Text: "Und dort.", Rich: `Und <g id="1">dort</g>.`, ChipBase: 1, Gap: " "},
	}
	text, rich := JoinPieces(pieces)
	if text != "Klicken Sie hier. Und dort." {
		t.Errorf("text = %q", text)
	}
	if want := `Klicken Sie <g id="1">hier</g>. Und <g id="2">dort</g>.`; rich != want {
		t.Errorf("rich = %q, want %q", rich, want)
	}
	if _, rich := JoinPieces([]Piece{{Text: "A."}, {Text: "B.", Gap: " "}}); rich != "" {
		t.Errorf("pieces with no chips joined to rich %q, want \"\"", rich)
	}
}
