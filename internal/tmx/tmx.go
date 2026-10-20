// Package tmx implements parsing and exporting of TMX 1.4b
// (Translation Memory eXchange) files.
package tmx

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"openloc/internal/langcode"
)

// File represents a parsed TMX document.
type File struct {
	Header Header
	Units  []TranslationUnit
}

// Header contains TMX file metadata.
type Header struct {
	CreationTool string
	DataType     string
	SegType      string
	AdminLang    string
	SrcLang      string // source language for the TM
}

// TranslationUnit represents a single <tu> element.
type TranslationUnit struct {
	ID           string // tuid attribute (optional)
	CreationDate string // creationdate attribute (ISO 8601)
	Creator      string // creationid attribute
	ChangeDate   string // changedate attribute
	ChangeUser   string // changeid attribute
	Variants     []Variant
}

// Variant represents a single <tuv> element — one language's text.
type Variant struct {
	Lang     string // lowercased xml:lang (e.g. "en-us", "pt-br")
	Text     string // segment text with inline XLIFF tags STRIPPED (used for matching/dedup)
	RichText string // raw <seg> InnerXML preserving inline tags; "" when there are no tags
}

// Entry is a flattened source→target pair ready for DB insertion.
//
// Plain vs rich text contract:
//   - SourceText / TargetText: plain (inline XLIFF tags STRIPPED). Used for
//     match scoring, dedup hashing, ILIKE search. Markup must NOT influence
//     these — that's why they're stripped on parse.
//   - SourceRichText / TargetRichText: tag-bearing form (raw <seg> InnerXML
//     with <bpt>, <ept>, <ph>, etc. preserved). Used for DISPLAY ONLY in
//     TM browser + Concordance modal. Empty when the segment has no inline
//     tags — caller should store NULL in that case (NULLIF in SQL).
type Entry struct {
	SourceLang     string
	TargetLang     string
	SourceText     string // plain — tags stripped
	TargetText     string // plain — tags stripped
	SourceRichText string // raw InnerXML with tags, "" if no tags
	TargetRichText string // raw InnerXML with tags, "" if no tags
	TUId           string // tuid attribute from <tu>
	CreationDate   string // creationdate attribute (ISO 8601)
	Creator        string // creationid attribute
	ChangeDate     string // changedate attribute
	ChangeUser     string // changeid attribute
}

// XLIFF 1.2 inline-tag set per §4.2: bpt/ept paired, ph standalone, it isolated,
// x generic placeholder, g grouping, hi highlight, sub sub-flow.
//
// Go's RE2 lacks backreferences so we can't write `<(name)>...</\1>` in one
// pattern. Two passes instead:
//
//	Pass 1 (xliffPairedRE): drops <bpt>…</bpt>, <ept>…</ept>, <ph>…</ph>,
//	                        <it>…</it>, <g>…</g>, <hi>…</hi>, <sub>…</sub>
//	                        — content inside (e.g. displaytext metadata) is
//	                        also dropped, matching what a translator typing
//	                        the same source segment without tags would write.
//	Pass 2 (xliffAnyTagRE): cleans up self-closing <ph/> / <x/> and any stray
//	                        open/close tags that survived (malformed input).
var (
	xliffPairedRE = regexp.MustCompile(`<bpt\b[^>]*>.*?</bpt>|<ept\b[^>]*>.*?</ept>|<ph\b[^>]*>.*?</ph>|<it\b[^>]*>.*?</it>|<g\b[^>]*>.*?</g>|<hi\b[^>]*>.*?</hi>|<sub\b[^>]*>.*?</sub>`)
	xliffAnyTagRE = regexp.MustCompile(`</?(?:bpt|ept|ph|it|x|g|hi|sub)\b[^>]*/?>`)
)

// stripInlineTags returns the plain-text form of a TMX <seg> InnerXML by
// removing all XLIFF inline tags AND the content inside paired tags. Used at
// IMPORT time to feed the plain source_text/target_text columns (which drive
// matching, dedup hashing, and ILIKE search — markup must not influence
// these). The rich form is preserved separately on Entry.SourceRichText/
// TargetRichText for display purposes.
//
// memoQ-style nested-XLIFF ("<bpt i=1>&lt;mq:rxt displaytext=&quot;…") is
// handled correctly: the entire `<bpt>…</bpt>` block is dropped,
// including the entity-escaped junk inside. The rich form preserves the
// original markup so the display layer's parser can render it as a chip.
func stripInlineTags(s string) string {
	s = xliffPairedRE.ReplaceAllString(s, "")
	s = xliffAnyTagRE.ReplaceAllString(s, "")
	return s
}

