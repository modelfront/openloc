package qa

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"openloc/internal/logging"
)

// Issue represents a single QA check result.
//
// Ignored is set after the pure checks run, by comparing against the stored
// qa_results row for the same translation + check_type: a stored row that is
// ignored AND carries the same message marks the fresh issue ignored. Ignored
// issues stay in the list (the UI shows them greyed out) but do not count as
// errors for the confirm gate.
type Issue struct {
	CheckType string `json:"check_type"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Ignored   bool   `json:"ignored"`
}

// TagChecksConfig controls which sub-checks are enabled in the tag_checks rule.
type TagChecksConfig struct {
	Missing  bool `json:"missing"`
	Extra    bool `json:"extra"`
	Unclosed bool `json:"unclosed"`
	Unopened bool `json:"unopened"`
}

// DefaultTagChecksConfig returns config with all sub-checks enabled.
func DefaultTagChecksConfig() TagChecksConfig {
	return TagChecksConfig{Missing: true, Extra: true, Unclosed: true, Unopened: true}
}

// CheckAllParams holds all inputs for a QA check run.
type CheckAllParams struct {
	Source         string
	Target         string
	SourceRichText string // XLIFF-tagged source (empty if no tags)
	TargetRichText string // XLIFF-tagged target (empty if no tags)
	Rules          map[string]string
	RuleConfigs    map[string][]byte // check_type → JSON config
	Concepts       []ConceptForQA
}

// CheckAll runs all enabled checks against a source/target pair.
func CheckAll(ctx context.Context, source, target string, rules map[string]string, concepts []ConceptForQA, richText ...string) []Issue {
	p := CheckAllParams{
		Source:   source,
		Target:   target,
		Rules:    rules,
		Concepts: concepts,
	}
	if len(richText) > 0 {
		p.SourceRichText = richText[0]
	}
	if len(richText) > 1 {
		p.TargetRichText = richText[1]
	}
	return CheckAllWithParams(ctx, p)
}

// CheckAllWithParams runs all enabled checks with full parameters.
func CheckAllWithParams(ctx context.Context, p CheckAllParams) []Issue {
	var issues []Issue

	if sev, ok := p.Rules["empty_target"]; ok {
		if issue := CheckEmpty(p.Target, sev); issue != nil {
			issues = append(issues, *issue)
		}
	}

	// Skip remaining checks if target is empty — they'd all be noise
	if strings.TrimSpace(p.Target) == "" {
		return issues
	}

	// For tag checks, prefer rich text (with XLIFF inline tags) over plain text
	tagSource := p.Source
	if p.SourceRichText != "" {
		tagSource = p.SourceRichText
	}
	tagTarget := p.Target
	if p.TargetRichText != "" {
		tagTarget = p.TargetRichText
	}

	if sev, ok := p.Rules["tag_checks"]; ok {
		cfg := DefaultTagChecksConfig()
		if raw, ok := p.RuleConfigs["tag_checks"]; ok && len(raw) > 2 {
			// Resilience: a malformed tag_checks config falls back to defaults
			// rather than blocking the QA run. But log the failure so a tenant
			// admin who configured a bad config can see it server-side.
			if err := json.Unmarshal(raw, &cfg); err != nil {
				logging.Warn(ctx, err, "tag_checks config malformed; using defaults", "qa-tag-checks-config-malformed")
				cfg = DefaultTagChecksConfig()
			}
		}
		issues = append(issues, CheckTagsDetailed(tagSource, tagTarget, sev, cfg)...)
	}

	if sev, ok := p.Rules["number_consistency"]; ok {
		issues = append(issues, CheckNumbers(p.Source, p.Target, sev)...)
	}

	if sev, ok := p.Rules["terminology"]; ok {
		if issue := CheckTerminology(p.Source, p.Target, p.Concepts, sev); issue != nil {
			issues = append(issues, *issue)
		}
	}

	if sev, ok := p.Rules["forbidden_terms"]; ok {
		if issue := CheckForbiddenTerms(p.Target, p.Concepts, sev); issue != nil {
			issues = append(issues, *issue)
		}
	}

	if sev, ok := p.Rules["punctuation"]; ok {
		if issue := CheckPunctuation(p.Source, p.Target, sev); issue != nil {
			issues = append(issues, *issue)
		}
	}

	if sev, ok := p.Rules["target_identical_to_source"]; ok {
		if issue := CheckTargetIdenticalToSource(p.Source, p.Target, sev); issue != nil {
			issues = append(issues, *issue)
		}
	}

	return issues
}

// TermRow is a flat, concept-tagged terminology lookup row (one source term ×
// one target term of the same concept). GroupConcepts collapses these into
// per-concept QA units. Fields line up with the lookup query's output columns.
type TermRow struct {
	ConceptID  int64
	Domain     string
	SourceTerm string
	TargetTerm string
	Status     string // target term's status: approved | forbidden | pending
	// SourceStatus is the SOURCE-language term's own status. "forbidden" means a
	// discouraged input phrasing — inert when that language is the source, so such
	// a source term is NOT enforceable (excluded from SourceTerms in GroupConcepts).
	SourceStatus string
}

// ConceptForQA is one terminology concept resolved for a segment: its source-
// language synonyms plus the target-language synonyms partitioned by status.
// Approved targets are ALTERNATIVES (any satisfies the check); forbidden targets
// each independently fail if present. Pending terms are neither enforced nor
// forbidden (dropped during grouping).
type ConceptForQA struct {
	ConceptID        int64
	Domain           string
	SourceTerms      []string
	ApprovedTargets  []string
	ForbiddenTargets []string
}

// GroupConcepts collapses flat concept-tagged rows into per-concept QA units.
// Distinct source synonyms → SourceTerms; approved targets → ApprovedTargets;
// forbidden → ForbiddenTargets; pending is dropped. First-seen order is
// preserved so violation messages are stable. Pure (no DB dependency) — the TS
// port and the checker both rely on this grouping being deterministic.
func GroupConcepts(rows []TermRow) []ConceptForQA {
	order := make([]int64, 0)
	byID := make(map[int64]*ConceptForQA)
	seen := make(map[int64]map[string]bool) // concept → "field:text" dedup

	for _, r := range rows {
		c, ok := byID[r.ConceptID]
		if !ok {
			c = &ConceptForQA{ConceptID: r.ConceptID, Domain: r.Domain}
			byID[r.ConceptID] = c
			seen[r.ConceptID] = make(map[string]bool)
			order = append(order, r.ConceptID)
		}
		dedup := seen[r.ConceptID]
		// A source term that's forbidden in the source language is a discouraged
		// input phrasing, not something to enforce a target against — skip it.
		// (Its target term below is still collected; the concept stays valid.)
		if r.SourceTerm != "" && r.SourceStatus != "forbidden" && !dedup["s:"+r.SourceTerm] {
			dedup["s:"+r.SourceTerm] = true
			c.SourceTerms = append(c.SourceTerms, r.SourceTerm)
		}
		if r.TargetTerm != "" {
			switch r.Status {
			case "approved":
				if !dedup["a:"+r.TargetTerm] {
					dedup["a:"+r.TargetTerm] = true
					c.ApprovedTargets = append(c.ApprovedTargets, r.TargetTerm)
				}
			case "forbidden":
				if !dedup["f:"+r.TargetTerm] {
					dedup["f:"+r.TargetTerm] = true
					c.ForbiddenTargets = append(c.ForbiddenTargets, r.TargetTerm)
				}
			}
		}
	}

	out := make([]ConceptForQA, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// isApostrophe reports whether r is a word-gluing apostrophe: straight ('),
// right single quote (’), or modifier letter apostrophe (ʼ). Keep in sync with
// the TS APOSTROPHE regex in web/src/lib/term-match.ts.
func isApostrophe(r rune) bool {
	return r == '\'' || r == '’' || r == 'ʼ'
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// isWordBoundary reports whether the [start,end) slice of s sits on word
// boundaries — i.e. the immediately-adjacent rune on each side is the string
// edge or a true separator. A word char is never a boundary; neither is an
// apostrophe that GLUES the match to a word (possessive "company's", contraction
// "don't"), so the tokenizer's bare "s"/"t" fragments don't match single-letter
// or partial terms. Mirrors TS isWholeWordMatch.
func isWordBoundary(s string, start, end int) bool {
	if start > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:start])
		if isWordRune(r) {
			return false
		}
		// A gluing apostrophe (word char on its far side) is not a boundary.
		if isApostrophe(r) {
			if far, _ := utf8.DecodeLastRuneInString(s[:start-size]); isWordRune(far) {
				return false
			}
		}
	}
	if end < len(s) {
		r, size := utf8.DecodeRuneInString(s[end:])
		if isWordRune(r) {
			return false
		}
		if isApostrophe(r) {
			if far, _ := utf8.DecodeRuneInString(s[end+size:]); isWordRune(far) {
				return false
			}
		}
	}
	return true
}

// countWordOccurrences counts case-insensitive, word-boundary-anchored,
// non-overlapping occurrences of needle within haystack. A boundary is the
// start/end of the string or an adjacent rune that is neither a Unicode letter
// nor digit. This kills subword false matches — the term "table" must not match
// inside "Tabellerkennung" — while still returning a count so callers can
// compare source-side vs. target-side frequency (occurrence check).
//
// Limitation: scripts without word separators (e.g. CJK Han) treat adjacent
// ideographs as letters, so a term flanked by such runes won't match. The
// reported terminology bugs are Latin-script; revisit if CJK term QA is needed
func countWordOccurrences(haystack, needle string) int {
	if needle == "" {
		return 0
	}
	h := strings.ToLower(haystack)
	n := strings.ToLower(needle)
	count := 0
	for from := 0; from <= len(h); {
		idx := strings.Index(h[from:], n)
		if idx < 0 {
			break
		}
		start := from + idx
		end := start + len(n)
		if isWordBoundary(h, start, end) {
			count++
			from = end // non-overlapping
		} else {
			from = start + 1 // resync past this (sub-word) hit
		}
	}
	return count
}

// --- Tag checks ---

// tagPattern matches XML/HTML-style tags and common placeholder formats.
var tagPattern = regexp.MustCompile(`<[^>]+>|\{[^}]+\}|%[0-9]*\$?[sdfu]|%%`)

// Tag matching patterns for XLIFF inline elements and HTML tags.
//
// The identifier attribute is NOT always `id`. XLIFF 1.2 pairs the halves of an
// inline tag with `rid` (each half carries a distinct `id`), and TMX 1.4 pairs
// with `i` — an SDLX-exported TMX writes `<bpt i="1" x="1">…</bpt>…<ept i="1">`
// with no `id` at all. All three must match, or TMX rich text fails the
// `hasXLIFF` sniff in extractTags and is diffed as raw strings, reporting
// phantom tag mismatches on 100% TM matches.
//
// `rid` is listed before `i`/`id` so the alternation prefers the pairing
// attribute, which is what makes both halves of a pair display the same number.
const tagIDAttr = `(?:rid|id|i)`

var (
	bptPattern   = regexp.MustCompile(`<bpt\s[^>]*\b` + tagIDAttr + `="([^"]+)"[^>]*>`)
	eptPattern   = regexp.MustCompile(`<ept\s[^>]*\b` + tagIDAttr + `="([^"]+)"[^>]*>`)
	phPattern    = regexp.MustCompile(`<ph\s[^>]*\b` + tagIDAttr + `="([^"]+)"[^>]*>`)
	xPattern     = regexp.MustCompile(`<x\s[^>]*\b` + tagIDAttr + `="([^"]+)"[^>]*/>`)
	gOpenPattern = regexp.MustCompile(`<g\s[^>]*\b` + tagIDAttr + `="([^"]+)"[^>]*>`)
	htmlOpenTag  = regexp.MustCompile(`<([a-zA-Z][a-zA-Z0-9]*)\b[^/>]*>`)
	htmlCloseTag = regexp.MustCompile(`</([a-zA-Z][a-zA-Z0-9]*)\s*>`)
	selfClosing  = regexp.MustCompile(`<[^>]+/>`)
)

