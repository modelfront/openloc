// Package segmentation splits the text of a block (a paragraph, a table cell,
// a JSON value) into sentences by an ordered list of break and no-break rules
// and two abbreviation word lists. It also holds the read-only built-in rule
// sets and reads and writes SRX. It has no database access.
package segmentation

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// Limits of a rule set, checked on save, on SRX import and again in Compile.
const (
	MaxRules         = 500
	MaxPatternLength = 1000 // bytes
	// MaxPatternSize bounds what a pattern compiles to, counting each
	// counted repeat as its copies: `.{0,999}` alone is about 2000. The
	// built-ins' patterns stay under 64.
	MaxPatternSize = 1024
	MaxWords       = 2000 // per word list
	MaxWordLength  = 64   // bytes
	MaxPreviewText = 20000
	// ContextWindow is how many bytes on each side of a position a pattern
	// can see, not counting the whitespace right at a position between a
	// non-space character and a space. A match that needs more context than
	// this is not found, and ^ and $ match only when the block edge is inside
	// the window.
	ContextWindow = 512
	// CostPerByte, CostPerRule and CostAllowance bound the work of one
	// split: the patterns may cost CostPerByte plus CostPerRule for each
	// rule per byte of the text, plus CostAllowance, before it fails with
	// ErrTooCostly. An attempt costs the bytes it reads plus a fixed charge,
	// times the pattern's weight; CostPerRule pays for a short attempt on
	// each side of every position, so a long list of ordinary rules fits on
	// any text. The built-ins cost at most about 25 per byte, on text made
	// of full stops.
	CostPerByte   = 1024
	CostPerRule   = 2 * (attemptCost + 4)
	CostAllowance = 16 << 10
)

// InvalidError is a rule set that fails validation. Message is written for
// the user and names the rule, word or limit.
type InvalidError struct {
	Message string
}

func (e *InvalidError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &InvalidError{Message: fmt.Sprintf(format, args...)}
}

// Normalize returns rs with its word lists trimmed (spaces and one trailing
// full stop), empty words dropped and duplicates removed in order, and an
// empty mode read as sentence. Validate expects a normalized set.
func Normalize(rs RuleSet) RuleSet {
	if rs.Mode == "" {
		rs.Mode = ModeSentence
	}
	if rs.Rules == nil {
		rs.Rules = []Rule{}
	}
	rs.Abbreviations = normalizeWords(rs.Abbreviations)
	rs.NumberAbbreviations = normalizeWords(rs.NumberAbbreviations)
	return rs
}

func normalizeWords(words []string) []string {
	out := make([]string, 0, len(words))
	seen := make(map[string]bool, len(words))
	for _, w := range words {
		w = strings.TrimSuffix(strings.TrimSpace(w), ".")
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// Validate checks a normalized rule set: the mode, every limit, and that every
// pattern is a valid RE2 regular expression. The error is an *InvalidError.
func Validate(rs RuleSet) error {
	if rs.Mode != ModeSentence && rs.Mode != ModeParagraph {
		return invalid("Mode must be sentence or paragraph.")
	}
	if len(rs.Rules) > MaxRules {
		return invalid("A rule set can have at most %d rules.", MaxRules)
	}
	for i, r := range rs.Rules {
		if r.Before == "" && r.After == "" {
			return invalid("Rule %d needs a before or an after pattern.", i+1)
		}
		if err := validatePattern(i, "before", r.Before); err != nil {
			return err
		}
		if err := validatePattern(i, "after", r.After); err != nil {
			return err
		}
	}
	if err := validateWords("abbreviation", rs.Abbreviations); err != nil {
		return err
	}
	return validateWords("number abbreviation", rs.NumberAbbreviations)
}

func validatePattern(i int, side, pattern string) error {
	if len(pattern) > MaxPatternLength {
		return invalid("Rule %d: the %s pattern is longer than %d characters.", i+1, side, MaxPatternLength)
	}
	if !utf8.ValidString(pattern) {
		return invalid("Rule %d: the %s pattern is not valid text.", i+1, side)
	}
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return invalid("Rule %d: the %s pattern is not valid: %s.", i+1, side, PatternProblem(pattern, err))
	}
	if programSize(re, MaxPatternSize) > MaxPatternSize {
		return invalid("Rule %d: the %s pattern is %s.", i+1, side, tooComplex)
	}
	return nil
}

// tooComplex explains a pattern over MaxPatternSize.
const tooComplex = "too complex: make its counted repeats such as {0,500} smaller"

// PatternProblem explains why pattern does not compile, naming the constructs
// RE2 does not support.
func PatternProblem(pattern string, err error) string {
	switch {
	case strings.Contains(pattern, "(?="), strings.Contains(pattern, "(?!"):
		return "look-ahead is not supported"
	case strings.Contains(pattern, "(?<="), strings.Contains(pattern, "(?<!"):
		return "look-behind is not supported"
	case strings.Contains(pattern, "(?>"):
		return "atomic groups are not supported"
	case backReference.MatchString(pattern):
		return "back-references are not supported"
	case possessive.MatchString(pattern):
		return "possessive quantifiers are not supported"
	}
	if se, ok := err.(*syntax.Error); ok {
		return fmt.Sprintf("%s in %q", se.Code, se.Expr)
	}
	return err.Error()
}

// Only consulted once a pattern has failed to parse, to name the cause.
var (
	backReference = regexp.MustCompile(`(?:^|[^\\])(?:\\\\)*\\[1-9]`)
	possessive    = regexp.MustCompile(`[*+?}]\+`)
)

func validateWords(kind string, words []string) error {
	if len(words) > MaxWords {
		return invalid("The %s list can have at most %d words.", kind, MaxWords)
	}
	for _, w := range words {
		if len(w) > MaxWordLength {
			return invalid("The %s %q is longer than %d characters.", kind, w, MaxWordLength)
		}
		if !utf8.ValidString(w) || strings.ContainsAny(w, "\n\r\t") {
			return invalid("The %s %q has a line break, a tab or invalid text.", kind, w)
		}
	}
	return nil
}
