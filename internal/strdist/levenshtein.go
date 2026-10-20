// Package strdist hosts string-distance primitives used by higher-level
// scorers (TM matching, QA edit-distance). Placed below the translation
// and translationmemory handler packages so both can depend on it without
// risking import cycles.
package strdist

// Levenshtein computes the character-level edit distance between two
// strings, operating on runes (so multibyte characters count as one edit).
func Levenshtein(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	la, lb := len(ra), len(rb)

	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	// Single-row DP — only the previous row is read, so no full matrix.
	prev := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr := make([]int, lb+1)
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev = curr
	}
	return prev[lb]
}
