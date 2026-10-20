package pipeline

import (
	"math"
	"regexp"
	"strings"
	"unicode"
)

// TransferTags re-inserts XLIFF inline tags from source rich text into MT plain text output.
//
// MT engines only accept plain text — tags are stripped before translation.
// This function maps the source tag structure onto the MT result so translators
// see tags in both source and target from the start.
//
// Algorithm (detag-and-project with whitespace word alignment, the industry
// baseline):
//  1. Parse sourceRichText into runs (tagged + plain) — see parseRuns.
//  2. For each tagged run, anchor it in mtPlainText, trying in order:
//     a. VERBATIM match of the wrapped content in the un-emitted tail —
//     passthrough tokens (brand names, URLs, code, numbers, mock/dev-engine
//     output) align character-exact. Strictly best when it hits.
//     b. WORD-INDEX projection — map the source WORD index(es) the run covers
//     to the same word index(es) in the MT output and anchor at those word
//     boundaries. When source/MT word counts diverge beyond a ratio band,
//     scale the index proportionally (still landing on a word boundary,
//     never mid-word), so the open tag stays on the wrapped word even when
//     that word is translated (`bold`→`negrita`).
//     c. CHARACTER-PROPORTIONAL fallback — only when neither side has word
//     structure (no whitespace, e.g. CJK) or the run is all whitespace.
//     Last resort; can drift, but only here.
//  3. Emit the mt slice between the previous emit position and the new anchor
//     as plain text, then wrap with the run's open/close tags.
//
// Self-closing/empty wraps (`<x id="1"/>`) anchor proportionally with zero
// width — tag emitted inline, no MT slice consumed.
//
// Returns "" if sourceRichText is empty or contains no tags.
func TransferTags(sourceRichText, sourcePlainText, mtPlainText string) string {
	if sourceRichText == "" || sourcePlainText == "" || mtPlainText == "" {
		return ""
	}

	runs := parseRuns(sourceRichText)
	if len(runs) == 0 || !hasTaggedRuns(runs) {
		return ""
	}

	srcRunes := []rune(sourcePlainText)
	sourceLen := len(srcRunes)
	mtRunes := []rune(mtPlainText)
	mtLen := len(mtRunes)
	if sourceLen == 0 || mtLen == 0 {
		return ""
	}

	// Word spans for the word-index projection path. Computed once;
	// empty for whitespace-free scripts (CJK), in which case anchorTaggedRun
	// reports !ok and the caller uses the character-proportional fallback.
	srcSpans := wordSpans(srcRunes)
	mtSpans := wordSpans(mtRunes)

	var result strings.Builder
	sourceOffset := 0 // running rune offset into sourcePlainText
	pos := 0          // rune offset in mtRunes already emitted (monotonic)

	for _, run := range runs {
		runPlainRunes := []rune(run.plainText)
		runLen := len(runPlainRunes)

		if run.openTag == "" {
			sourceOffset += runLen
			continue
		}

		if runLen == 0 {
			// Self-closing / empty wrap — proportional, zero width.
			mtAt := sourceOffset * mtLen / sourceLen
			if mtAt < pos {
				mtAt = pos
			}
			if mtAt > mtLen {
				mtAt = mtLen
			}
			if mtAt > pos {
				result.WriteString(string(mtRunes[pos:mtAt]))
			}
			result.WriteString(run.openTag)
			if run.closeTag != "" {
				result.WriteString(run.closeTag)
			}
			pos = mtAt
			continue
		}

		var mtStart, mtEnd int
		if idx := indexRunes(mtRunes[pos:], runPlainRunes); idx >= 0 {
			// (a) Verbatim — wrapped content survived translation unchanged.
			mtStart = pos + idx
			mtEnd = mtStart + runLen
		} else if ws, we, ok := anchorTaggedRun(srcRunes, srcSpans, mtSpans, sourceOffset, runLen, pos); ok {
			// (b) Word-index projection — anchor at MT word boundaries.
			mtStart, mtEnd = ws, we
		} else {
			// (c) Character-proportional fallback — no word structure on one
			// side (e.g. CJK) or an all-whitespace run. Legacy path; can drift.
			mtStart = sourceOffset * mtLen / sourceLen
			mtEnd = (sourceOffset + runLen) * mtLen / sourceLen
			if mtStart < pos {
				mtStart = pos
			}
			if mtEnd > mtLen {
				mtEnd = mtLen
			}
			if mtEnd < mtStart {
				mtEnd = mtStart
			}
		}

		if mtStart > pos {
			result.WriteString(string(mtRunes[pos:mtStart]))
		}
		result.WriteString(run.openTag)
		if mtEnd > mtStart {
			result.WriteString(string(mtRunes[mtStart:mtEnd]))
		}
		result.WriteString(run.closeTag)
		pos = mtEnd
		sourceOffset += runLen
	}

	if pos < mtLen {
		result.WriteString(string(mtRunes[pos:]))
	}
	return result.String()
}

