// Package tbx implements parsing and exporting of TBX (TermBase eXchange,
// ISO 30042) terminology files.
//
// TBX is the terminology sibling of TMX (see internal/tmx). This package
// mirrors that one's shape: streaming token-based parse for large files, and a
// streaming header/entries/footer writer for export.
//
// Two dialects are supported on IMPORT (real-world exports vary):
//
//   - TBX v3 / TBX-Basic (ISO 30042:2019), DCA style — root <tbx>,
//     <conceptEntry> → <langSec xml:lang> → <termSec> → <term>. Metadata via
//     <termNote type="…"> and <descrip type="…">.
//   - TBX v2 (ISO 30042:2008), DCA style — root <martif>, <termEntry> →
//     <langSet xml:lang> → <tig>/<ntig> → <term>.
//
// EXPORT always emits TBX-Basic v3 (DCA), the most widely accepted form.
//
// Two parse entry points:
//   - ParseConcepts → full multilingual Concepts (all languages, synonyms,
//     per-term status) for the concept-native import path.
//   - ParseEntries → flattened source→target Entry pairs for the bilingual
//     CSV/legacy path (cross-product of source × target synonyms).
//
// The structural decoders are dialect-agnostic; a data-category MAPPING REGISTRY
// (resolveDomain / resolveStatus / normalizeStatus / contentNoteTypes, near the
// top) is the single extensible point that teaches the parser a new dialect's or
// tool's naming. Type matching is case-insensitive, so a tool's capitalization
// (Kaleidoscope/quickTerm exports use "Domain", "Usage", "Definition") just
// works. Dialects handled beyond the ISO-canonical names:
//   - XTM: <termNote type="status">FORBIDDEN, <descrip type="domain">.
//   - Kaleidoscope/quickTerm (XTM export): term status via <termNote type="Usage">
//     (Preferred/Admitted → approved, "Do not use" → forbidden), with a langSet
//     <descrip type="Validated"> (Yes → approved, else pending) as the fallback;
//     domain via <descrip type="Domain">; Definition/Example/Note/… folded into
//     term notes. (Part of speech / "Grammar" is intentionally not imported.)
//
// Add an alias in the registry; no structural change.
package tbx

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"openloc/internal/langcode"
)

// Term statuses this package emits. These are the subset of the terminology
// package's valid statuses that TBX administrativeStatus maps onto.
const (
	statusApproved  = "approved"
	statusForbidden = "forbidden"
	statusPending   = "pending" // unrecognized administrative status → needs review
)

// Entry is a flattened source→target term pair ready for DB insertion.
// Fields line up 1:1 with the terminology_entries columns the importer writes.
type Entry struct {
	SourceTerm string
	TargetTerm string
	Status     string // "approved", "forbidden" or "pending"
	Notes      string
	Domain     string
}

// --- XML structures for unmarshalling (both dialects) ---

// xmlConcept captures a single <conceptEntry> (v3) or <termEntry> (v2). The two
// dialects use different child element names for language sections, so we
// capture both and normalize in toParsed.
type xmlConcept struct {
	LangSecs    []xmlLangSec    `xml:"langSec"`    // v3
	LangSets    []xmlLangSec    `xml:"langSet"`    // v2 (same shape after unmarshal)
	Descrips    []xmlDescrip    `xml:"descrip"`    // concept-level (e.g. subjectField)
	DescripGrps []xmlDescripGrp `xml:"descripGrp"` // descrips wrapped in <descripGrp> (e.g. Microsoft)
}

// xmlLangSec captures a <langSec> (v3) or <langSet> (v2). termSec is v3; tig and
// ntig are v2 term-information groups. Both are decoded into xmlTermSec.
type xmlLangSec struct {
	Lang        string          `xml:"http://www.w3.org/XML/1998/namespace lang,attr"`
	TermSecs    []xmlTermSec    `xml:"termSec"`    // v3
	Tigs        []xmlTermSec    `xml:"tig"`        // v2
	Ntigs       []xmlTermSec    `xml:"ntig"`       // v2 (nested)
	Descrips    []xmlDescrip    `xml:"descrip"`    // lang-level (e.g. definition)
	DescripGrps []xmlDescripGrp `xml:"descripGrp"` // lang-level descrips wrapped in <descripGrp>
}

