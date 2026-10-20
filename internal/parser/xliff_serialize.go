package parser

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

// BilingualSegment is the input shape for SerializeBilingualXLIFF — the
// minimal data needed to emit one `<trans-unit>` with both `<source>`
// and `<target>`. Decoupled from the DB row shape (`db.ListAllForBilingualExportRow`)
// so the serializer is pure: callers translate from their own row type
// into this and the function has no DB or file-format dependency.
//
// `Key` is REQUIRED and MUST equal `segments.segment_key` so that the
// XLIFF re-import path (internal/file/reimport.go) can match
// the round-trip back to the same DB row. Without that contract, the
// re-import would either silently mis-match or fall back to source-text
// matching (which is ambiguous for repeated source text). The
// integration test `TestIntegration_BilingualExport_RoundTripsViaReimport`
// pins this end-to-end.
type BilingualSegment struct {
	Key string

	// Source text: prefer rich (with XLIFF inline tags) when present, fall
	// back to plain. The serializer keeps the rich form's inline tags in
	// `<source>` so the recipient CAT tool sees the same tag chips the user
	// saw in our editor.
	SourceText     string
	SourceRichText string

	// Target text: same rich-vs-plain priority. Empty target is a valid
	// state — the recipient is a translator about to fill it in. We emit
	// `<target></target>` so XLIFF state attribute machinery still works
	// (state="needs-translation").
	TargetText     string
	TargetRichText string

	// Status is the translation lifecycle marker: needs_review / approved /
	// auto_approved, or empty for untranslated — the export builder
	// leaves Status blank when there's no target text. Mapped to XLIFF 1.2
	// `<target state="...">` per the table in mapStatusToXLIFFState.
	Status string

	// Locked overrides Status when true: locked segments emit
	// state="signed-off" so importing CAT tools honor the lock as a
	// finalization signal. Without this, a re-imported locked
	// segment from another tool could clobber the protected state.
	Locked bool

	// Origin is where the pre-translation came from: "tm", "mt", "ape",
	// "file_import", or "" for none. Human edits keep it, so it describes
	// the pre-translation even after the text was changed.
	Origin string
	// MatchPercent is the score of a TM pre-translation (101 = context
	// match); nil for other origins.
	MatchPercent *int
	// Match is the TM unit a TM pre-translation used; nil when unknown.
	Match *TMMatch
}

// TMMatch is the TM unit behind a TM pre-translation, written as an
// <alt-trans>. Plain text only: it is a reference for the translator.
type TMMatch struct {
	SourceText string
	TargetText string
}

// xliffExtNamespace holds the attributes XLIFF 1.2 has no standard place
// for. memoQ reads a match rate only from a named <trans-unit> attribute,
// so the TM score is also written as openloc:match-percent.
const xliffExtNamespace = "urn:openloc:xliff"

// XLIFF 1.2 standard `state` values, which CAT tools read without
// extensions.
const (
	xliffStateNeedsTranslation       = "needs-translation"
	xliffStateNeedsReviewTranslation = "needs-review-translation"
	xliffStateFinal                  = "final"
	xliffStateSignedOff              = "signed-off"
)

// mapStatusToXLIFFState translates our translation status to the XLIFF 1.2
// `state` attribute:
//
//   - "" (no target text)  → needs-translation
//   - needs_review         → needs-review-translation
//   - approved, auto_approved → final
//   - locked=true          → signed-off, whatever the status
func mapStatusToXLIFFState(status string, locked bool) string {
	if locked {
		return xliffStateSignedOff
	}
	switch status {
	case "approved", "auto_approved":
		return xliffStateFinal
	case "needs_review":
		return xliffStateNeedsReviewTranslation
	default:
		// Untranslated, or a status this mapping does not know yet.
		return xliffStateNeedsTranslation
	}
}

// mapOriginToStateQualifier translates the pre-translation origin to the
// XLIFF 1.2 `state-qualifier` attribute, which Trados, memoQ and Phrase can
// map to their own match types. "" means no attribute.
func mapOriginToStateQualifier(origin string, percent *int) string {
	switch origin {
	case "tm":
		switch {
		case percent == nil:
			return "leveraged-tm"
		case *percent >= 100:
			return "exact-match"
		default:
			return "fuzzy-match"
		}
	case "mt", "ape":
		return "leveraged-mt"
	default:
		return ""
	}
}