// xliffClosingElement matches closing tags for XLIFF structural elements.
// These are XML structure (e.g. </bpt>, </ept>, </ph>), not inline tags.
var xliffClosingElement = regexp.MustCompile(`^</(bpt|ept|ph|it|hi|sub)>$`)

// displayTag converts raw XLIFF/HTML markup to the display form shown in the editor.
//
//	<bpt id="1"><b></bpt>  → <1>
//	<ept id="1"></b></ept>  → </1>
//	<ph id="2">{key}</ph>  → {2}
//	<x id="3" ctype="lb"/> → {3}
//	<g id="4" ctype="bold"> → {4}
//	</g>                    → {/g}
//	<b>, </b>               → <b>, </b>  (HTML kept as-is)
func displayTag(raw string) string {
	// XLIFF structural closing tags — not user-visible, suppress
	if xliffClosingElement.MatchString(raw) {
		return ""
	}
	// Full XLIFF elements: <bpt id="1"><b></bpt> → <1>
	if m := bptPattern.FindStringSubmatch(raw); m != nil {
		return "<" + m[1] + ">"
	}
	// Full XLIFF elements: <ept id="1"></b></ept> → </1>
	if m := eptPattern.FindStringSubmatch(raw); m != nil {
		return "</" + m[1] + ">"
	}
	if m := phPattern.FindStringSubmatch(raw); m != nil {
		return "{" + m[1] + "}"
	}
	if m := xPattern.FindStringSubmatch(raw); m != nil {
		return "{" + m[1] + "}"
	}
	if m := gOpenPattern.FindStringSubmatch(raw); m != nil {
		return "{" + m[1] + "}"
	}
	if raw == "</g>" {
		return "{/g}"
	}
	return raw
}

