package parsers

import (
	"openloc/internal/parsers/common"
	"openloc/internal/parsers/json_i18n"
	"openloc/internal/parsers/po"
	"openloc/internal/parsers/txt"
	"openloc/internal/parsers/xlsx"
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
func GetParserFor(format string, inlineHTML bool) (common.Parser, error) {
	if inlineHTML || !InlineHTMLFormat(format) {
		return GetParser(format)
	}
	switch format {
	case "json_i18n":
		return common.SentenceSegmenter{Inner: json_i18n.JSONi18n{PlainText: true}}, nil
	case "po", "pot":
		return po.PO{PlainText: true}, nil
	case "txt":
		return common.SentenceSegmenter{Inner: txt.TXT{PlainText: true}}, nil
	default:
		return xlsx.XLSX{PlainText: true}, nil
	}
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
	// XLSXLayout is the uploader's column layout of a workbook; nil reads an
	// Excel file by its row 1.
	XLSXLayout *xlsx.XLSXLayout
}

// GetParserWith is GetParserFor that also applies an Excel column layout.
func GetParserWith(format string, o ParseOptions) (common.Parser, error) {
	if format == "xlsx" && o.XLSXLayout != nil {
		return xlsx.XLSX{PlainText: !o.InlineHTML, Layout: o.XLSXLayout}, nil
	}
	return GetParserFor(format, o.InlineHTML)
}

// FileXLSXLayout decodes the layout stored on a file row; nil when none.
func FileXLSXLayout(raw []byte) (*xlsx.XLSXLayout, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	return xlsx.ParseXLSXLayout(raw)
}
