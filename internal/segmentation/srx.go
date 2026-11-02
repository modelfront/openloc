package segmentation

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"

	"openloc/internal/langcode"
)

// SRX (Segmentation Rules eXchange) 2.0 import and export. Version 1.0 files
// read too: they differ in namespace and in wrapping language maps in
// <maprule>, and neither matters to a reader that matches local names.
//
// SRX patterns are ICU regular expressions; a pattern RE2 cannot run is left
// out of the draft and reported, never dropped silently. A set's word lists
// are written twice: as plain no-break rules, so other tools apply them, and
// in an extension element in the header, so an import here restores them as
// lists.

const (
	srxNamespace = "http://www.lisa.org/srx20"
	// wordListsNamespace names the extension element that carries a set's
	// mode and word lists.
	wordListsNamespace = "urn:openloc:srx:wordlists:1"
	// MaxSRXSize is the largest SRX file an import reads,
	// MaxSRXLanguageRules and MaxSRXLanguageMaps the most language rules
	// (drafts) and language maps it holds, and MaxSRXWarnings the most
	// problems a draft lists one by one.
	MaxSRXSize          = 1 << 20
	MaxSRXLanguageRules = 200
	MaxSRXLanguageMaps  = 1000
	MaxSRXWarnings      = 100
)

type srxFile struct {
	XMLName xml.Name          `xml:"srx"`
	Header  srxHeader         `xml:"header"`
	Rules   []srxLanguageRule `xml:"body>languagerules>languagerule"`
	// Maps holds SRX 2.0's direct <languagemap>s and SRX 1.0's, which sit in
	// <maprule>s.
	Maps   []srxLanguageMap `xml:"body>maprules>languagemap"`
	Maps10 []srxLanguageMap `xml:"body>maprules>maprule>languagemap"`
}

type srxHeader struct {
	SegmentSubflows string            `xml:"segmentsubflows,attr"`
	Cascade         string            `xml:"cascade,attr"`
	FormatHandles   []srxFormatHandle `xml:"formathandle"`
	WordLists       []srxWordLists    `xml:"urn:openloc:srx:wordlists:1 wordlists"`
}

type srxFormatHandle struct {
	Type    string `xml:"type,attr"`
	Include string `xml:"include,attr"`
}

type srxWordLists struct {
	LanguageRule        string    `xml:"languagerulename,attr"`
	Mode                string    `xml:"mode,attr"`
	GeneratedRules      int       `xml:"generatedrules,attr"`
	Abbreviations       []string  `xml:"urn:openloc:srx:wordlists:1 abbreviation"`
	NumberAbbreviations []string  `xml:"urn:openloc:srx:wordlists:1 numberabbreviation"`
	Rules               []srxRule `xml:"urn:openloc:srx:wordlists:1 rule"`
}

type srxLanguageRule struct {
	Name  string    `xml:"languagerulename,attr"`
	Rules []srxRule `xml:"rule"`
}

type srxRule struct {
	Break  string `xml:"break,attr"`
	Before string `xml:"beforebreak"`
	After  string `xml:"afterbreak"`
}

type srxLanguageMap struct {
	Pattern string `xml:"languagepattern,attr"`
	Rule    string `xml:"languagerulename,attr"`
}