// displayTags renders tags for a finding's message, sorted.
//
// The sort is load-bearing, not cosmetic: every caller collects its tags from a
// map, so without it the message's word order is randomised per run. A finding's
// message is its identity for the per-finding ignore, so an
// unstable message makes an ignored finding un-ignore itself on the next run.
func displayTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		d := displayTag(t)
		if d != "" {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// xliffFullElement patterns match complete XLIFF elements including content:
// <bpt id="1"><b></bpt>, <ept id="1"></b></ept>, <ph id="2">{key}</ph>
var (
	fullBpt = regexp.MustCompile(`<bpt\s[^>]*>.*?</bpt>`)
	fullEpt = regexp.MustCompile(`<ept\s[^>]*>.*?</ept>`)
	fullPh  = regexp.MustCompile(`<ph\s[^>]*>.*?</ph>`)
	fullIt  = regexp.MustCompile(`<it\s[^>]*>.*?</it>`)
)

// extractTags returns the list of tags/placeholders in a string.
// For XLIFF rich text, it extracts complete XLIFF elements (bpt...bpt, ept.../ept)
// as single units, plus x/g/hi tags and non-XLIFF placeholders ({var}, %s, etc).
// For plain text, falls back to tagPattern.
func extractTags(text string) []string {
	// Check if text has XLIFF elements
	hasXLIFF := bptPattern.MatchString(text) || eptPattern.MatchString(text) ||
		phPattern.MatchString(text)

	if !hasXLIFF {
		return tagPattern.FindAllString(text, -1)
	}

	// Extract complete XLIFF elements as single units
	var tags []string
	remaining := text

	// Find all full XLIFF elements first
	covered := make([]bool, len(remaining))
	for _, pat := range []*regexp.Regexp{fullBpt, fullEpt, fullPh, fullIt} {
		for _, idx := range pat.FindAllStringIndex(remaining, -1) {
			match := remaining[idx[0]:idx[1]]
			tags = append(tags, match)
			for i := idx[0]; i < idx[1]; i++ {
				covered[i] = true
			}
		}
	}

	// Also extract x (self-closing), g tags, and non-XML placeholders from uncovered regions
	allGeneric := tagPattern.FindAllStringIndex(remaining, -1)
	for _, idx := range allGeneric {
		// Skip if this range is covered by a full XLIFF element
		isCovered := false
		for i := idx[0]; i < idx[1]; i++ {
			if covered[i] {
				isCovered = true
				break
			}
		}
		if isCovered {
			continue
		}
		match := remaining[idx[0]:idx[1]]
		// Skip XLIFF structural closing tags that weren't part of a full element
		if xliffClosingElement.MatchString(match) {
			continue
		}
		tags = append(tags, match)
	}

	return tags
}

// CheckTagsDetailed does a single pass over source and target tags, returning
// one issue per root cause. The config controls which sub-checks are active.
// Tags reported as missing/extra are excluded from unclosed/unopened checks
// to avoid cascading errors from a single root cause.
//
// Comparisons are keyed by `displayTag(...)` (the canonical user-
// visible form — `<1>` / `</1>` / `{2}`), NOT by the raw markup. Two
// `<ph id="1"/>` tags are the SAME tag even if their attributes differ
// (`<ph id="1" x="ENTER"/>` vs `<ph id="1"/>`), so an attribute difference
// never reports one tag as both missing and extra. HTML tags (no XLIFF id)
// fall through `displayTag` to their raw form, so `<a href="x">` vs
// `<a href="y">` still correctly compare as distinct.
func CheckTagsDetailed(source, target, severity string, cfg TagChecksConfig) []Issue {
	srcTags := extractTags(source)
	tgtTags := extractTags(target)

	if len(srcTags) == 0 && len(tgtTags) == 0 {
		return nil
	}

	// Bucket each raw tag under its display key. Map preserves the original
	// raw string for the FIRST occurrence of each key — the message renderer
	// and `recordMismatch` (which extracts ID from the raw form) need a
	// representative raw tag, not the display key.
	bucket := func(tags []string) (counts map[string]int, repr map[string]string) {
		counts = make(map[string]int, len(tags))
		repr = make(map[string]string, len(tags))
		for _, raw := range tags {
			key := displayTag(raw)
			if key == "" {
				// Suppressed XLIFF structural closing tags — already filtered
				// by extractTags, but be defensive.
				continue
			}
			counts[key]++
			if _, seen := repr[key]; !seen {
				repr[key] = raw
			}
		}
		return
	}
	srcCount, srcRepr := bucket(srcTags)
	tgtCount, tgtRepr := bucket(tgtTags)

	// Track paired tag IDs with missing/extra issues to suppress cascading.
	// When bpt id="1" or ept id="1" is flagged as missing/extra, we record
	// the ID so checkTagPairing skips it (the root cause is already reported).
	mismatchIDs := make(map[string]bool)  // XLIFF pair IDs (e.g. "1")
	mismatchTags := make(map[string]bool) // HTML tag names (e.g. "b")
	recordMismatch := func(tag string) {
		if m := bptPattern.FindStringSubmatch(tag); m != nil {
			mismatchIDs[m[1]] = true
		} else if m := eptPattern.FindStringSubmatch(tag); m != nil {
			mismatchIDs[m[1]] = true
		} else if m := htmlCloseTag.FindStringSubmatch(tag); m != nil {
			mismatchTags[strings.ToLower(m[1])] = true
		} else if m := htmlOpenTag.FindStringSubmatch(tag); m != nil {
			mismatchTags[strings.ToLower(m[1])] = true
		}
	}

	var issues []Issue

	// 1. Missing: source tag (by display key) absent from target.
	if cfg.Missing {
		var missing []string
		for key, n := range srcCount {
			if tgtCount[key] < n {
				rawForRecord := srcRepr[key]
				for i := 0; i < n-tgtCount[key]; i++ {
					missing = append(missing, rawForRecord)
					recordMismatch(rawForRecord)
				}
			}
		}
		if len(missing) > 0 {
			issues = append(issues, Issue{
				CheckType: "tag_missing",
				Severity:  severity,
				Message:   fmt.Sprintf("Source tag(s) missing from target: %s", strings.Join(displayTags(missing), ", ")),
			})
		}
	}

	// 2. Extra: target tag (by display key) absent from source.
	if cfg.Extra {
		var extra []string
		for key, n := range tgtCount {
			if srcCount[key] < n {
				rawForRecord := tgtRepr[key]
				for i := 0; i < n-srcCount[key]; i++ {
					extra = append(extra, rawForRecord)
					recordMismatch(rawForRecord)
				}
			}
		}
		if len(extra) > 0 {
			issues = append(issues, Issue{
				CheckType: "tag_extra",
				Severity:  severity,
				Message:   fmt.Sprintf("Target has extra tag(s) not in source: %s", strings.Join(displayTags(extra), ", ")),
			})
		}
	}

	// 3 & 4. Unclosed/unopened: structural checks on target only.
	// Skip IDs/tags already reported as missing/extra — they're the root cause.
	if cfg.Unclosed || cfg.Unopened {
		unclosed, unopened := checkTagPairing(tgtTags, mismatchIDs, mismatchTags)

		if cfg.Unclosed && len(unclosed) > 0 {
			issues = append(issues, Issue{
				CheckType: "tag_unclosed",
				Severity:  severity,
				Message:   fmt.Sprintf("Opening tag(s) without closing tag: %s", strings.Join(displayTags(unclosed), ", ")),
			})
		}
		if cfg.Unopened && len(unopened) > 0 {
			issues = append(issues, Issue{
				CheckType: "tag_unopened",
				Severity:  severity,
				Message:   fmt.Sprintf("Closing tag(s) without opening tag: %s", strings.Join(displayTags(unopened), ", ")),
			})
		}
	}

	return issues
}

// checkTagPairing finds unclosed/unopened tags in a tag list.
// mismatchIDs contains XLIFF pair IDs already reported (e.g. "1" for bpt/ept id="1").
// mismatchHTMLTags contains HTML tag names already reported (e.g. "b").
func checkTagPairing(tags []string, mismatchIDs, mismatchHTMLTags map[string]bool) (unclosed, unopened []string) {
	// XLIFF bpt/ept pairing by id — skip IDs already reported as missing/extra
	bptIDs := make(map[string]int)
	eptIDs := make(map[string]int)
	for _, tag := range tags {
		if m := bptPattern.FindStringSubmatch(tag); m != nil {
			if !mismatchIDs[m[1]] {
				bptIDs[m[1]]++
			}
		}
		if m := eptPattern.FindStringSubmatch(tag); m != nil {
			if !mismatchIDs[m[1]] {
				eptIDs[m[1]]++
			}
		}
	}
	for id, n := range bptIDs {
		for i := 0; i < n-eptIDs[id]; i++ {
			unclosed = append(unclosed, fmt.Sprintf(`<bpt id="%s">`, id))
		}
	}
	for id, n := range eptIDs {
		for i := 0; i < n-bptIDs[id]; i++ {
			unopened = append(unopened, fmt.Sprintf(`<ept id="%s">`, id))
		}
	}

	// HTML-style open/close pairing (skip XLIFF elements, self-closing, and mismatched)
	xliffNames := map[string]bool{"bpt": true, "ept": true, "ph": true, "it": true, "x": true, "g": true, "hi": true, "sub": true}
	openCount := make(map[string]int)
	closeCount := make(map[string]int)
	for _, tag := range tags {
		// Skip full XLIFF elements — they're handled by bpt/ept pairing above
		if bptPattern.MatchString(tag) || eptPattern.MatchString(tag) || phPattern.MatchString(tag) {
			continue
		}
		if selfClosing.MatchString(tag) {
			continue
		}
		if m := htmlCloseTag.FindStringSubmatch(tag); m != nil && !xliffNames[m[1]] && !mismatchHTMLTags[strings.ToLower(m[1])] {
			closeCount[strings.ToLower(m[1])]++
		} else if m := htmlOpenTag.FindStringSubmatch(tag); m != nil && !xliffNames[m[1]] && !mismatchHTMLTags[strings.ToLower(m[1])] {
			openCount[strings.ToLower(m[1])]++
		}
	}
	for tag, n := range openCount {
		for i := 0; i < n-closeCount[tag]; i++ {
			unclosed = append(unclosed, "<"+tag+">")
		}
	}
	for tag, n := range closeCount {
		for i := 0; i < n-openCount[tag]; i++ {
			unopened = append(unopened, "</"+tag+">")
		}
	}

	return
}

// --- Other checks ---

// CheckEmpty flags empty or whitespace-only translations.
func CheckEmpty(target, severity string) *Issue {
	if strings.TrimSpace(target) == "" {
		return &Issue{
			CheckType: "empty_target",
			Severity:  severity,
			Message:   "Target segment is empty",
		}
	}
	return nil
}

// CheckTargetIdenticalToSource flags segments where the target text is the same
// as the source text (case-insensitive). This catches the common problem where
// a translator confirms without editing (frontend auto-copies source → target)
// or MT returns the source unchanged.
func CheckTargetIdenticalToSource(source, target, severity string) *Issue {
	if strings.TrimSpace(target) == "" {
		return nil // empty_target handles this case
	}
	if strings.EqualFold(strings.TrimSpace(source), strings.TrimSpace(target)) {
		return &Issue{
			CheckType: "target_identical_to_source",
			Severity:  severity,
			Message:   "Target is identical to source (potentially untranslated)",
		}
	}
	return nil
}

var numberPattern = regexp.MustCompile(`-?\d[\d,.]*`)

// CheckNumbers verifies numbers are consistent between source and target — in
// BOTH directions — and returns up to two issues at DIFFERENT
// severities:
//
//   - number_consistency (configured severity): numbers present in the source
//     but missing from the target. Dropping a source number is a real defect;
//     at error severity it blocks confirm.
//   - number_added (WARNING, non-blocking): numbers present in the target but
//     absent from the source. Often legitimate (spelled-out → digit, footnote
//     markers), so it is surfaced but never walls the translator (product
//     decision 2026-08-12). Hardcoded warning, independent of the rule's
//     configured severity.
func CheckNumbers(source, target, severity string) []Issue {
	srcNums := numberPattern.FindAllString(source, -1)
	tgtNums := numberPattern.FindAllString(target, -1)
	if len(srcNums) == 0 && len(tgtNums) == 0 {
		return nil
	}

	normalize := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, ",", ""), ".", "")
	}

	srcSet := make(map[string]bool, len(srcNums))
	for _, n := range srcNums {
		srcSet[normalize(n)] = true
	}
	tgtSet := make(map[string]bool, len(tgtNums))
	for _, n := range tgtNums {
		tgtSet[normalize(n)] = true
	}

	collectUnmatched := func(nums []string, other map[string]bool) []string {
		var out []string
		seen := make(map[string]bool)
		for _, n := range nums {
			norm := normalize(n)
			if !other[norm] && !seen[norm] {
				out = append(out, n)
				seen[norm] = true
			}
		}
		return out
	}

	var issues []Issue
	if missing := collectUnmatched(srcNums, tgtSet); len(missing) > 0 {
		issues = append(issues, Issue{
			CheckType: "number_consistency",
			Severity:  severity,
			Message:   fmt.Sprintf("Missing number(s) in target: %s", strings.Join(missing, ", ")),
		})
	}
	if extra := collectUnmatched(tgtNums, srcSet); len(extra) > 0 {
		issues = append(issues, Issue{
			CheckType: "number_added",
			Severity:  "warning",
			Message:   fmt.Sprintf("Number(s) in target but not source: %s", strings.Join(extra, ", ")),
		})
	}
	return issues
}

