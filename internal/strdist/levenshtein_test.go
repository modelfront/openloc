package strdist

import "testing"

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
		{"book", "back", 2},
		// Unicode — rune-based, not byte-based.
		{"Haus", "Häuser", 3},
		{"日本語", "日本人", 1},
		{"a", "b", 1},
		{"a", "a", 0},
		{"abc", "aXbYcZ", 3},
		{"abcdef", "ace", 3},
	}

	for _, tt := range tests {
		got := Levenshtein(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("Levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