// ParseSRX reads an SRX file into drafts. A file that is not SRX is an
// *InvalidError.
func ParseSRX(data []byte) (SRXImport, error) {
	if len(data) > MaxSRXSize {
		return SRXImport{}, invalid("The SRX file is larger than %d KB.", MaxSRXSize>>10)
	}
	var f srxFile
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	if err := dec.Decode(&f); err != nil {
		return SRXImport{}, invalid("The file is not valid SRX: %s.", xmlProblem(err))
	}
	if len(f.Rules) == 0 {
		return SRXImport{}, invalid("The SRX file has no language rules.")
	}
	if len(f.Rules) > MaxSRXLanguageRules {
		return SRXImport{}, invalid("The SRX file has more than %d language rules.", MaxSRXLanguageRules)
	}
	maps := append(append([]srxLanguageMap{}, f.Maps...), f.Maps10...)
	if len(maps) > MaxSRXLanguageMaps {
		return SRXImport{}, invalid("The SRX file has more than %d language maps.", MaxSRXLanguageMaps)
	}
	out := SRXImport{Drafts: []SRXDraft{}, Warnings: headerWarnings(f.Header)}
	byName := map[string]srxLanguageRule{}
	for _, lr := range f.Rules {
		byName[lr.Name] = lr
	}
	lists := map[string]srxWordLists{}
	for _, wl := range f.Header.WordLists {
		lists[wl.LanguageRule] = wl
	}
	cascade := strings.EqualFold(f.Header.Cascade, "yes")
	// Each map pattern is compiled, and each cascaded language rule
	// imported, once for the whole file; a draft takes each cascaded rule
	// once, however many maps name it.
	covers := make([]*regexp.Regexp, len(maps))
	for i, m := range maps {
		covers[i], _ = regexp.Compile(`^(?i:` + m.Pattern + `)$`) //nolint:errcheck // a pattern that does not compile covers nothing
	}
	type imported struct {
		rules    []Rule
		warnings []string
	}
	cascaded := map[string]imported{}

	for _, lr := range f.Rules {
		d := SRXDraft{Name: strings.TrimSpace(lr.Name), SourceLanguages: []string{}, Warnings: []string{}}
		if d.Name == "" {
			d.Name = "Imported rules"
		}
		own, ownLang := -1, ""
		for i, m := range maps {
			if m.Rule != lr.Name {
				continue
			}
			lang := languageFromPattern(m.Pattern)
			if own < 0 {
				own, ownLang = i, lang
			}
			if lang != "" && !slices.Contains(d.SourceLanguages, lang) {
				d.SourceLanguages = append(d.SourceLanguages, lang)
			}
		}
		wl, hasLists := lists[lr.Name]
		rules := lr.Rules
		if hasLists {
			rules = skipGenerated(rules, wl.GeneratedRules)
		}
		d.RuleSet.Mode = ModeSentence
		d.RuleSet.Rules = importRules(rules, "", &d.Warnings)
		if hasLists {
			if Mode(wl.Mode) == ModeParagraph {
				d.RuleSet.Mode = ModeParagraph
				d.RuleSet.Rules = importRules(wl.Rules, "", &d.Warnings)
			}
			d.RuleSet.Abbreviations = wl.Abbreviations
			d.RuleSet.NumberAbbreviations = wl.NumberAbbreviations
		}
		if cascade && own >= 0 {
			taken := map[string]bool{lr.Name: true}
			for i := own + 1; i < len(maps); i++ {
				m := maps[i]
				if taken[m.Rule] || !patternCovers(covers[i], ownLang) {
					continue
				}
				taken[m.Rule] = true
				next, ok := byName[m.Rule]
				if !ok {
					continue
				}
				imp, done := cascaded[m.Rule]
				if !done {
					imp.rules = importRules(next.Rules, next.Name, &imp.warnings)
					cascaded[m.Rule] = imp
				}
				d.RuleSet.Rules = append(d.RuleSet.Rules, imp.rules...)
				d.Warnings = append(d.Warnings, imp.warnings...)
			}
		}
		// What the limits cut comes first, so the warning cap never hides it.
		cuts := []string{}
		d.RuleSet = limitDraft(Normalize(d.RuleSet), &cuts, &d.Warnings)
		d.Warnings = append(cuts, limitWarnings(d.Warnings)...)
		out.Drafts = append(out.Drafts, d)
	}
	return out, nil
}

// importRules converts SRX rules, leaving out (and reporting) any pattern RE2
// cannot run. from names the cascaded language rule they come from, if any.
func importRules(rules []srxRule, from string, warnings *[]string) []Rule {
	out := []Rule{}
	for i, r := range rules {
		where := fmt.Sprintf("Rule %d", i+1)
		if from != "" {
			where = fmt.Sprintf("Rule %d of %q", i+1, from)
		}
		before, after := icuToRE2(r.Before), icuToRE2(r.After)
		if before == "" && after == "" {
			*warnings = append(*warnings, where+" has no pattern and was left out.")
			continue
		}
		if msg := patternError(before); msg != "" {
			*warnings = append(*warnings, fmt.Sprintf("%s was left out: the before pattern \"%s\": %s.", where, r.Before, msg))
			continue
		}
		if msg := patternError(after); msg != "" {
			*warnings = append(*warnings, fmt.Sprintf("%s was left out: the after pattern \"%s\": %s.", where, r.After, msg))
			continue
		}
		out = append(out, Rule{Before: before, After: after, Break: !strings.EqualFold(strings.TrimSpace(r.Break), "no")})
	}
	return out
}

func patternError(p string) string {
	if p == "" {
		return ""
	}
	if len(p) > MaxPatternLength {
		return fmt.Sprintf("longer than %d characters", MaxPatternLength)
	}
	re, err := syntax.Parse(p, syntax.Perl)
	if err != nil {
		return PatternProblem(p, err)
	}
	if programSize(re, MaxPatternSize) > MaxPatternSize {
		return tooComplex
	}
	return ""
}

// icuEscape matches an ICU \uXXXX or \UXXXXXXXX escape, and a doubled
// backslash so an escaped backslash is left alone.
var icuEscape = regexp.MustCompile(`\\\\|\\u([0-9A-Fa-f]{4})|\\U([0-9A-Fa-f]{8})`)

