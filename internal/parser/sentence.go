package parser

import "openloc/internal/segmentation"

// TextRange is a half-open byte range [Start, End) within a string.
type TextRange = segmentation.TextRange

// segmenterOr returns seg, or the conservative built-in set when seg is nil: a
// parser built without options, and every file stored without a
// segmentation snapshot, splits with it.
func segmenterOr(seg *segmentation.Segmenter) *segmentation.Segmenter {
	if seg == nil {
		return segmentation.Conservative()
	}
	return seg
}

// breakVeto returns a canBreak callback for Segmenter.Split that allows a
// break only where the character it follows is breakable: breakable[i]
// reports whether text byte i may end a sentence.
func breakVeto(breakable []bool) func(int) bool {
	return func(pos int) bool {
		return pos > 0 && pos <= len(breakable) && breakable[pos-1]
	}
}