// xmlTermSec captures a <termSec>/<tig>/<ntig>. In v2 <ntig> the term lives
// inside a nested <termGrp>, so we capture that too and merge in effectiveTerm.
type xmlTermSec struct {
	Term        string          `xml:"term"`
	TermNotes   []xmlTermNote   `xml:"termNote"`
	Descrips    []xmlDescrip    `xml:"descrip"`
	DescripGrps []xmlDescripGrp `xml:"descripGrp"` // term-level descrips wrapped in <descripGrp>
	Notes       []string        `xml:"note"`
	TermGrp     *xmlTermGrp     `xml:"termGrp"` // v2 <ntig> nesting
}

type xmlTermGrp struct {
	Term      string        `xml:"term"`
	TermNotes []xmlTermNote `xml:"termNote"`
}

type xmlTermNote struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type xmlDescrip struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

// xmlDescripGrp is a <descripGrp> wrapper. TBX lets a <descrip> be wrapped in
// one (with source/admin siblings); Microsoft's export puts the definition
// there. flattenDescrips merges these back with the direct <descrip> children.
type xmlDescripGrp struct {
	Descrips []xmlDescrip `xml:"descrip"`
}

// flattenDescrips merges direct <descrip> children with those inside any
// <descripGrp> wrappers, so callers see one list regardless of nesting.
func flattenDescrips(direct []xmlDescrip, grps []xmlDescripGrp) []xmlDescrip {
	if len(grps) == 0 {
		return direct
	}
	out := append([]xmlDescrip(nil), direct...)
	for _, g := range grps {
		out = append(out, g.Descrips...)
	}
	return out
}

// --- Normalized intermediate (dialect-agnostic) ---

type parsedTerm struct {
	term           string
	status         string
	statusExplicit bool   // an explicit status note was present (vs. defaulted)
	pos            string // part of speech (kept separate; ParseEntries folds it into notes)
	notes          []string
	domain         string
}

// --- Data-category mapping registry (the dialect-extensible point) ---
//
// TBX v3 is modular: a fixed TBX-Core structure plus data-category modules, and
// tools label the same category differently (ISO/TerminOrgs names vs XTM/memoQ
// aliases). The structural decoders above are dialect-agnostic; this registry is
// where a new dialect/tool is taught. Add an alias here — no structural change.
var (
	// descrip type → domain
	domainDescripTypes = []string{"subjectField", "domain"}
	// termNote type → administrative status. "usage" is the Kaleidoscope/XTM
	// term-level status field (Preferred / Admitted / Do not use).
	statusNoteTypes = []string{"administrativeStatus", "status", "normativeAuthorization", "usage"}
	// termNote types folded (labeled) into a term's notes — the content-bearing
	// categories worth keeping. Order fixes the rendered note order. Pure
	// provenance/metadata (Origin, Validator name, tips, Project ID, …) is dropped.
	contentNoteTypes = []struct{ typ, label string }{
		{"definition", "Definition"},
		{"context", "Context"},
		{"example", "Example"},
		{"note", "Note"},
		{"description", "Description"},
		{"see also", "See also"},
	}
)

// resolveDomain returns the first present domain data-category value.
func resolveDomain(descrips []xmlDescrip) string {
	for _, typ := range domainDescripTypes {
		if v := firstDescrip(descrips, typ); v != "" {
			return v
		}
	}
	return ""
}

// resolveStatus maps a term's administrative status onto our status enum,
// tolerant of dialect naming. The bool reports whether an explicit status note
// was found; when false the caller may fall back to a langSet-level signal (e.g.
// Kaleidoscope's <descrip type="Validated">) before defaulting.
//
// Default (no note, no fallback) → approved: TBX marks forbidden terms
// explicitly (deprecatedTerm/supersededTerm/FORBIDDEN/Do not use), so an unmarked
// term is a normal usable glossary term.
func resolveStatus(notes []xmlTermNote) (string, bool) {
	for _, typ := range statusNoteTypes {
		if v := firstTermNote(notes, typ); v != "" {
			return normalizeStatus(v), true
		}
	}
	return statusApproved, false
}