// icuToRE2 rewrites the ICU escapes RE2 spells differently (\uXXXX is
// \x{XXXX}); everything else is left for RE2 to accept or reject.
func icuToRE2(p string) string {
	return icuEscape.ReplaceAllStringFunc(p, func(m string) string {
		if m == `\\` {
			return m
		}
		hex := strings.TrimLeft(m[2:], "0")
		if hex == "" {
			hex = "0"
		}
		return `\x{` + hex + `}`
	})
}

// skipGenerated drops the first n rules, the word-list rules an export here
// wrote for other tools; the extension element restores them as lists.
func skipGenerated(rules []srxRule, n int) []srxRule {
	if n <= 0 {
		return rules
	}
	if n > len(rules) {
		n = len(rules)
	}
	return rules[n:]
}

// limitDraft trims a draft to the limits a saved set must keep: what a limit
// cut goes to cuts, each word it left out to warnings.
func limitDraft(rs RuleSet, cuts, warnings *[]string) RuleSet {
	if len(rs.Rules) > MaxRules {
		*cuts = append(*cuts, fmt.Sprintf("Only the first %d of %d rules were kept.", MaxRules, len(rs.Rules)))
		rs.Rules = append([]Rule(nil), rs.Rules[:MaxRules]...)
	}
	rs.Abbreviations = limitWords(rs.Abbreviations, "abbreviation", cuts, warnings)
	rs.NumberAbbreviations = limitWords(rs.NumberAbbreviations, "number abbreviation", cuts, warnings)
	return rs
}

// limitWarnings keeps the first MaxSRXWarnings warnings and counts the rest.
func limitWarnings(ws []string) []string {
	if len(ws) <= MaxSRXWarnings {
		return ws
	}
	out := append([]string(nil), ws[:MaxSRXWarnings]...)
	return append(out, fmt.Sprintf("%d more problems are not listed.", len(ws)-MaxSRXWarnings))
}

func limitWords(words []string, kind string, cuts, warnings *[]string) []string {
	out := []string{}
	for _, w := range words {
		if validateWords(kind, []string{w}) != nil {
			*warnings = append(*warnings, fmt.Sprintf("The %s %q was left out: it is too long or has a line break.", kind, w))
			continue
		}
		out = append(out, w)
	}
	if len(out) > MaxWords {
		*cuts = append(*cuts, fmt.Sprintf("Only the first %d of %d %ss were kept.", MaxWords, len(out), kind))
		out = out[:MaxWords]
	}
	return out
}

// headerWarnings reports the header settings an import ignores when they
// differ from SRX's defaults: tags never split a segment here.
func headerWarnings(h srxHeader) []string {
	out := []string{}
	if h.SegmentSubflows != "" && !strings.EqualFold(h.SegmentSubflows, "yes") {
		out = append(out, `segmentsubflows="no" is ignored.`)
	}
	defaults := map[string]string{"start": "no", "end": "yes", "isolated": "no"}
	for _, fh := range h.FormatHandles {
		if want, ok := defaults[fh.Type]; ok && !strings.EqualFold(fh.Include, want) {
			out = append(out, fmt.Sprintf(`formathandle type=%q include=%q is ignored.`, fh.Type, fh.Include))
		}
	}
	return out
}

// simpleLanguagePattern is a language map pattern that names one language:
// "EN.*", "(?i)fr_CA", "de(?:[-_].*)?".
var simpleLanguagePattern = regexp.MustCompile(`^(?:\(\?i\))?([A-Za-z]{2,3})(?:(?:-|_|\[-_\]|\[_-\])([A-Za-z0-9]{2,8}))?(?:\.\*|\.\+|(?:-|_|\[-_\]|\[_-\])\.\*|\(\?:(?:-|_|\[-_\]|\[_-\])\.\*\)\?)?$`)

// languageFromPattern returns the language a map pattern names, normalized,
// or "" when it names none or many.
func languageFromPattern(p string) string {
	m := simpleLanguagePattern.FindStringSubmatch(strings.TrimSpace(p))
	if m == nil {
		return ""
	}
	code := m[1]
	if m[2] != "" {
		code += "-" + m[2]
	}
	code = langcode.Normalize(code)
	if !langcode.Known(code) {
		return ""
	}
	return code
}

// patternCovers reports whether a compiled language map pattern applies to
// lang. With no language it applies only when it matches any language.
func patternCovers(re *regexp.Regexp, lang string) bool {
	if re == nil {
		return false
	}
	if lang == "" {
		for _, probe := range []string{"en", "de-AT", "ja", "x-none"} {
			if !re.MatchString(probe) {
				return false
			}
		}
		return true
	}
	for _, form := range []string{lang, strings.ReplaceAll(lang, "-", "_")} {
		if re.MatchString(form) || re.MatchString(strings.ToUpper(form)) {
			return true
		}
	}
	return false
}

