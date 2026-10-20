package inlinetag

import (
	"strings"
	"unicode/utf8"
)

// ReplaceText replaces every non-overlapping occurrence of find in text and
// returns how many it replaced. Case-insensitive matching compares rune by
// rune (Unicode simple folding) on the text itself, so the replacement lands
// exactly where the match is even when a letter's lower case has a different
// byte length (İ → i̇), and the surrounding text keeps its case.
func ReplaceText(text, find, replace string, caseSensitive bool) (int, string) {
	if find == "" || text == "" {
		return 0, text
	}
	if caseSensitive {
		n := strings.Count(text, find)
		if n == 0 {
			return 0, text
		}
		return n, strings.ReplaceAll(text, find, replace)
	}
	var b strings.Builder
	count, pos := 0, 0
	for i := 0; i < len(text); {
		if end, ok := foldMatchAt(text, i, find); ok {
			b.WriteString(text[pos:i])
			b.WriteString(replace)
			count++
			i, pos = end, end
			continue
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		i += size
	}
	if count == 0 {
		return 0, text
	}
	b.WriteString(text[pos:])
	return count, b.String()
}

// foldMatchAt reports whether find matches text at byte offset i, ignoring
// case, and where the match ends.
func foldMatchAt(text string, i int, find string) (int, bool) {
	j := 0
	for j < len(find) {
		if i >= len(text) {
			return 0, false
		}
		tr, ts := utf8.DecodeRuneInString(text[i:])
		fr, fs := utf8.DecodeRuneInString(find[j:])
		if tr != fr && !strings.EqualFold(string(tr), string(fr)) {
			return 0, false
		}
		i += ts
		j += fs
	}
	return i, true
}

// ReplaceInRich is ReplaceText on the editable text of rich text: only the
// text runs between tags change, so every tag — and a placeholder's own
// content — survives byte for byte. A match that crosses a tag is not
// replaced, so its count can be lower than ReplaceText on the plain form.
func ReplaceInRich(rich, find, replace string, caseSensitive bool) (int, string) {
	var b strings.Builder
	b.Grow(len(rich))
	count := 0
	var run strings.Builder // consecutive text tokens, matched as one string
	flush := func() {
		n, out := ReplaceText(run.String(), find, replace, caseSensitive)
		count += n
		b.WriteString(out)
		run.Reset()
	}
	for _, t := range tokenize(rich) {
		if t.kind == "text" {
			run.WriteString(t.raw)
			continue
		}
		flush()
		b.WriteString(t.raw)
	}
	flush()
	return count, b.String()
}
