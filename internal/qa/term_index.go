package qa

import (
	"regexp"
	"sort"
	"strings"

	db "openloc/internal/database/sqlcgen"
)

// termIndex answers LookupSettingsTerms for many segments from one load of the
// glossary: a row is a candidate for a segment when
// `source ILIKE '%' || source_term || '%'`, and candidates keep the query's
// row order. ILIKE compares lower-cased text; in the term, `%` is any run of
// characters, `_` any one character, and `\` makes the next character literal.
type termIndex struct {
	rows     []TermRow
	patRows  [][]int // rows of each pattern, in row order
	patterns []termPattern
	byPrefix map[string][]int // literal patterns of 3+ bytes, by their first 3 bytes
	short    []int            // literal patterns shorter than 3 bytes
	wild     []int            // patterns with a wildcard
}

type termPattern struct {
	lit string         // lower-cased literal text, when re is nil
	re  *regexp.Regexp // the pattern with wildcards
}

const termPrefixLen = 3

func newTermIndex(rows []db.ListSettingsTermsForQARow) *termIndex {
	ix := &termIndex{
		rows:     make([]TermRow, len(rows)),
		byPrefix: make(map[string][]int),
	}
	seen := make(map[string]int)
	for i, r := range rows {
		ix.rows[i] = TermRow{
			ConceptID:    r.ConceptID,
			Domain:       r.Domain,
			SourceTerm:   r.SourceTerm,
			TargetTerm:   r.TargetTerm,
			Status:       r.Status,
			SourceStatus: r.SourceStatus,
		}
		key := strings.ToLower(r.SourceTerm)
		p, ok := seen[key]
		if !ok {
			p = len(ix.patterns)
			seen[key] = p
			pat := compileTermPattern(key)
			ix.patterns = append(ix.patterns, pat)
			ix.patRows = append(ix.patRows, nil)
			switch {
			case pat.re != nil:
				ix.wild = append(ix.wild, p)
			case len(pat.lit) < termPrefixLen:
				ix.short = append(ix.short, p)
			default:
				prefix := pat.lit[:termPrefixLen]
				ix.byPrefix[prefix] = append(ix.byPrefix[prefix], p)
			}
		}
		ix.patRows[p] = append(ix.patRows[p], i)
	}
	return ix
}

// compileTermPattern turns a lower-cased term into a literal or, when it holds
// a wildcard, an unanchored regexp. A trailing lone `\` is taken literally.
func compileTermPattern(term string) termPattern {
	var lit strings.Builder
	var re strings.Builder
	wild := false
	runes := []rune(term)
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; {
		case r == '\\' && i+1 < len(runes):
			i++
			lit.WriteRune(runes[i])
			re.WriteString(regexp.QuoteMeta(string(runes[i])))
		case r == '%':
			wild = true
			re.WriteString(".*")
		case r == '_':
			wild = true
			re.WriteString(".")
		default:
			lit.WriteRune(r)
			re.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	if !wild {
		return termPattern{lit: lit.String()}
	}
	return termPattern{re: regexp.MustCompile("(?s)" + re.String())}
}

// concepts is ConceptsFromLookupRows of the rows LookupSettingsTerms returns
// for source.
func (ix *termIndex) concepts(source string) []ConceptForQA {
	lower := strings.ToLower(source)
	matched := make(map[int]bool)
	for _, p := range ix.short {
		if strings.Contains(lower, ix.patterns[p].lit) {
			matched[p] = true
		}
	}
	for _, p := range ix.wild {
		if ix.patterns[p].re.MatchString(lower) {
			matched[p] = true
		}
	}
	for i := 0; i+termPrefixLen <= len(lower); i++ {
		for _, p := range ix.byPrefix[lower[i:i+termPrefixLen]] {
			if !matched[p] && strings.HasPrefix(lower[i:], ix.patterns[p].lit) {
				matched[p] = true
			}
		}
	}
	// The matched patterns' rows, back in the query's row order.
	var hit []int
	for p := range matched {
		hit = append(hit, ix.patRows[p]...)
	}
	sort.Ints(hit)
	trs := make([]TermRow, len(hit))
	for i, r := range hit {
		trs[i] = ix.rows[r]
	}
	return GroupConcepts(trs)
}