// mapValidated maps a langSet-level <descrip type="Validated"> (Kaleidoscope/XTM)
// onto status, used only when a term has no explicit status note. Yes → approved;
// anything else (No, In progress, …) → pending — not-yet-validated is not the
// same as banned, so it imports inert for review rather than enforced.
func mapValidated(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), "yes") {
		return statusApproved
	}
	return statusPending
}

// normalizeStatus maps a status VALUE (any dialect) onto our status enum.
// Recognized-forbidden → forbidden, recognized-approved → approved, and an
// UNRECOGNIZED value → pending: we can't interpret it, so don't guess "approved"
// (which would silently enforce or wave through a term we didn't understand) —
// import it inert, for a human to review.
func normalizeStatus(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.HasPrefix(v, "deprecated"),
		strings.HasPrefix(v, "superseded"),
		strings.Contains(v, "forbidden"),
		strings.Contains(v, "prohibited"),
		strings.Contains(v, "obsolete"),
		strings.Contains(v, "do not use"): // Kaleidoscope/XTM Usage=Do not use
		return statusForbidden
	case strings.HasPrefix(v, "preferred"),
		strings.HasPrefix(v, "admitted"),
		strings.HasPrefix(v, "valid"):
		return statusApproved
	default:
		return statusPending // unrecognized administrative status — needs review
	}
}

type parsedLang struct {
	lang  string
	terms []parsedTerm
}

// detectAndWrapEncoding peeks at the first bytes of r to detect a UTF-16 BOM and
// wraps the reader with a UTF-16→UTF-8 decoder if found. Otherwise assumes UTF-8.
// (Same approach as internal/tmx.)
func detectAndWrapEncoding(r io.Reader) io.Reader {
	br := bufio.NewReaderSize(r, 4)
	peek, err := br.Peek(2)
	if err != nil || len(peek) < 2 {
		return br
	}
	if peek[0] == 0xFF && peek[1] == 0xFE { // UTF-16 LE
		return transform.NewReader(br, unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder())
	}
	if peek[0] == 0xFE && peek[1] == 0xFF { // UTF-16 BE
		return transform.NewReader(br, unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewDecoder())
	}
	return br
}

// UnsupportedError signals a file we can identify but can't import: not a TBX
// document, or a TBX style we don't handle (DCT). Msg is written for the end user
// and is safe to surface directly; Root/Type/Style carry the detected dialect so
// callers can report which unsupported type was attempted (none is customer
// content — all are structural attributes).
type UnsupportedError struct {
	Msg   string
	Root  string // root element (tbx / martif / something else)
	Type  string // the `type` attribute (TBX-Basic, TBX, …) if present
	Style string // the `style` attribute (dca / dct) if present
}

func (e *UnsupportedError) Error() string { return e.Msg }

// validateRoot checks the root element (the first StartElement) is a TBX root we
// support. TBX v3 (<tbx>) and v2 (<martif>) in the DCA style (Data Category as
// Attribute) are supported. DCT style (Data Category as Tag) expresses metadata
// as namespaced elements this parser doesn't read, so it's rejected explicitly
// rather than importing terms with all their status/domain silently dropped.
func validateRoot(start xml.StartElement) error {
	root := start.Name.Local
	var typ, style string
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "type":
			typ = a.Value
		case "style":
			style = a.Value
		}
	}
	if root != "tbx" && root != "martif" {
		return &UnsupportedError{
			Root: root, Type: typ, Style: style,
			Msg: fmt.Sprintf("This doesn't look like a TBX file — its root element is <%s>, but a TBX document starts with <tbx> or <martif>.", root),
		}
	}
	if style != "" && !strings.EqualFold(style, "dca") {
		return &UnsupportedError{
			Root: root, Type: typ, Style: style,
			Msg: fmt.Sprintf("TBX style %q is not supported — please export as DCA-style (Data Category as Attribute) TBX-Basic.", style),
		}
	}
	return nil
}

// Report describes what a parse observed beyond the returned entries. It lets
// callers explain a zero-result import (e.g. "the file's languages don't match
// this term base") instead of silently reporting "0 imported".
type Report struct {
	Concepts        int      // conceptEntry/termEntry elements seen
	Malformed       int      // concepts skipped because they failed to decode
	LanguagesSeen   []string // distinct normalized xml:lang values across the file (sorted)
	MatchedConcepts int      // concepts that produced at least one entry
}

