package productdocs

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// MaxSearchResults caps a search response.
const MaxSearchResults = 10

// Search ranks pages the role may read against a free-text query. Every query
// term must appear somewhere in the page (title, summary, headings or body); a
// title hit outranks a summary or heading hit, which outranks body occurrences
// (heading text also counts toward the body score). The set is ~30 pages held
// in memory, so a scan is the right tool — no index library.
func (idx *Index) Search(query, role string) []DocSearchHit {
	terms := tokenize(query)
	if len(terms) == 0 {
		return []DocSearchHit{}
	}
	type scored struct {
		page  *Page
		score int
	}
	var matches []scored
	for _, p := range idx.pages {
		if !p.visible(role) || p.Stub {
			continue
		}
		title := strings.ToLower(p.Title)
		summary := strings.ToLower(p.Summary)
		body := strings.ToLower(p.Body)
		score := 0
		for _, t := range terms {
			hit := 0
			if strings.Contains(title, t) {
				hit += 10
			}
			if strings.Contains(summary, t) {
				hit += 5
			}
			for _, h := range p.headings {
				if strings.Contains(h, t) {
					hit += 4
					break
				}
			}
			if n := strings.Count(body, t); n > 0 {
				hit += 1 + min(n, 5)
			}
			if hit == 0 {
				score = 0
				break
			}
			score += hit
		}
		if score > 0 {
			matches = append(matches, scored{p, score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].page.Slug < matches[j].page.Slug
	})
	if len(matches) > MaxSearchResults {
		matches = matches[:MaxSearchResults]
	}
	hits := make([]DocSearchHit, 0, len(matches))
	for _, m := range matches {
		hits = append(hits, DocSearchHit{
			Slug:         m.page.Slug,
			Title:        m.page.Title,
			Section:      m.page.Section,
			SectionTitle: idx.sectionTitle(m.page.Section),
			Snippet:      snippet(m.page, terms[0]),
		})
	}
	return hits
}

func (idx *Index) sectionTitle(slug string) string {
	for _, s := range idx.sections {
		if s.Slug == slug {
			return s.Title
		}
	}
	return ""
}

// tokenize lowercases and splits on anything that is not a letter or digit,
// dropping one-character fragments. Terms are deduplicated.
func tokenize(q string) []string {
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if len(f) < 2 || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// markdownNoise strips the markup a snippet should not show.
var markdownNoise = regexp.MustCompile("[#*_`>\\[\\]()!|]")

// snippet returns ~160 characters of plain text around the first occurrence of
// term in the body, falling back to the summary.
func snippet(p *Page, term string) string {
	plain := strings.Join(strings.Fields(markdownNoise.ReplaceAllString(p.Body, " ")), " ")
	lower := strings.ToLower(plain)
	i := strings.Index(lower, term)
	if i < 0 {
		return p.Summary
	}
	const radius = 80
	start, end := i-radius, i+len(term)+radius
	prefix, suffix := "…", "…"
	if start <= 0 {
		start, prefix = 0, ""
	}
	if end >= len(plain) {
		end, suffix = len(plain), ""
	}
	// Don't cut through a multibyte rune.
	for start > 0 && !isRuneStart(plain[start]) {
		start--
	}
	for end < len(plain) && !isRuneStart(plain[end]) {
		end++
	}
	return prefix + strings.TrimSpace(plain[start:end]) + suffix
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
