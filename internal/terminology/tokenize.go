package terminology

import (
	"strings"
	"unicode"
)

// TokenizeSourceText is the exported entry point to the term-lookup tokenizer.
// The MCP terminology tool needs the exact same tokenization the CAT
// editor's sidebar uses — reimplementing it would silently diverge and make the
// two surfaces disagree about which terms apply to a segment.
func TokenizeSourceText(text string, maxN int) []string { return tokenizeSourceText(text, maxN) }

// isTokenApostrophe reports whether r is a word-gluing apostrophe (straight ',
// right single quote ’, modifier letter apostrophe ʼ). Keep in sync with the Go
// isApostrophe in internal/qa/checker.go and the TS APOSTROPHE regex.
func isTokenApostrophe(r rune) bool { return r == '\'' || r == '’' || r == 'ʼ' }

// termPunct are the characters a term may hold inside or at its end: R&D,
// Node.js, TCP/IP, C++, C#, Inc., @mention.
const termPunct = "&./+#@"

// tokenizeSourceText splits source text into lowercase word n-grams (1..maxN words).
// Used for fast indexed term lookup: instead of ILIKE scanning all terms,
// we tokenize the source text and match against lower(source_term) = ANY(tokens).
//
// Example: "Reset your password." with maxN=3 produces:
//
//	["reset", "your", "password", "reset your", "your password", "reset your password"]
//
// plus the same n-grams over two more word lists, so a term with punctuation
// matches too: one keeping the termPunct characters ("r&d", "password."), and
// that one with a sentence-final "." dropped ("node.js" from "Node.js.").
func tokenizeSourceText(text string, maxN int) []string {
	lower := []rune(strings.ToLower(text))
	plain := tokenWords(lower, "")
	withPunct := tokenWords(lower, termPunct)
	noFinalDot := make([]string, 0, len(withPunct))
	for _, w := range withPunct {
		if t := strings.TrimRight(w, "."); t != "" {
			noFinalDot = append(noFinalDot, t)
		}
	}

	seen := make(map[string]struct{})
	tokens := make([]string, 0, len(plain)*2)
	for _, words := range [][]string{plain, withPunct, noFinalDot} {
		for n := 1; n <= maxN && n <= len(words); n++ {
			for i := 0; i <= len(words)-n; i++ {
				ngram := strings.Join(words[i:i+n], " ")
				if _, ok := seen[ngram]; !ok {
					seen[ngram] = struct{}{}
					tokens = append(tokens, ngram)
				}
			}
		}
	}
	return tokens
}

// tokenWords splits lowercased text into words. Punctuation and symbols act as
// word boundaries (covers CJK full-width punctuation like ，。：（） and Latin
// punctuation), EXCEPT: '-'/'_' (kept inside words), the characters in keep,
// and an apostrophe sitting BETWEEN two word chars — a possessive/contraction
// like "company's"/"don't" stays one token so the tokenizer never emits
// spurious bare "s"/"t" fragments that would match single-letter terms.
// Mirrors the word-boundary rule in internal/qa/checker.go (isWordBoundary)
// and web/src/lib/term-match.ts.
func tokenWords(lower []rune, keep string) []string {
	rs := make([]rune, len(lower))
	copy(rs, lower)
	for i, r := range rs {
		switch {
		case r == '-' || r == '_' || strings.ContainsRune(keep, r):
			// kept inside words
		case isTokenApostrophe(r) &&
			i > 0 && i+1 < len(rs) &&
			(unicode.IsLetter(rs[i-1]) || unicode.IsDigit(rs[i-1])) &&
			(unicode.IsLetter(rs[i+1]) || unicode.IsDigit(rs[i+1])):
			// preserve intra-word apostrophe
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			rs[i] = ' '
		}
	}
	return strings.Fields(string(rs))
}