// ParseEntries streams a TBX file and returns source→target Entry pairs for the
// given language pair, without loading the whole tree into memory. Concepts
// missing either language are skipped. The first source-language term is paired
// with each target-language term (so target synonyms become separate rows).
// Supports UTF-8 and UTF-16 (LE/BE with BOM).
//
// The returned Report surfaces why a parse may have produced few/no entries
// (languages present, malformed concepts) so callers can give an actionable
// error rather than a silent empty import.
func ParseEntries(r io.Reader, sourceLang, targetLang string) ([]Entry, Report, error) {
	r = detectAndWrapEncoding(r)
	dec := xml.NewDecoder(r)
	// detectAndWrapEncoding already converted UTF-16→UTF-8, so any declared
	// charset is passed through unchanged.
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }

	srcNorm := langcode.Normalize(sourceLang)
	tgtNorm := langcode.Normalize(targetLang)
	srcBase := langcode.BaseLang(srcNorm)
	tgtBase := langcode.BaseLang(tgtNorm)

	var entries []Entry
	var report Report
	seenLangs := make(map[string]struct{})
	sawRoot := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, report, fmt.Errorf("tbx: stream XML: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !sawRoot {
			sawRoot = true
			if err := validateRoot(start); err != nil {
				return nil, report, err
			}
		}
		// v3 uses <conceptEntry>, v2 uses <termEntry>.
		if start.Name.Local != "conceptEntry" && start.Name.Local != "termEntry" {
			continue
		}
		report.Concepts++

		var raw xmlConcept
		if err := dec.DecodeElement(&raw, &start); err != nil {
			report.Malformed++
			continue // skip malformed concepts
		}

		langs, conceptDomain, _ := toParsed(raw)
		for _, l := range langs {
			if n := langcode.Normalize(l.lang); n != "" {
				seenLangs[n] = struct{}{}
			}
		}
		src := pickLang(langs, srcNorm, srcBase)
		tgt := pickLang(langs, tgtNorm, tgtBase)
		if src == nil || tgt == nil {
			continue
		}

		// Cross-product: every source synonym × every target synonym.
		matched := false
		for _, s := range src.terms {
			if s.term == "" {
				continue
			}
			for _, t := range tgt.terms {
				if t.term == "" {
					continue
				}
				domain := t.domain
				if domain == "" {
					domain = conceptDomain
				}
				entries = append(entries, Entry{
					SourceTerm: s.term,
					TargetTerm: t.term,
					Status:     t.status,
					Notes:      joinNotes(t.pos, t.notes),
					Domain:     domain,
				})
				matched = true
			}
		}
		if matched {
			report.MatchedConcepts++
		}
	}

	report.LanguagesSeen = make([]string, 0, len(seenLangs))
	for l := range seenLangs {
		report.LanguagesSeen = append(report.LanguagesSeen, l)
	}
	sort.Strings(report.LanguagesSeen)

	return entries, report, nil
}

// toParsed converts a decoded concept (either dialect) into normalized language
// sections plus the concept-level domain and definition (dialect-agnostic via the
// mapping registry).
func toParsed(c xmlConcept) (langs []parsedLang, conceptDomain, conceptDefinition string) {
	conceptDescrips := flattenDescrips(c.Descrips, c.DescripGrps)
	conceptDomain = resolveDomain(conceptDescrips)
	conceptDefinition = firstDescrip(conceptDescrips, "definition")

	sections := c.LangSecs
	if len(sections) == 0 {
		sections = c.LangSets // v2
	}
	var firstLangDefn string // a language-level definition (e.g. Microsoft's en-US descripGrp)
	for _, sec := range sections {
		pl := parsedLang{lang: sec.Lang}
		secDescrips := flattenDescrips(sec.Descrips, sec.DescripGrps)
		if firstLangDefn == "" {
			firstLangDefn = firstDescrip(secDescrips, "definition")
		}
		langDomain := resolveDomain(secDescrips)
		// Kaleidoscope/XTM put validation state at the langSet level; it's the
		// status fallback for terms with no explicit status note.
		langValidated := firstDescrip(secDescrips, "validated")

		termSecs := sec.TermSecs
		termSecs = append(termSecs, sec.Tigs...)
		termSecs = append(termSecs, sec.Ntigs...)
		for _, ts := range termSecs {
			pt := parseTermSec(ts)
			if pt.domain == "" {
				pt.domain = langDomain
			}
			if !pt.statusExplicit && langValidated != "" {
				pt.status = mapValidated(langValidated)
			}
			pl.terms = append(pl.terms, pt)
		}
		langs = append(langs, pl)
	}
	// A concept with no concept-level definition inherits a language-level one
	// (Microsoft puts the definition on the source language section).
	if conceptDefinition == "" {
		conceptDefinition = firstLangDefn
	}
	return langs, conceptDomain, conceptDefinition
}