// Rich text carries XLIFF inline tags (chips) and the formatting tags the
// editor writes, always lowercase. Every other character is text, so a `<` or
// `>` that is not such a tag stays. A paired bpt, ept or it wraps native
// markup (`&lt;b&gt;`) that is not text either: the whole element goes. The
// opener must not be self-closed (`<it id="1"/>`), or the match would run on
// to a later element's closing tag and take the text between.
var (
	inlineNativeCodeRE = regexp.MustCompile(`<bpt(?:\s[^<>]*[^/<>])?>[\s\S]*?</bpt>|<ept(?:\s[^<>]*[^/<>])?>[\s\S]*?</ept>|<it(?:\s[^<>]*[^/<>])?>[\s\S]*?</it>`)
	inlineMarkupRE     = regexp.MustCompile(`</?(?:g|x|bpt|ept|ph|it|mrk|hi|sub|b|i|u|em|strong|sup|span|a|br)(?:\s[^<>]*)?/?>`)
)

// stripInlineTags removes the inline markup from rich text, leaving only the
// visible text: the native code inside bpt/ept/it goes with the element, as
// the XLIFF parser's plain text leaves it out. Used to derive the plain
// `active_text` from an engine's tag-preserving (Tier 2) output, where the
// engine returns rich text with the tags repositioned.
func stripInlineTags(s string) string {
	if !strings.Contains(s, "<") {
		return s
	}
	return inlineMarkupRE.ReplaceAllString(inlineNativeCodeRE.ReplaceAllString(s, ""), "")
}