// CheckTerminology checks that each triggered concept's approved translation is
// used in the target with the right FREQUENCY. It works per CONCEPT, and the
// approved target synonyms are ALTERNATIVES: the concept is satisfied if the
// target contains enough of ANY of them combined.
//
// Per concept:
//   - srcCount = MAX word-boundary occurrences over the concept's source
//     synonyms (max, not sum — the leniency that kills false positives). A
//     concept whose source doesn't occur as a whole word is skipped (the DB
//     pre-filter uses a loose ILIKE, so "cat" can be looked up from "category").
//   - tgtCount = SUM of word-boundary occurrences over the approved targets
//     (sum, so "password password" → "Passwort … Kennwort" satisfies 2-of-2).
//   - violation when the concept has approved targets, its source occurs, and
//     tgtCount < srcCount. Extra target occurrences are fine.
//
// Word-boundary matching avoids subword false matches on both sides
// ("table" ≠ inside "Tabellerkennung").
func CheckTerminology(source, target string, concepts []ConceptForQA, severity string) *Issue {
	if len(concepts) == 0 {
		return nil
	}

	var violations []string
	for _, c := range concepts {
		if len(c.ApprovedTargets) == 0 {
			continue // no approved term to enforce (may still have forbidden ones)
		}
		srcCount, srcRepr := 0, ""
		for _, st := range c.SourceTerms {
			if n := countWordOccurrences(source, st); n > srcCount {
				srcCount, srcRepr = n, st
			}
		}
		if srcCount == 0 {
			continue // concept not actually triggered in the source
		}
		tgtCount := 0
		for _, at := range c.ApprovedTargets {
			tgtCount += countWordOccurrences(target, at)
		}
		if tgtCount >= srcCount {
			continue // satisfied by one or more approved alternatives
		}
		if tgtCount == 0 {
			violations = append(violations, fmt.Sprintf("%q (no approved term used)", srcRepr))
		} else {
			violations = append(violations, fmt.Sprintf("%q (approved term used %d of %d)", srcRepr, tgtCount, srcCount))
		}
	}

	if len(violations) == 0 {
		return nil
	}

	return &Issue{
		CheckType: "terminology",
		Severity:  severity,
		Message:   fmt.Sprintf("Term(s) not used: %s", strings.Join(violations, ", ")),
	}
}

