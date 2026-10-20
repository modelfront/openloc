package aiprompt

import (
	"sort"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
)

// Per-language prompt additions. A system prompt is shared across every
// language pair its engine routes; an addition carries the part that only holds
// for some of them, and is appended to the instruction text when the job's
// target language matches.
//
// The target side is the only constraint: a router picks its engine by target
// language, so that is the axis a prompt's languages vary along. An addition
// naming no target would match every job, which is what the prompt's own
// instruction text is already for — refused by Validate and by a CHECK on the
// table.
//
// Matching is by base subtag, so an addition written for `de` applies to a
// de-AT job. That is the behaviour a tenant expects from a language rule here,
// and it matches how TM and terminology assignments already resolve sublanguages.

// Matches reports whether an addition's target constraint admits this job.
//
// langcode.SameBase expects normalized input — it compares primary subtags
// textually, so an un-normalized "DE_AT" has no parseable base and silently
// matches nothing. Both sides are normalized here rather than trusted: the
// stored codes are normalized on write, but a job's languages reach this from
// the project row.
func Matches(targetCodes []string, targetLang string) bool {
	norm := langcode.Normalize(targetLang)
	if norm == "" {
		return false
	}
	for _, code := range targetCodes {
		if c := langcode.Normalize(code); c != "" && langcode.SameBase(c, norm) {
			return true
		}
	}
	return false
}

// ApplyLanguageAdditions returns the instruction text with every matching
// addition appended, in position order, one blank line apart. The base text is
// returned unchanged when nothing matches — which is the common case, since a
// prompt usually carries additions for a handful of its languages.
//
// Appending (rather than prepending) keeps the tenant's general instructions
// first and the language-specific exception last, which is the order that reads
// as an override.
func ApplyLanguageAdditions(base string, additions []db.AiPromptLanguageAddition, targetLang string) string {
	matched := make([]string, 0, len(additions))
	for _, a := range sortedByPosition(additions) {
		text := strings.TrimSpace(a.AdditionText)
		if text == "" || !Matches(a.TargetLanguages, targetLang) {
			continue
		}
		matched = append(matched, text)
	}
	if len(matched) == 0 {
		return base
	}
	return strings.TrimSpace(strings.TrimSpace(base) + "\n\n" + strings.Join(matched, "\n\n"))
}

// sortedByPosition orders a copy of the additions. The queries already return
// them ordered, but the execution path must not depend on a caller having used
// the right one — an addition landing out of order is invisible in the result.
func sortedByPosition(additions []db.AiPromptLanguageAddition) []db.AiPromptLanguageAddition {
	out := append([]db.AiPromptLanguageAddition(nil), additions...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// NormalizeLanguages cleans a submitted code list: trims, normalizes each code
// (langcode.Normalize, never strings.ToLower — locale codes carry case-bearing
// subtags), drops blanks and de-duplicates while keeping the author's order.
func NormalizeLanguages(codes []string) []string {
	seen := make(map[string]bool, len(codes))
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		norm := langcode.Normalize(c)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	return out
}
