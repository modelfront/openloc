package parser

import "strings"

// sourceOf is what a translator edits: the segment's rich text, or its text
// when it has none.
func sourceOf(s Segment) string {
	if s.RichText != "" {
		return s.RichText
	}
	return s.Text
}

// translateAll maps every segment's key to its source with r applied.
func translateAll(segs []Segment, r *strings.Replacer) map[string]string {
	out := make(map[string]string, len(segs))
	for _, s := range segs {
		out[s.Key] = r.Replace(sourceOf(s))
	}
	return out
}