// CheckForbiddenTerms flags when any concept's forbidden target term IS present
// in the translation. Deliberately the OPPOSITE aggregation from the approved
// case: approved synonyms are alternatives (any satisfies), but every forbidden
// synonym independently fails if present. Not source-gated — a banned term is
// banned regardless of what the source says.
func CheckForbiddenTerms(target string, concepts []ConceptForQA, severity string) *Issue {
	var found []string
	for _, c := range concepts {
		for _, ft := range c.ForbiddenTargets {
			if countWordOccurrences(target, ft) > 0 {
				found = append(found, fmt.Sprintf("%q", ft))
			}
		}
	}

	if len(found) == 0 {
		return nil
	}

	return &Issue{
		CheckType: "forbidden_terms",
		Severity:  severity,
		Message:   fmt.Sprintf("Forbidden term(s) used: %s", strings.Join(found, ", ")),
	}
}

// CheckPunctuation compares leading/trailing punctuation between source and target.
func CheckPunctuation(source, target, severity string) *Issue {
	source = strings.TrimSpace(source)
	target = strings.TrimSpace(target)
	if source == "" || target == "" {
		return nil
	}

	var issues []string

	srcEnd := lastPunct(source)
	tgtEnd := lastPunct(target)
	if srcEnd != tgtEnd {
		if srcEnd != "" && tgtEnd == "" {
			issues = append(issues, fmt.Sprintf("missing trailing %q", srcEnd))
		} else if srcEnd == "" && tgtEnd != "" {
			issues = append(issues, fmt.Sprintf("unexpected trailing %q", tgtEnd))
		} else {
			issues = append(issues, fmt.Sprintf("trailing punctuation: source %q vs target %q", srcEnd, tgtEnd))
		}
	}

	srcStart := firstPunct(source)
	tgtStart := firstPunct(target)
	if srcStart != tgtStart {
		if srcStart != "" && tgtStart == "" {
			issues = append(issues, fmt.Sprintf("missing leading %q", srcStart))
		}
	}

	if len(issues) == 0 {
		return nil
	}

	return &Issue{
		CheckType: "punctuation",
		Severity:  severity,
		Message:   fmt.Sprintf("Punctuation mismatch — %s", strings.Join(issues, "; ")),
	}
}