// indexRunes is the rune-level analogue of strings.Index — strings.Index
// returns a byte offset, which would corrupt the rune-indexed slicing the
// caller uses for multi-byte-safe output. Returns -1 if not found.
func indexRunes(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	last := len(haystack) - len(needle)
	for i := 0; i <= last; i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// wordSpans returns the [start,end) rune offsets of each maximal run of
// non-whitespace characters in runes. Empty for whitespace-free input (CJK),
// which signals the caller to skip word-index projection.
func wordSpans(runes []rune) [][2]int {
	var spans [][2]int
	i, n := 0, len(runes)
	for i < n {
		for i < n && unicode.IsSpace(runes[i]) {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n && !unicode.IsSpace(runes[i]) {
			i++
		}
		spans = append(spans, [2]int{start, i})
	}
	return spans
}

// wordIndexContaining returns the index of the word span whose [start,end)
// range contains rune offset off, or -1 if off lands in whitespace / out of
// range.
func wordIndexContaining(spans [][2]int, off int) int {
	for idx, s := range spans {
		if off >= s[0] && off < s[1] {
			return idx
		}
	}
	return -1
}

// anchorTaggedRun maps a tagged source run — covering rune range
// [runStart, runStart+runLen) in the source plain text — to a [mtStart, mtEnd)
// rune range in the MT output, by WORD index.
//
// It finds which source words the run's first and last non-whitespace
// characters belong to, then anchors the tag at the same word index in the MT
// output. When source/MT word counts are within a ratio band (0.66..1.5) the
// mapping is 1:1; otherwise the index is scaled proportionally. Either way the
// result lands on MT word boundaries, never mid-word.
//
// Returns ok=false when there is no usable word structure (either side has no
// words, e.g. CJK, or the run is all whitespace) so the caller can fall back
// to character-proportional placement.
func anchorTaggedRun(srcRunes []rune, srcSpans, mtSpans [][2]int, runStart, runLen, pos int) (mtStart, mtEnd int, ok bool) {
	srcWC, mtWC := len(srcSpans), len(mtSpans)
	if srcWC == 0 || mtWC == 0 {
		return 0, 0, false
	}

	// First / last non-whitespace rune offsets within the run.
	sFirst, sLast := -1, -1
	end := runStart + runLen
	if end > len(srcRunes) {
		end = len(srcRunes)
	}
	for i := runStart; i < end; i++ {
		if !unicode.IsSpace(srcRunes[i]) {
			if sFirst < 0 {
				sFirst = i
			}
			sLast = i
		}
	}
	if sFirst < 0 || sLast < 0 {
		return 0, 0, false // run is all whitespace
	}

	wStart := wordIndexContaining(srcSpans, sFirst)
	wEnd := wordIndexContaining(srcSpans, sLast)
	if wStart < 0 || wEnd < 0 {
		return 0, 0, false
	}

	mStart, mEnd := wStart, wEnd
	if r := float64(mtWC) / float64(srcWC); r < 0.66 || r > 1.5 {
		// Word counts diverge — scale the index proportionally. Still snaps to
		// a word boundary below, so the worst case is "tag at a space," never
		// mid-word.
		mStart = int(math.Round(float64(wStart) * r))
		mEnd = int(math.Round(float64(wEnd) * r))
	}
	mStart = clampInt(mStart, 0, mtWC-1)
	mEnd = clampInt(mEnd, mStart, mtWC-1)

	mtStart = mtSpans[mStart][0]
	mtEnd = mtSpans[mEnd][1]
	if mtStart < pos {
		mtStart = pos
	}
	if mtEnd < mtStart {
		mtEnd = mtStart
	}
	return mtStart, mtEnd, true
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// run represents a segment of rich text — either plain text or a tagged region.
type run struct {
	openTag   string // e.g., `<g id="1" ctype="bold">` or `<bpt id="1"><b></bpt>`
	closeTag  string // e.g., `</g>` or `<ept id="1"></b></ept>`
	plainText string // the visible text inside/outside tags
}

// gTagRe matches opening <g ...> tags.
var gTagOpenRe = regexp.MustCompile(`<g\s[^>]*>`)

// Tag patterns for tokenizing rich text.
var (
	// Opening tags: <g ...>, <bpt ...>...</bpt>
	gOpenRe  = regexp.MustCompile(`<g\s[^>]*>`)
	gCloseRe = regexp.MustCompile(`</g>`)
	// bpt/ept pairs
	bptRe = regexp.MustCompile(`<bpt[^>]*>.*?</bpt>`)
	eptRe = regexp.MustCompile(`<ept[^>]*>.*?</ept>`)
	// Self-closing: <x .../>, <ph .../>, <it .../> — slash REQUIRED.
	//
	// The slash is required so a paired opener like `<ph id="4">` falls
	// through to the paired matcher instead of being taken as a complete
	// self-closing tag and leaking into the MT target literally.
	selfCloseRe = regexp.MustCompile(`<(x|ph|it)\s[^>]*\/>`)

	// Paired ph/it: `<ph id="N">…</ph>`, `<it id="N">…</it>`. These wrap a
	// "placeholder display text" payload that is part of the visible plain
	// text. parseRuns extracts the inner content as the run's plainText (same
	// shape as a `<g>` wrap) so TransferTags can anchor it against the MT
	// output instead of emitting the openTag string with the inner text baked
	// in (which double-counted the payload / journey j12 C2).
	pairedPhItOpenRe  = regexp.MustCompile(`<(ph|it)\s[^>]*>`)
	pairedPhItCloseRe = regexp.MustCompile(`</(ph|it)>`)
)

// parseRuns splits rich text into a sequence of runs.
// Each run is either plain text or a tagged region with open/close tags + inner plain text.
func parseRuns(richText string) []run {
	var runs []run
	// Use a simple state machine: track open <g> tags with a stack.
	i := 0
	runes := []byte(richText)
	n := len(runes)

	for i < n {
		startI := i

		// Try to match a <g ...> opening tag
		if loc := gOpenRe.FindIndex(runes[i:]); loc != nil && loc[0] == 0 {
			openTag := string(runes[i : i+loc[1]])
			i += loc[1]

			// Find the matching </g> — handle nesting by counting depth.
			depth := 1
			innerStart := i
			for i < n && depth > 0 {
				if loc2 := gOpenRe.FindIndex(runes[i:]); loc2 != nil && loc2[0] == 0 {
					depth++
					i += loc2[1]
				} else if loc2 := gCloseRe.FindIndex(runes[i:]); loc2 != nil && loc2[0] == 0 {
					depth--
					if depth == 0 {
						innerRich := string(runes[innerStart:i])
						i += loc2[1] // skip </g>

						// Recursively parse inner content to collect:
						// 1. All nested opening tags as prefix
						// 2. Plain text
						// 3. All nested closing tags as suffix
						innerRuns := parseRuns(innerRich)
						var plainBuf, prefixBuf, suffixBuf strings.Builder
						for _, ir := range innerRuns {
							if ir.plainText == "" && ir.openTag != "" {
								// Self-closing or empty tag — add to prefix
								prefixBuf.WriteString(ir.openTag)
								if ir.closeTag != "" {
									suffixBuf.WriteString(ir.closeTag)
								}
							} else if ir.openTag != "" {
								prefixBuf.WriteString(ir.openTag)
								plainBuf.WriteString(ir.plainText)
								suffixBuf.WriteString(ir.closeTag)
							} else {
								plainBuf.WriteString(ir.plainText)
							}
						}

						runs = append(runs, run{
							openTag:   openTag + prefixBuf.String(),
							closeTag:  suffixBuf.String() + "</g>",
							plainText: plainBuf.String(),
						})
						break
					} else {
						i += loc2[1]
					}
				} else {
					i++
				}
			}
			continue
		}

		// Try to match a <bpt ...>...</bpt> opener. Scan forward depth-aware so
		// nested bpt/ept pair correctly: pairing the outer bpt with the *first*
		// ept seen mispairs nested tags and leaves a stray ept behind.
		if loc := bptRe.FindIndex(runes[i:]); loc != nil && loc[0] == 0 {
			bptTag := string(runes[i : i+loc[1]])
			i += loc[1]

			depth := 1
			innerStart := i
			var eptTag string
			for i < n && depth > 0 {
				if loc2 := bptRe.FindIndex(runes[i:]); loc2 != nil && loc2[0] == 0 {
					depth++
					i += loc2[1]
				} else if loc2 := eptRe.FindIndex(runes[i:]); loc2 != nil && loc2[0] == 0 {
					depth--
					if depth == 0 {
						eptTag = string(runes[i : i+loc2[1]])
						break
					}
					i += loc2[1]
				} else {
					i++
				}
			}

			innerRich := string(runes[innerStart:i])
			if eptTag != "" {
				i += len(eptTag) // consume the matching ept
			}

			// Recursively flatten the inner content: nested tags become a
			// prefix/suffix wrapping the same plain text, mirroring the <g>
			// branch above.
			innerRuns := parseRuns(innerRich)
			var plainBuf, prefixBuf, suffixBuf strings.Builder
			for _, ir := range innerRuns {
				if ir.plainText == "" && ir.openTag != "" {
					prefixBuf.WriteString(ir.openTag)
					if ir.closeTag != "" {
						suffixBuf.WriteString(ir.closeTag)
					}
				} else if ir.openTag != "" {
					prefixBuf.WriteString(ir.openTag)
					plainBuf.WriteString(ir.plainText)
					suffixBuf.WriteString(ir.closeTag)
				} else {
					plainBuf.WriteString(ir.plainText)
				}
			}

			runs = append(runs, run{
				openTag:   bptTag + prefixBuf.String(),
				closeTag:  suffixBuf.String() + eptTag,
				plainText: plainBuf.String(),
			})
			continue
		}

		// Stray <ept> with no opening <bpt>: treat as opaque markup so the
		// outer loop advances. Without this branch the plain-text collector
		// breaks on the `<` and no branch consumes it.
		if loc := eptRe.FindIndex(runes[i:]); loc != nil && loc[0] == 0 {
			runs = append(runs, run{openTag: string(runes[i : i+loc[1]])})
			i += loc[1]
			continue
		}

		// Try to match a paired <ph>…</ph> or <it>…</it>. We must check this
		// BEFORE selfCloseRe so the inner placeholder text is captured as
		// plainText rather than disappearing inside the openTag.
		// Depth tracking handles nested ph/it gracefully.
		if loc := pairedPhItOpenRe.FindIndex(runes[i:]); loc != nil && loc[0] == 0 {
			openTag := string(runes[i : i+loc[1]])
			i += loc[1]
			depth := 1
			innerStart := i
			var closeTag string
			for i < n && depth > 0 {
				if loc2 := pairedPhItOpenRe.FindIndex(runes[i:]); loc2 != nil && loc2[0] == 0 {
					depth++
					i += loc2[1]
				} else if loc2 := pairedPhItCloseRe.FindIndex(runes[i:]); loc2 != nil && loc2[0] == 0 {
					depth--
					if depth == 0 {
						closeTag = string(runes[i : i+loc2[1]])
						i += loc2[1]
						break
					}
					i += loc2[1]
				} else {
					i++
				}
			}
			innerRich := string(runes[innerStart : i-len(closeTag)])
			innerRuns := parseRuns(innerRich)
			var plainBuf, prefixBuf, suffixBuf strings.Builder
			for _, ir := range innerRuns {
				if ir.plainText == "" && ir.openTag != "" {
					prefixBuf.WriteString(ir.openTag)
					if ir.closeTag != "" {
						suffixBuf.WriteString(ir.closeTag)
					}
				} else if ir.openTag != "" {
					prefixBuf.WriteString(ir.openTag)
					plainBuf.WriteString(ir.plainText)
					suffixBuf.WriteString(ir.closeTag)
				} else {
					plainBuf.WriteString(ir.plainText)
				}
			}
			runs = append(runs, run{
				openTag:   openTag + prefixBuf.String(),
				closeTag:  suffixBuf.String() + closeTag,
				plainText: plainBuf.String(),
			})
			continue
		}

		// Self-closing tag: <x .../>, <ph .../>, <it .../> with required slash.
		if loc := selfCloseRe.FindIndex(runes[i:]); loc != nil && loc[0] == 0 {
			tag := string(runes[i : i+loc[1]])
			i += loc[1]
			runs = append(runs, run{
				openTag:   tag,
				closeTag:  "",
				plainText: "",
			})
			continue
		}

		// Plain text — collect until next tag
		start := i
		for i < n {
			if runes[i] == '<' {
				// Check if this is a tag we recognize
				rest := runes[i:]
				if gOpenRe.FindIndex(rest) != nil && gOpenRe.FindIndex(rest)[0] == 0 {
					break
				}
				if bptRe.FindIndex(rest) != nil && bptRe.FindIndex(rest)[0] == 0 {
					break
				}
				if pairedPhItOpenRe.FindIndex(rest) != nil && pairedPhItOpenRe.FindIndex(rest)[0] == 0 {
					break
				}
				if selfCloseRe.FindIndex(rest) != nil && selfCloseRe.FindIndex(rest)[0] == 0 {
					break
				}
				if gCloseRe.FindIndex(rest) != nil && gCloseRe.FindIndex(rest)[0] == 0 {
					break
				}
				if eptRe.FindIndex(rest) != nil && eptRe.FindIndex(rest)[0] == 0 {
					break
				}
			}
			i++
		}
		if i > start {
			runs = append(runs, run{plainText: string(runes[start:i])})
		}

		// Belt-and-braces: every iteration of the outer loop must consume at
		// least one byte. A regex hole that leaves i unchanged would otherwise
		// infinite-loop — force progress so the worst case is
		// "tagging glitch" not "wedged worker".
		if i == startI {
			i++
		}
	}

	return runs
}

func hasTaggedRuns(runs []run) bool {
	for _, r := range runs {
		if r.openTag != "" {
			return true
		}
	}
	return false
}