// SerializeBilingualXLIFF builds an XLIFF 1.2 document carrying both
// `<source>` and `<target>` for every segment, regardless of the original
// file's source format (txt, docx, json, xliff, etc.). This is the
// "bilingual export" feature that lets external translators work in
// another CAT tool against our project's segments.
//
// Output structure (per XLIFF 1.2 spec):
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2" xmlns:openloc="urn:openloc:xliff">
//	  <file original="report.docx" source-language="en" target-language="es" datatype="plaintext">
//	    <body>
//	      <trans-unit id="seg_key" openloc:match-percent="74">
//	        <source>...</source>
//	        <target state="..." state-qualifier="fuzzy-match">...</target>
//	        <alt-trans match-quality="74" origin="tm">
//	          <source>TM unit source</source>
//	          <target>TM unit target</target>
//	        </alt-trans>
//	      </trans-unit>
//	      ...
//	    </body>
//	  </file>
//	</xliff>
//
// Inline tags (XLIFF <bpt>, <ept>, <ph>, <x>, <g>) in source/target rich
// text are kept as inline elements; the text around and inside them is
// XML-escaped.
//
// Source/target language codes are emitted as-is (the caller is responsible
// for normalizing). Empty strings would produce malformed XLIFF — the
// caller MUST validate before passing.
//
// `original` is the name of the source file the segments were extracted from
// (e.g. "report.docx"). XLIFF 1.2 makes it a REQUIRED attribute on `<file>`,
// alongside source-language and datatype, and importing tools reject files
// that omit it.
// We emit the caller-supplied name verbatim (XML-attribute-escaped); it is
// informational identity for the recipient tool, not a round-trip key.
func SerializeBilingualXLIFF(original, sourceLang, targetLang string, segments []BilingualSegment) ([]byte, error) {
	if original == "" {
		return nil, fmt.Errorf("original file name is required")
	}
	if sourceLang == "" {
		return nil, fmt.Errorf("source language is required")
	}
	if targetLang == "" {
		return nil, fmt.Errorf("target language is required")
	}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&buf, `<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2" xmlns:openloc=%s>`+"\n", quoteXMLAttr(xliffExtNamespace))
	fmt.Fprintf(&buf, `  <file original=%s source-language=%s target-language=%s datatype="plaintext">`+"\n",
		quoteXMLAttr(original), quoteXMLAttr(sourceLang), quoteXMLAttr(targetLang))
	buf.WriteString("    <body>\n")

	for _, seg := range segments {
		// segment_key as trans-unit id — the load-bearing round-trip
		// contract. Without an id the re-import path can't match.
		if seg.Key == "" {
			return nil, fmt.Errorf("segment with empty Key cannot be serialized — re-import would fail")
		}

		// Pre-translation lineage describes a target; an empty one has none.
		hasTarget := seg.TargetText != "" || seg.TargetRichText != ""
		var percent *int
		var match *TMMatch
		qualifier := ""
		if hasTarget {
			qualifier = mapOriginToStateQualifier(seg.Origin, seg.MatchPercent)
			if seg.Origin == "tm" {
				percent, match = seg.MatchPercent, seg.Match
			}
		}

		fmt.Fprintf(&buf, `      <trans-unit id=%s`, quoteXMLAttr(seg.Key))
		if percent != nil {
			fmt.Fprintf(&buf, ` openloc:match-percent="%d"`, *percent)
		}
		buf.WriteString(">\n")

		// <source>: prefer rich form (XLIFF inline tags) over plain.
		buf.WriteString("        <source>")
		if err := writeXLIFFContent(&buf, seg.SourceText, seg.SourceRichText); err != nil {
			return nil, fmt.Errorf("write source for segment %q: %w", seg.Key, err)
		}
		buf.WriteString("</source>\n")

		// <target state="...">: always present, so even an empty target
		// tells the receiving tool "this needs translation."
		fmt.Fprintf(&buf, `        <target state=%s`, quoteXMLAttr(mapStatusToXLIFFState(seg.Status, seg.Locked)))
		if qualifier != "" {
			fmt.Fprintf(&buf, ` state-qualifier=%s`, quoteXMLAttr(qualifier))
		}
		buf.WriteString(">")
		if err := writeXLIFFContent(&buf, seg.TargetText, seg.TargetRichText); err != nil {
			return nil, fmt.Errorf("write target for segment %q: %w", seg.Key, err)
		}
		buf.WriteString("</target>\n")

		// XLIFF 1.2 puts <alt-trans> after the trans-unit's own <target>.
		if match != nil {
			buf.WriteString("        <alt-trans")
			if percent != nil {
				fmt.Fprintf(&buf, ` match-quality="%d"`, *percent)
			}
			buf.WriteString(` origin="tm">` + "\n")
			buf.WriteString("          <source>")
			if err := xml.EscapeText(&buf, []byte(match.SourceText)); err != nil {
				return nil, fmt.Errorf("write match source for segment %q: %w", seg.Key, err)
			}
			buf.WriteString("</source>\n          <target>")
			if err := xml.EscapeText(&buf, []byte(match.TargetText)); err != nil {
				return nil, fmt.Errorf("write match target for segment %q: %w", seg.Key, err)
			}
			buf.WriteString("</target>\n        </alt-trans>\n")
		}

		buf.WriteString("      </trans-unit>\n")
	}

	buf.WriteString("    </body>\n")
	buf.WriteString("  </file>\n")
	buf.WriteString("</xliff>\n")
	return buf.Bytes(), nil
}

// writeXLIFFContent writes rich text when present, else plain text, as
// well-formed XLIFF element content: inline tags are kept and text is
// escaped (see xliffRichToXML).
//
// Returns the write error so the caller can wrap it with segment context.
func writeXLIFFContent(buf *bytes.Buffer, plain, rich string) error {
	if rich != "" {
		_, err := buf.WriteString(xliffRichToXML(rich))
		return err
	}
	return xml.EscapeText(buf, []byte(plain))
}

// quoteXMLAttr returns s as a double-quoted XML attribute value that an XML
// parser reads back as exactly s. xml.EscapeText writes `"`, tab, newline
// and CR as character references, which attribute-value normalization
// leaves alone. Go's %q is not XML quoting: it doubles a backslash and
// writes a no-break space as a backslash-u escape, which XML keeps as text.
func quoteXMLAttr(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	// The error is discarded because strings.Builder writes cannot fail.
	_ = xml.EscapeText(&sb, []byte(s))
	sb.WriteByte('"')
	return sb.String()
}
