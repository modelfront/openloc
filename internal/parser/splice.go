package parser

import (
	"sort"
	"strings"
)

// spliceEdit replaces the bytes of a document in rng with text.
type spliceEdit struct {
	rng  TextRange
	text string
}

// splice applies edits to src in a single forward pass, so the cost stays
// linear in the document size however many edits there are. An edit that
// overlaps the one before it or falls outside src is skipped: a bad range
// would silently corrupt the document.
func splice(src string, edits []spliceEdit) string {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].rng.Start < edits[j].rng.Start })

	var out strings.Builder
	out.Grow(len(src))
	pos := 0
	for _, e := range edits {
		if e.rng.Start < pos || e.rng.Start > e.rng.End || e.rng.End > len(src) {
			continue
		}
		out.WriteString(src[pos:e.rng.Start])
		out.WriteString(e.text)
		pos = e.rng.End
	}
	out.WriteString(src[pos:])
	return out.String()
}
