package pipeline

import "strings"

// TM-path tag projection.
//
// A TM stores tag STRUCTURE, not tag identity: the entry's own ids came from
// whatever file it was harvested from, and a TMX built by another tool numbers
// its pairs with `i` rather than `id`. Every major CAT tool therefore
// re-anchors a hit's tags onto the segment being translated, taking ids from
// the CURRENT source. Copying the TM's markup through verbatim would put
// foreign ids in the target and make the tag QA check disagree with itself.
//
// Without this, a 100% match on a tagged segment would land with no tags and
// trip the missing-tags bar.

// tmTargetRichText returns the rich-text form to store for a TM match, or ""
// when the segment needs none.
//
// sourceRich is the current segment's tagged source, sourcePlain its plain
// form, targetPlain the TM hit's plain target, and targetRich the hit's stored
// rich target ("" when the entry has none).
func tmTargetRichText(sourceRich, sourcePlain, targetPlain, targetRich string) string {
	// Tag-free source: nothing to project. Leaving this NULL keeps ordinary
	// segments byte-identical to their pre-change behaviour.
	if sourceRich == "" || sourcePlain == "" || targetPlain == "" {
		return ""
	}
	sourceRuns := parseRuns(sourceRich)
	if !hasTaggedRuns(sourceRuns) {
		return ""
	}

	// Best case: the TM entry already knows where the tags belong inside the
	// target — it just spells them differently. Re-label its tags with the
	// source's, which preserves the human-verified placement instead of
	// re-deriving it from text alignment.
	if remapped := remapTagsFromSource(sourceRuns, targetRich); remapped != "" {
		return remapped
	}

	// Otherwise project the source's tags onto the plain target with the same
	// Tier 1 algorithm the MT path uses.
	return TransferTags(sourceRich, sourcePlain, targetPlain)
}

// remapTagsFromSource rewrites the tags in targetRich using the source's tags,
// matched positionally. Returns "" when the two sides don't carry the same
// number of tagged runs, in which case positional matching would be a guess
// and the caller should fall back to text-anchored projection.
func remapTagsFromSource(sourceRuns []run, targetRich string) string {
	if targetRich == "" {
		return ""
	}
	targetRuns := parseRuns(targetRich)

	srcTagged := taggedRuns(sourceRuns)
	tgtTagged := taggedRuns(targetRuns)
	if len(srcTagged) == 0 || len(srcTagged) != len(tgtTagged) {
		return ""
	}

	var b strings.Builder
	next := 0
	for _, r := range targetRuns {
		if r.openTag == "" {
			b.WriteString(r.plainText)
			continue
		}
		src := srcTagged[next]
		next++
		b.WriteString(src.openTag)
		// A placeholder's payload is its DISPLAY form ("<2/>"), not
		// translatable text. Once the tag is replaced that representation no
		// longer applies, and carrying it would write a literal "<2/>" into
		// the exported document.
		if !isPlaceholderRun(r) {
			b.WriteString(r.plainText)
		}
		b.WriteString(src.closeTag)
	}
	return b.String()
}

// isPlaceholderRun reports whether a run is a standalone placeholder (ph / it /
// x) rather than a pair wrapping real text. Trados models an ST4 <meta-ref> as
// a placeholder where our parser makes it a paired wrapper, so the two sides
// disagree on whether the run has content.
func isPlaceholderRun(r run) bool {
	for _, prefix := range []string{"<ph", "<it", "<x"} {
		if strings.HasPrefix(r.openTag, prefix) {
			return true
		}
	}
	return false
}

// taggedRuns returns only the runs that carry a tag, in order.
func taggedRuns(runs []run) []run {
	var out []run
	for _, r := range runs {
		if r.openTag != "" {
			out = append(out, r)
		}
	}
	return out
}