// segText is the plain text of a <seg>'s inner XML: the inline tags dropped
// and the character data decoded ("Tom &amp; Jerry" → "Tom & Jerry").
func segText(raw string) string {
	return html.UnescapeString(stripInlineTags(raw))
}

// hasInlineTags reports whether s contains any XLIFF inline tag. Used to
// decide whether to populate Source/TargetRichText vs leaving it "" → NULL.
func hasInlineTags(s string) bool {
	return xliffAnyTagRE.MatchString(s)
}

// segContentForExport picks the right form to write inside <seg>...</seg>:
//   - The rich text when it is set (TMX codes from an import, XLIFF chips
//     and editor formatting from a confirm), as tmxSegMarkup writes it.
//   - Else the plain text when it holds inline tags (rows where the raw inner
//     XML landed in source_text/target_text), the same way.
//   - Else, and whenever tmxSegMarkup cannot write the markup, the escaped
//     plain text.
func segContentForExport(plain, rich string) string {
	if rich == "" && hasInlineTags(plain) {
		rich = plain
	}
	if rich != "" {
		if m, ok := tmxSegMarkup(rich); ok {
			return m
		}
	}
	return xmlEscape(plain)
}

// --- XML structures for unmarshalling ---

type xmlTMX struct {
	XMLName xml.Name  `xml:"tmx"`
	Header  xmlHeader `xml:"header"`
	Body    xmlBody   `xml:"body"`
}

type xmlHeader struct {
	CreationTool string `xml:"creationtool,attr"`
	DataType     string `xml:"datatype,attr"`
	SegType      string `xml:"segtype,attr"`
	AdminLang    string `xml:"adminlang,attr"`
	SrcLang      string `xml:"srclang,attr"`
}

type xmlBody struct {
	Units []xmlTU `xml:"tu"`
}

type xmlTU struct {
	ID           string   `xml:"tuid,attr"`
	CreationDate string   `xml:"creationdate,attr"`
	Creator      string   `xml:"creationid,attr"`
	ChangeDate   string   `xml:"changedate,attr"`
	ChangeUser   string   `xml:"changeid,attr"`
	Variants     []xmlTUV `xml:"tuv"`
}

type xmlTUV struct {
	Lang string `xml:"http://www.w3.org/XML/1998/namespace lang,attr"`
	Seg  xmlSeg `xml:"seg"`
}

// xmlSeg captures the raw inner XML of <seg> to preserve inline markup.
type xmlSeg struct {
	InnerXML string `xml:",innerxml"`
}

// detectAndWrapEncoding peeks at the first bytes of r to detect UTF-16 BOM.
// If a UTF-16 LE or BE BOM is found, the reader is wrapped with a UTF-16→UTF-8 decoder.
// Otherwise, the reader is returned as-is (assumes UTF-8).
func detectAndWrapEncoding(r io.Reader) io.Reader {
	br := bufio.NewReaderSize(r, 4)
	peek, err := br.Peek(2)
	if err != nil || len(peek) < 2 {
		return br
	}

	// UTF-16 LE BOM: 0xFF 0xFE
	if peek[0] == 0xFF && peek[1] == 0xFE {
		return transform.NewReader(br, unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder())
	}
	// UTF-16 BE BOM: 0xFE 0xFF
	if peek[0] == 0xFE && peek[1] == 0xFF {
		return transform.NewReader(br, unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewDecoder())
	}

	return br
}

// Parse reads a TMX document and returns a structured File.
// Inline markup within <seg> elements is preserved as-is.
// Language codes are normalized to canonical BCP 47 (see langcode.Normalize).
// Supports UTF-8 and UTF-16 (LE/BE with BOM) encoded files.
func Parse(r io.Reader) (*File, error) {
	r = detectAndWrapEncoding(r)

	dec := xml.NewDecoder(r)
	// Handle non-UTF-8 encoding declarations (e.g. encoding="UTF-16").
	// Since detectAndWrapEncoding already converts UTF-16→UTF-8, the
	// CharsetReader just returns the reader as-is for any declared encoding.
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		return input, nil
	}

	var raw xmlTMX
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("tmx: decode XML: %w", err)
	}

	f := &File{
		Header: Header{
			CreationTool: raw.Header.CreationTool,
			DataType:     raw.Header.DataType,
			SegType:      raw.Header.SegType,
			AdminLang:    langcode.Normalize(raw.Header.AdminLang),
			SrcLang:      langcode.Normalize(raw.Header.SrcLang),
		},
	}

	for _, tu := range raw.Body.Units {
		unit := TranslationUnit{
			ID:           tu.ID,
			CreationDate: tu.CreationDate,
			Creator:      tu.Creator,
			ChangeDate:   tu.ChangeDate,
			ChangeUser:   tu.ChangeUser,
		}
		for _, tuv := range tu.Variants {
			raw := strings.TrimSpace(tuv.Seg.InnerXML)
			if raw == "" {
				continue
			}
			rich := ""
			if hasInlineTags(raw) {
				rich = raw
			}
			unit.Variants = append(unit.Variants, Variant{
				Lang:     langcode.Normalize(tuv.Lang),
				Text:     segText(raw),
				RichText: rich,
			})
		}
		if len(unit.Variants) > 0 {
			f.Units = append(f.Units, unit)
		}
	}

	return f, nil
}

