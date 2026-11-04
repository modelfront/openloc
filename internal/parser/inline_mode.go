package parser

import (
	"encoding/json"
	"fmt"

	"openloc/internal/segmentation"
)

// The strings of a JSON i18n, PO/POT, TXT or XLSX file are HTML or plain
// text, as the "Strings contain HTML" setting says. HTML strings get chips on
// import and keep only the source's tags on export; a plain-text string is
// all text: no chips, and its translation is written as is.

// inlineHTMLFormats are the formats the setting applies to.
var inlineHTMLFormats = map[string]bool{
	"json_i18n": true,
	"po":        true,
	"pot":       true,
	"txt":       true,
	"xlsx":      true,
}

// InlineHTMLFormat reports whether the "Strings contain HTML" setting applies
// to format.
func InlineHTMLFormat(format string) bool {
	return inlineHTMLFormats[format]
}

// GetParserFor returns the parser for a file of format whose strings are HTML
// when inlineHTML is true, plain text otherwise. inlineHTML matters only for
// an InlineHTMLFormat; GetParser is GetParserFor with inlineHTML on.
func GetParserFor(format string, inlineHTML bool) (Parser, error) {
	if inlineHTML || !InlineHTMLFormat(format) {
		return GetParser(format)
	}
	switch format {
	case "json_i18n":
		return sentenceSegmenter{inner: JSONi18n{PlainText: true}}, nil
	case "po", "pot":
		return PO{PlainText: true}, nil
	case "txt":
		return sentenceSegmenter{inner: TXT{PlainText: true}}, nil
	default:
		return XLSX{PlainText: true}, nil
	}
}

// importString splits a string read from a file into its plain and rich text,
// as DetectInlineTags does; a plain-text string has no chips.
func importString(text string, plainText bool) (plain, rich string) {
	if plainText {
		return text, ""
	}
	return DetectInlineTags(text)
}

// exportString is what the export writes for translated, whose source string
// in the file is source.
func exportString(translated, source string, plainText bool) string {
	if plainText {
		return translated
	}
	return sourceInlineMarkup(translated, source)
}

// TextFormatTranslation is the stored form of a translation that an
// InlineHTMLFormat export writes: in an HTML file the rich text when there is
// one, so its chips turn back into the source's tags, else the text; in a
// plain-text file the text.
func TextFormatTranslation(text, rich string, inlineHTML bool) string {
	if inlineHTML && rich != "" {
		return rich
	}
	return text
}

// ParseOptions are the per-file choices a file is parsed and exported with,
// stored on the file row.
type ParseOptions struct {
	InlineHTML bool
	// Segmenter splits prose blocks into sentences; nil uses the conservative
	// built-in set (segmentation.ConservativeID).
	Segmenter *segmentation.Segmenter
	// XLSXLayout is the uploader's column layout of a workbook; nil reads an
	// Excel file by its row 1.
	XLSXLayout *XLSXLayout
	// HTML and XML are the file's filter options for an HTML or generic XML
	// file; nil is DefaultHTMLOptions / DefaultXMLOptions.
	HTML *HTMLOptions
	XML  *XMLOptions
}

// GetParserWith is GetParserFor that also applies an Excel column layout and
// the file's segmentation rules.
func GetParserWith(format string, o ParseOptions) (Parser, error) {
	if format == "xlsx" && o.XLSXLayout != nil {
		return XLSX{PlainText: !o.InlineHTML, Layout: o.XLSXLayout}, nil
	}
	switch {
	case format == "html" && o.HTML != nil:
		return withSegmenter(sentenceSegmenter{inner: HTML{Options: o.HTML}, richExport: true}, o.Segmenter), nil
	case format == "xml" && o.XML != nil:
		return withSegmenter(XML{Options: o.XML}, o.Segmenter), nil
	}
	p, err := GetParserFor(format, o.InlineHTML)
	if err != nil {
		return nil, err
	}
	return withSegmenter(p, o.Segmenter), nil
}

// withSegmenter returns p splitting sentences with seg, for the parsers that
// split; nil keeps the conservative default.
func withSegmenter(p Parser, seg *segmentation.Segmenter) Parser {
	if seg == nil {
		return p
	}
	switch v := p.(type) {
	case sentenceSegmenter:
		v.seg = seg
		return v
	case XML:
		v.Segmenter = seg
		return v
	case PPTX:
		v.Segmenter = seg
		return v
	}
	return p
}

// FileXLSXLayout decodes the layout stored on a file row; nil when none.
func FileXLSXLayout(raw []byte) (*XLSXLayout, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	return ParseXLSXLayout(raw)
}

// FileOptions are the ParseOptions a stored file is parsed with again: its
// strings mode, its Excel layout (FileXLSXLayout), the segmentation snapshot
// it was imported with (nil or JSON null: the conservative built-in set) and
// the HTML or XML options of its filter profile snapshot (nil: the defaults).
// Every re-parse of a stored file goes through here, so it reads exactly as
// it did on import.
func FileOptions(inlineHTML bool, xlsxLayout, segmentationSnapshot, filterSnapshot []byte) (ParseOptions, error) {
	layout, err := FileXLSXLayout(xlsxLayout)
	if err != nil {
		return ParseOptions{}, fmt.Errorf("read stored xlsx layout: %w", err)
	}
	seg, err := segmentation.ForSnapshot(segmentationSnapshot)
	if err != nil {
		return ParseOptions{}, err
	}
	o := ParseOptions{InlineHTML: inlineHTML, XLSXLayout: layout, Segmenter: seg}
	if len(filterSnapshot) > 0 && string(filterSnapshot) != "null" {
		// The parse-relevant part of filterprofile.Snapshot.
		var snap struct {
			Options struct {
				HTML *HTMLOptions `json:"html"`
				XML  *XMLOptions  `json:"xml"`
			} `json:"options"`
		}
		if err := json.Unmarshal(filterSnapshot, &snap); err != nil {
			return ParseOptions{}, fmt.Errorf("read stored filter profile: %w", err)
		}
		o.HTML, o.XML = snap.Options.HTML, snap.Options.XML
	}
	return o, nil
}
