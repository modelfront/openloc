package editor

import (
	"slices"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

func indexOf(sources ...string) Index {
	rows := make([]db.ListEditorIndexRow, len(sources))
	for i, s := range sources {
		rows[i] = db.ListEditorIndexRow{SegmentID: int64(i + 1), Position: int32(i), SourceText: s}
	}
	return buildIndex(rows)
}

func TestBuildIndex_MarksRepetitions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sources []string
		want    []int8
	}{
		{"none", nil, []int8{}},
		{"every source unique", []string{"a", "b", "c"}, []int8{0, 0, 0}},
		{"first occurrence is the source, later ones repeat", []string{"Hello", "World", "Hello", "World", "Hello"}, []int8{1, 1, 2, 2, 2}},
		{"document order decides the source", []string{"x", "y", "x"}, []int8{1, 0, 2}},
		{"case differences are distinct", []string{"Hello", "hello"}, []int8{0, 0}},
		{"whitespace differences are distinct", []string{"Hello", "Hello ", " Hello"}, []int8{0, 0, 0}},
		{"empty matches empty", []string{"", ""}, []int8{1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := indexOf(tc.sources...).Repetition; !slices.Equal(got, tc.want) {
				t.Errorf("repetition = %v, want %v", got, tc.want)
			}
		})
	}
}

// Stats count only the later occurrences: words split on whitespace,
// characters in UTF-16 code units, as the leverage bar counts them.
func TestBuildIndex_RepetitionStats(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sources []string
		want    RepetitionStats
	}{
		{"unique", []string{"a b", "c"}, RepetitionStats{}},
		{"later occurrences only", []string{"Hello world", "Hello world", "Hello world"}, RepetitionStats{Segments: 2, Words: 4, Characters: 22}},
		{"several repeated sources", []string{"one", "two words", "one", "two words"}, RepetitionStats{Segments: 2, Words: 3, Characters: 12}},
		{"empty source", []string{"", ""}, RepetitionStats{Segments: 1}},
		{"whitespace only", []string{"   ", "   "}, RepetitionStats{Segments: 1, Characters: 3}},
		{"runs of whitespace", []string{"a  \t b", "a  \t b"}, RepetitionStats{Segments: 1, Words: 2, Characters: 6}},
		{"characters beyond the BMP count twice", []string{"😀", "😀"}, RepetitionStats{Segments: 1, Words: 1, Characters: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := indexOf(tc.sources...).RepetitionStats; got != tc.want {
				t.Errorf("stats = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRefKeys_DecodesDedupesAndSorts(t *testing.T) {
	got := refKeys([]string{
		`See <ph id="1" ref_key="b&amp;c">x</ph> and <ph id="2" ref_key="a">y</ph>`,
		`Again <ph id="1" ref_key="a">y</ph>`,
		`Text that mentions ref_key="not-a-tag" outside any tag`,
	})
	if want := []string{"a", "b&c"}; !slices.Equal(got, want) {
		t.Errorf("refKeys = %q, want %q", got, want)
	}
}

// The editor offers a join before the next piece of a split, and before the
// next sentence of the same block in a format whose sentences join.
func TestBuildIndex_JoinsNext(t *testing.T) {
	rows := func(format db.FileFormat, keys ...string) []db.ListEditorIndexRow {
		out := make([]db.ListEditorIndexRow, len(keys))
		for i, k := range keys {
			out[i] = db.ListEditorIndexRow{Position: int32(i), UnitKey: k, FileFormat: format}
		}
		return out
	}
	cases := []struct {
		name string
		rows []db.ListEditorIndexRow
		want []bool
	}{
		{"sentences of a line", rows(db.FileFormatTxt, "line:1#1", "line:1#2-3", "line:1#4", "line:2"), []bool{true, true, false, false}},
		{"mrk sentences of a trans-unit", rows(db.FileFormatXliff, "a1:1", "a1:2~3", "a2"), []bool{true, false, false}},
		{"PO plural forms", rows(db.FileFormatPo, "x#0", "x#1"), []bool{false, false}},
		{"TXT lines", rows(db.FileFormatTxt, "line:1", "line:2"), []bool{false, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := buildIndex(c.rows).JoinsNext; !slices.Equal(got, c.want) {
				t.Errorf("joins next = %v, want %v", got, c.want)
			}
		})
	}
	pieces := rows(db.FileFormatPo, "x", "x@9")
	pieces[1].Position = 0
	if got := buildIndex(pieces).JoinsNext; !got[0] {
		t.Errorf("pieces of a split: joins next = %v", got)
	}
}