// ParseEntries returns all source→target Entry pairs of a TMX file. It holds
// every entry in memory; StreamEntries is the bounded-memory form.
func ParseEntries(r io.Reader) ([]Entry, error) {
	var entries []Entry
	err := StreamEntries(r, 500, func(batch []Entry) error {
		entries = append(entries, batch...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// StreamEntries reads a TMX file one <tu> at a time and calls fn with the
// source→target pairs in batches of up to batchSize, so memory stays bounded
// whatever the file size. It reads the <header> for srclang first. fn must
// not keep the slice after it returns. A malformed <tu> is skipped; broken
// XML stops the stream with an error. An error from fn stops it too and is
// returned as is.
func StreamEntries(r io.Reader, batchSize int, fn func([]Entry) error) error {
	r = detectAndWrapEncoding(r)
	dec := xml.NewDecoder(r)
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		return input, nil
	}

	batchSize = max(batchSize, 1)
	var srcLang string
	batch := make([]Entry, 0, batchSize)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// The entries read before the break are whole; deliver them.
			if len(batch) > 0 {
				if ferr := fn(batch); ferr != nil {
					return ferr
				}
			}
			return fmt.Errorf("tmx: stream XML: %w", err)
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		switch start.Name.Local {
		case "header":
			// Extract srclang from header attributes
			for _, a := range start.Attr {
				if a.Name.Local == "srclang" {
					srcLang = langcode.Normalize(a.Value)
				}
			}
			dec.Skip() // skip header content

		case "tu":
			// Decode one <tu> at a time
			var tu xmlTU
			if err := dec.DecodeElement(&tu, &start); err != nil {
				continue // skip malformed TUs
			}

			// Find source variant — keep RAW form for rich, strip for plain.
			// An exact srclang match wins; failing that, a sublanguage variant
			// of it counts (srclang="en" with an "en-US" tuv is common).
			var srcLangActual, srcRaw, srcPlain string
			for _, tuv := range tu.Variants {
				lang := langcode.Normalize(tuv.Lang)
				if !langcode.SameBase(lang, srcLang) {
					continue
				}
				raw := strings.TrimSpace(tuv.Seg.InnerXML)
				plain := segText(raw)
				if plain == "" {
					continue
				}
				if srcPlain == "" || lang == srcLang {
					srcLangActual, srcRaw, srcPlain = lang, raw, plain
				}
				if lang == srcLang {
					break
				}
			}
			if srcPlain == "" {
				continue
			}
			// Only emit RichText when raw differs from plain — i.e., when there
			// were actually inline tags.
			srcRich := ""
			if hasInlineTags(srcRaw) {
				srcRich = srcRaw
			}

			// Pair source with each target. Any tuv sharing the source's base
			// language is skipped, not paired: it's either the source itself or
			// a sibling variant, and an en-us→en-gb "translation" is junk.
			for _, tuv := range tu.Variants {
				lang := langcode.Normalize(tuv.Lang)
				if langcode.SameBase(lang, srcLang) {
					continue
				}
				raw := strings.TrimSpace(tuv.Seg.InnerXML)
				plain := segText(raw)
				if plain == "" {
					continue
				}
				rich := ""
				if hasInlineTags(raw) {
					rich = raw
				}
				batch = append(batch, Entry{
					SourceLang:     srcLangActual,
					TargetLang:     lang,
					SourceText:     srcPlain,
					TargetText:     plain,
					SourceRichText: srcRich,
					TargetRichText: rich,
					TUId:           tu.ID,
					CreationDate:   tu.CreationDate,
					Creator:        tu.Creator,
					ChangeDate:     tu.ChangeDate,
					ChangeUser:     tu.ChangeUser,
				})
				if len(batch) == batchSize {
					if err := fn(batch); err != nil {
						return err
					}
					batch = batch[:0]
				}
			}
		}
	}

	if len(batch) > 0 {
		return fn(batch)
	}
	return nil
}

// FlattenEntries expands translation units into source→target pairs.
// The source language is taken from the TMX header's srclang field.
// Each non-source variant in a TU produces one Entry.
func FlattenEntries(f *File) []Entry {
	srcLang := f.Header.SrcLang
	var entries []Entry

	for _, tu := range f.Units {
		// Find the source variant — exact srclang match first, else a
		// sublanguage variant of it. See ParseEntries for the reasoning.
		var srcLangActual, srcText, srcRich string
		for _, v := range tu.Variants {
			if !langcode.SameBase(v.Lang, srcLang) || v.Text == "" {
				continue
			}
			if srcText == "" || v.Lang == srcLang {
				srcLangActual, srcText, srcRich = v.Lang, v.Text, v.RichText
			}
			if v.Lang == srcLang {
				break
			}
		}
		if srcText == "" {
			continue // skip TUs without a source variant
		}

		// Pair source with each target (same-base variants are not targets)
		for _, v := range tu.Variants {
			if langcode.SameBase(v.Lang, srcLang) {
				continue
			}
			entries = append(entries, Entry{
				SourceLang:     srcLangActual,
				TargetLang:     v.Lang,
				SourceText:     srcText,
				TargetText:     v.Text,
				SourceRichText: srcRich,
				TargetRichText: v.RichText,
				TUId:           tu.ID,
				CreationDate:   tu.CreationDate,
				Creator:        tu.Creator,
				ChangeDate:     tu.ChangeDate,
				ChangeUser:     tu.ChangeUser,
			})
		}
	}

	return entries
}

// Export writes TM entries as a valid TMX 1.4b XML document.
// The srcLang is used as the header's srclang attribute.
// Inline markup in entry text is written as raw XML (not escaped).
// This is a convenience wrapper around WriteHeader, WriteEntries, WriteFooter.
func Export(w io.Writer, srcLang string, entries []Entry) error {
	if err := WriteHeader(w, srcLang); err != nil {
		return err
	}
	if err := WriteEntries(w, entries); err != nil {
		return err
	}
	return WriteFooter(w)
}

// WriteHeader writes the TMX XML declaration, header, and opening body tag.
func WriteHeader(w io.Writer, srcLang string) error {
	_, err := fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header creationtool="openloc" creationtoolversion="1.0"
          datatype="plaintext" segtype="sentence"
          adminlang="en" srclang="%s"/>
  <body>
`, xmlEscape(srcLang))
	return err
}

// WriteEntries writes a batch of entries as individual TUs.
// Each entry becomes its own <tu> with source and target <tuv> elements.
// Can be called multiple times for streaming export.
func WriteEntries(w io.Writer, entries []Entry) error {
	for _, e := range entries {
		// Build <tu> opening tag with metadata attributes
		tu := "    <tu"
		if e.TUId != "" {
			tu += fmt.Sprintf(` tuid="%s"`, xmlEscape(e.TUId))
		}
		if e.CreationDate != "" {
			tu += fmt.Sprintf(` creationdate="%s"`, xmlEscape(e.CreationDate))
		}
		if e.Creator != "" {
			tu += fmt.Sprintf(` creationid="%s"`, xmlEscape(e.Creator))
		}
		if e.ChangeDate != "" {
			tu += fmt.Sprintf(` changedate="%s"`, xmlEscape(e.ChangeDate))
		}
		if e.ChangeUser != "" {
			tu += fmt.Sprintf(` changeid="%s"`, xmlEscape(e.ChangeUser))
		}
		tu += ">\n"
		if _, err := fmt.Fprint(w, tu); err != nil {
			return err
		}
		srcSeg := segContentForExport(e.SourceText, e.SourceRichText)
		tgtSeg := segContentForExport(e.TargetText, e.TargetRichText)
		if _, err := fmt.Fprintf(w, "      <tuv xml:lang=\"%s\">\n        <seg>%s</seg>\n      </tuv>\n",
			xmlEscape(e.SourceLang), srcSeg); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "      <tuv xml:lang=\"%s\">\n        <seg>%s</seg>\n      </tuv>\n",
			xmlEscape(e.TargetLang), tgtSeg); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "    </tu>\n"); err != nil {
			return err
		}
	}
	return nil
}

// WriteFooter writes the closing body and tmx tags.
func WriteFooter(w io.Writer) error {
	_, err := fmt.Fprintf(w, "  </body>\n</tmx>\n")
	return err
}

// xmlEscape escapes a string for use in XML attribute values.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