// CheckConsistency checks if the same source text has different translations.
func CheckConsistency(target string, otherTargets []string, severity string) *Issue {
	if len(otherTargets) == 0 {
		return nil
	}

	tgtNorm := strings.TrimSpace(strings.ToLower(target))
	for _, other := range otherTargets {
		otherNorm := strings.TrimSpace(strings.ToLower(other))
		if otherNorm != "" && otherNorm != tgtNorm {
			return &Issue{
				CheckType: "consistency",
				Severity:  severity,
				Message:   fmt.Sprintf("Inconsistent translation — same source has different target: %q", truncate(other, 60)),
			}
		}
	}
	return nil
}

// --- Helpers ---

var sentenceEndPunct = map[rune]bool{
	'.': true, '!': true, '?': true, '…': true, ':': true, ';': true,
	'。': true, '！': true, '？': true,
}

func lastPunct(s string) string {
	if s == "" {
		return ""
	}
	r := rune(s[len(s)-1])
	if sentenceEndPunct[r] {
		return string(r)
	}
	return ""
}

var leadingPunct = map[rune]bool{
	'•': true, '–': true, '—': true, '-': true,
	'¿': true, '¡': true,
}

func firstPunct(s string) string {
	if s == "" {
		return ""
	}
	for _, r := range s {
		if leadingPunct[r] {
			return string(r)
		}
		break
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func countStrings(ss []string) map[string]int {
	m := make(map[string]int, len(ss))
	for _, s := range ss {
		m[s]++
	}
	return m
}