// parseTermSec extracts term text, status, notes, and domain from a single
// term-information group, merging v2 <ntig><termGrp> nesting.
func parseTermSec(ts xmlTermSec) parsedTerm {
	term := strings.TrimSpace(ts.Term)
	termNotes := ts.TermNotes
	if term == "" && ts.TermGrp != nil {
		term = strings.TrimSpace(ts.TermGrp.Term)
		termNotes = append(termNotes, ts.TermGrp.TermNotes...)
	}

	descrips := flattenDescrips(ts.Descrips, ts.DescripGrps)
	status, explicit := resolveStatus(termNotes)
	pt := parsedTerm{
		term:           term,
		status:         status,
		statusExplicit: explicit,
		pos:            firstTermNote(termNotes, "partOfSpeech"),
		domain:         resolveDomain(descrips),
	}
	// notes: term-level definition/context descrips, remarks termNote, <note>.
	if def := firstDescrip(descrips, "definition"); def != "" {
		pt.notes = append(pt.notes, def)
	}
	if ctx := firstDescrip(descrips, "context"); ctx != "" {
		pt.notes = append(pt.notes, ctx)
	}
	if rem := firstTermNote(termNotes, "remarks"); rem != "" {
		pt.notes = append(pt.notes, rem)
	}
	// Content-bearing termNotes (Kaleidoscope/XTM: Definition, Example, Note, …),
	// labeled so the merged notes string stays legible.
	for _, cn := range contentNoteTypes {
		if v := firstTermNote(termNotes, cn.typ); v != "" {
			pt.notes = append(pt.notes, cn.label+": "+v)
		}
	}
	for _, n := range ts.Notes {
		if n = strings.TrimSpace(n); n != "" {
			pt.notes = append(pt.notes, n)
		}
	}
	return pt
}

// pickLang selects the language section matching want. It prefers an exact
// normalized match (e.g. "pt-br" == "pt-br") and falls back to a primary-subtag
// match (e.g. "en" matches "en-US") so region variants still line up.
func pickLang(langs []parsedLang, wantNorm, wantBase string) *parsedLang {
	var baseMatch *parsedLang
	for i := range langs {
		n := langcode.Normalize(langs[i].lang)
		if n == wantNorm {
			return &langs[i]
		}
		if baseMatch == nil && langcode.BaseLang(n) == wantBase {
			baseMatch = &langs[i]
		}
	}
	return baseMatch
}

// firstTermNote / firstDescrip match the data-category type case-INsensitively:
// ISO uses camelCase (subjectField, administrativeStatus) but real exports vary
// the casing (Kaleidoscope/XTM: "Domain", "Usage", "Definition"). Our registry
// entries stay in canonical case; the fold here lets any casing match.
func firstTermNote(notes []xmlTermNote, typ string) string {
	for _, n := range notes {
		if strings.EqualFold(n.Type, typ) {
			return strings.TrimSpace(n.Value)
		}
	}
	return ""
}

func firstDescrip(descrips []xmlDescrip, typ string) string {
	for _, d := range descrips {
		if strings.EqualFold(d.Type, typ) {
			return strings.TrimSpace(d.Value)
		}
	}
	return ""
}

