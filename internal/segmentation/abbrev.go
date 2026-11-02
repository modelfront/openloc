package segmentation

import (
	"sort"
	"unicode"
	"unicode/utf8"
)

// wordList matches the words of an abbreviation list: a word followed by a
// full stop, at the start of the block or after a character that is not a
// letter. Exact, case-sensitive text.
type wordList struct {
	words map[string]bool
	// lengths are the distinct byte lengths of the words, longest first.
	lengths []int
}

func newWordList(words []string) *wordList {
	if len(words) == 0 {
		return nil
	}
	l := &wordList{words: make(map[string]bool, len(words))}
	seen := map[int]bool{}
	for _, w := range words {
		l.words[w] = true
		if !seen[len(w)] {
			seen[len(w)] = true
			l.lengths = append(l.lengths, len(w))
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(l.lengths)))
	return l
}

// endsAt returns the listed word that ends text[:dot], where text[dot] is the
// full stop, or "" when none does.
func (l *wordList) endsAt(text string, dot int) string {
	if l == nil {
		return ""
	}
	for _, n := range l.lengths {
		start := dot - n
		if start < 0 {
			continue
		}
		w := text[start:dot]
		if !l.words[w] {
			continue
		}
		if start == 0 {
			return w
		}
		if r, _ := utf8.DecodeLastRuneInString(text[:start]); !unicode.IsLetter(r) {
			return w
		}
	}
	return ""
}