func xmlProblem(err error) string {
	if se, ok := err.(*xml.SyntaxError); ok {
		return fmt.Sprintf("line %d: %s", se.Line, se.Msg)
	}
	if err == io.EOF {
		return "the file is empty"
	}
	return err.Error()
}

// ExportSRX writes one rule set as an SRX 2.0 file: one language rule named
// name and one language map per language in langs (one for any language when
// there are none).
func ExportSRX(name string, langs []string, rs RuleSet) ([]byte, error) {
	rs = Normalize(rs)
	var generated []Rule
	for _, w := range rs.Abbreviations {
		generated = append(generated, Rule{Before: `(?:^|[^\p{L}])` + regexp.QuoteMeta(w) + `\.`, After: `\s`})
	}
	for _, w := range rs.NumberAbbreviations {
		generated = append(generated, Rule{Before: `(?:^|[^\p{L}])` + regexp.QuoteMeta(w) + `\.`, After: `\s+\p{Nd}`})
	}
	rules := append([]Rule{}, generated...)
	wl := srxWordLists{
		LanguageRule: name, Mode: string(rs.Mode), GeneratedRules: len(generated),
		Abbreviations: rs.Abbreviations, NumberAbbreviations: rs.NumberAbbreviations,
	}
	if rs.Mode == ModeParagraph {
		// Other tools see only the no-break rules, so they split nothing;
		// the full list travels in the extension.
		for _, r := range rs.Rules {
			if !r.Break {
				rules = append(rules, r)
			}
			wl.Rules = append(wl.Rules, toSRXRule(r))
		}
	} else {
		rules = append(rules, rs.Rules...)
	}

	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, "<srx xmlns=%q version=\"2.0\">\n", srxNamespace)
	b.WriteString("  <header segmentsubflows=\"yes\" cascade=\"no\">\n")
	b.WriteString("    <formathandle type=\"start\" include=\"no\"/>\n")
	b.WriteString("    <formathandle type=\"end\" include=\"yes\"/>\n")
	b.WriteString("    <formathandle type=\"isolated\" include=\"no\"/>\n")
	fmt.Fprintf(&b, "    <wordlists xmlns=%q languagerulename=%s mode=%s generatedrules=\"%d\">\n",
		wordListsNamespace, attr(wl.LanguageRule), attr(wl.Mode), wl.GeneratedRules)
	for _, w := range wl.Abbreviations {
		fmt.Fprintf(&b, "      <abbreviation>%s</abbreviation>\n", text(w))
	}
	for _, w := range wl.NumberAbbreviations {
		fmt.Fprintf(&b, "      <numberabbreviation>%s</numberabbreviation>\n", text(w))
	}
	for _, r := range wl.Rules {
		writeSRXRule(&b, "      ", r)
	}
	b.WriteString("    </wordlists>\n")
	b.WriteString("  </header>\n  <body>\n    <languagerules>\n")
	fmt.Fprintf(&b, "      <languagerule languagerulename=%s>\n", attr(name))
	for _, r := range rules {
		writeSRXRule(&b, "        ", toSRXRule(r))
	}
	b.WriteString("      </languagerule>\n    </languagerules>\n    <maprules>\n")
	if len(langs) == 0 {
		langs = []string{""}
	}
	for _, lang := range langs {
		fmt.Fprintf(&b, "      <languagemap languagepattern=%s languagerulename=%s/>\n", attr(languagePattern(lang)), attr(name))
	}
	b.WriteString("    </maprules>\n  </body>\n</srx>\n")
	return b.Bytes(), nil
}

func toSRXRule(r Rule) srxRule {
	brk := "yes"
	if !r.Break {
		brk = "no"
	}
	return srxRule{Break: brk, Before: r.Before, After: r.After}
}

func writeSRXRule(b *bytes.Buffer, indent string, r srxRule) {
	fmt.Fprintf(b, "%s<rule break=%q>\n", indent, r.Break)
	fmt.Fprintf(b, "%s  <beforebreak>%s</beforebreak>\n", indent, text(r.Before))
	fmt.Fprintf(b, "%s  <afterbreak>%s</afterbreak>\n", indent, text(r.After))
	fmt.Fprintf(b, "%s</rule>\n", indent)
}

// languagePattern is a case-insensitive map pattern for lang and its
// regional variants; ".*" for any language.
func languagePattern(lang string) string {
	if lang == "" {
		return ".*"
	}
	return "(?i)" + strings.ReplaceAll(regexp.QuoteMeta(lang), "-", "[-_]") + "(?:[-_].*)?"
}

func text(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return ""
	}
	return b.String()
}

func attr(s string) string {
	return `"` + text(s) + `"`
}