func dedupeNonEmpty(vals []string) []string {
	seen := make(map[string]struct{}, len(vals))
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// joinNotes renders the flat-model notes string: part of speech (as "POS: x")
// prepended, then the collected notes, deduped and "; "-joined. Used by the
// bilingual ParseEntries path where there's no separate POS column.
func joinNotes(pos string, notes []string) string {
	all := notes
	if pos != "" {
		all = append([]string{"POS: " + pos}, notes...)
	}
	return strings.Join(dedupeNonEmpty(all), "; ")
}

// --- Concept-aware parsing (multilingual import) ---

// Concept is a fully parsed TBX concept: shared metadata plus every term in
// every language, with per-term status/POS/notes preserved. Maps 1:1 to a
// term_concept + its term rows.
type Concept struct {
	Definition string
	Domain     string
	Terms      []ConceptTerm
}

// ConceptTerm is one term within a concept, in one language.
type ConceptTerm struct {
	Language     string
	Text         string
	Status       string // approved | forbidden | pending
	PartOfSpeech string
	Notes        string
}

// ParseConcepts streams a TBX file into full multilingual concepts (all
// languages, all synonyms, per-term status) — the concept-native import path.
// Unlike ParseEntries it does not flatten to a language pair. Supports both
// dialects (v2 martif, v3 tbx) and UTF-8/UTF-16.
func ParseConcepts(r io.Reader) ([]Concept, Report, error) {
	r = detectAndWrapEncoding(r)
	dec := xml.NewDecoder(r)
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }

	var concepts []Concept
	var report Report
	seenLangs := make(map[string]struct{})
	sawRoot := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, report, fmt.Errorf("tbx: stream XML: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !sawRoot {
			sawRoot = true
			if err := validateRoot(start); err != nil {
				return nil, report, err
			}
		}
		if start.Name.Local != "conceptEntry" && start.Name.Local != "termEntry" {
			continue
		}
		report.Concepts++

		var raw xmlConcept
		if err := dec.DecodeElement(&raw, &start); err != nil {
			report.Malformed++
			continue
		}

		langs, conceptDomain, conceptDefinition := toParsed(raw)
		c := Concept{Domain: conceptDomain, Definition: conceptDefinition}
		for _, l := range langs {
			lang := langcode.Normalize(l.lang)
			if lang != "" {
				seenLangs[lang] = struct{}{}
			}
			for _, t := range l.terms {
				if t.term == "" {
					continue
				}
				domain := t.domain
				if domain != "" && c.Domain == "" {
					c.Domain = domain // fall back to a term/lang-level domain
				}
				c.Terms = append(c.Terms, ConceptTerm{
					Language:     lang,
					Text:         t.term,
					Status:       t.status,
					PartOfSpeech: t.pos,
					Notes:        strings.Join(dedupeNonEmpty(t.notes), "; "),
				})
			}
		}
		if len(c.Terms) > 0 {
			concepts = append(concepts, c)
			report.MatchedConcepts++
		}
	}

	report.LanguagesSeen = make([]string, 0, len(seenLangs))
	for l := range seenLangs {
		report.LanguagesSeen = append(report.LanguagesSeen, l)
	}
	sort.Strings(report.LanguagesSeen)
	return concepts, report, nil
}

// --- Export (TBX-Basic v3, DCA) ---

// Export writes term entries as a valid TBX-Basic v3 document.
// Convenience wrapper around WriteHeader / WriteEntries / WriteFooter.
func Export(w io.Writer, sourceLang, targetLang string, entries []Entry) error {
	if err := WriteHeader(w, sourceLang); err != nil {
		return err
	}
	if err := WriteEntries(w, sourceLang, targetLang, entries, 0); err != nil {
		return err
	}
	return WriteFooter(w)
}

