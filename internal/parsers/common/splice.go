package common

import (
	"sort"
	"strings"
)

// SpliceEdit replaces the bytes of a document in rng with text.
type SpliceEdit struct {
	Rng  TextRange
	Text string
}

// Splice applies edits to src in a single forward pass, so the cost stays
// linear in the document size however many edits there are. An edit that
// overlaps the one before it or falls outside src is skipped: a bad range
// would silently corrupt the document.
func Splice(src string, edits []SpliceEdit) string {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].Rng.Start < edits[j].Rng.Start })

	var out strings.Builder
	out.Grow(len(src))
	pos := 0
	for _, e := range edits {
		if e.Rng.Start < pos || e.Rng.Start > e.Rng.End || e.Rng.End > len(src) {
			continue
		}
		out.WriteString(src[pos:e.Rng.Start])
		out.WriteString(e.Text)
		pos = e.Rng.End
	}
	out.WriteString(src[pos:])
	return out.String()
}