// WriteHeader writes the XML declaration, <tbx> root, TBX header, and opening
// <text><body> tags.
func WriteHeader(w io.Writer, sourceLang string) error {
	_, err := fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<tbx style="dca" type="TBX-Basic" xml:lang="%s" xmlns="urn:iso:std:iso:30042:ed-2">
  <tbxHeader>
    <fileDesc>
      <sourceDesc>
        <p>Exported from OpenLoc</p>
      </sourceDesc>
    </fileDesc>
  </tbxHeader>
  <text>
    <body>
`, xmlEscape(langcode.Normalize(sourceLang)))
	return err
}

// WriteEntries writes a batch of entries as <conceptEntry> elements. startIndex
// is the number of concepts already written, so ids stay unique and sequential
// across streamed batches.
func WriteEntries(w io.Writer, sourceLang, targetLang string, entries []Entry, startIndex int) error {
	src := xmlEscape(langcode.Normalize(sourceLang))
	tgt := xmlEscape(langcode.Normalize(targetLang))
	for i, e := range entries {
		id := startIndex + i + 1
		if _, err := fmt.Fprintf(w, "      <conceptEntry id=\"c%d\">\n", id); err != nil {
			return err
		}
		// Source language section — just the term.
		if _, err := fmt.Fprintf(w,
			"        <langSec xml:lang=\"%s\">\n          <termSec>\n            <term>%s</term>\n          </termSec>\n        </langSec>\n",
			src, xmlEscape(e.SourceTerm)); err != nil {
			return err
		}
		// Target language section — term plus mapped metadata.
		if _, err := fmt.Fprintf(w,
			"        <langSec xml:lang=\"%s\">\n          <termSec>\n            <term>%s</term>\n",
			tgt, xmlEscape(e.TargetTerm)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w,
			"            <termNote type=\"administrativeStatus\">%s</termNote>\n", adminStatusFor(e.Status)); err != nil {
			return err
		}
		if e.Domain != "" {
			if _, err := fmt.Fprintf(w,
				"            <descrip type=\"subjectField\">%s</descrip>\n", xmlEscape(e.Domain)); err != nil {
				return err
			}
		}
		if e.Notes != "" {
			if _, err := fmt.Fprintf(w, "            <note>%s</note>\n", xmlEscape(e.Notes)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "          </termSec>\n        </langSec>\n      </conceptEntry>\n"); err != nil {
			return err
		}
	}
	return nil
}

// WriteConceptEntries writes full multilingual concepts as <conceptEntry>
// elements — one per concept, with all its languages and per-term status/POS/
// notes. This is the concept-native export (vs the bilingual WriteEntries).
func WriteConceptEntries(w io.Writer, concepts []Concept, startIndex int) error {
	var werr error
	p := func(format string, a ...any) {
		if werr == nil {
			_, werr = fmt.Fprintf(w, format, a...)
		}
	}
	for i, c := range concepts {
		p("      <conceptEntry id=\"c%d\">\n", startIndex+i+1)
		if c.Domain != "" {
			p("        <descrip type=\"subjectField\">%s</descrip>\n", xmlEscape(c.Domain))
		}
		if c.Definition != "" {
			p("        <descrip type=\"definition\">%s</descrip>\n", xmlEscape(c.Definition))
		}
		// Group terms by language, preserving first-seen order.
		langOrder := make([]string, 0)
		byLang := make(map[string][]ConceptTerm)
		for _, t := range c.Terms {
			if _, ok := byLang[t.Language]; !ok {
				langOrder = append(langOrder, t.Language)
			}
			byLang[t.Language] = append(byLang[t.Language], t)
		}
		for _, lang := range langOrder {
			p("        <langSec xml:lang=\"%s\">\n", xmlEscape(langcode.Normalize(lang)))
			for _, t := range byLang[lang] {
				p("          <termSec>\n            <term>%s</term>\n", xmlEscape(t.Text))
				p("            <termNote type=\"administrativeStatus\">%s</termNote>\n", adminStatusFor(t.Status))
				if t.PartOfSpeech != "" {
					p("            <termNote type=\"partOfSpeech\">%s</termNote>\n", xmlEscape(t.PartOfSpeech))
				}
				if t.Notes != "" {
					p("            <note>%s</note>\n", xmlEscape(t.Notes))
				}
				p("          </termSec>\n")
			}
			p("        </langSec>\n")
		}
		p("      </conceptEntry>\n")
	}
	return werr
}

// WriteFooter writes the closing </body></text></tbx> tags.
func WriteFooter(w io.Writer) error {
	_, err := fmt.Fprint(w, "    </body>\n  </text>\n</tbx>\n")
	return err
}

// adminStatusFor maps our status column onto a TBX administrativeStatus value.
// Anything but forbidden exports as preferredTerm; "pending" has no standard
// TBX-Basic equivalent, so it re-imports as "approved".
func adminStatusFor(status string) string {
	if status == statusForbidden {
		return "deprecatedTerm-admn-sts"
	}
	return "preferredTerm-admn-sts"
}

// xmlEscape escapes a string for use in XML text and attribute values.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
