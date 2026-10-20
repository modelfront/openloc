package parser

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
	// XLSXLayout is the uploader's column layout of a workbook; nil reads an
	// Excel file by its row 1.
	XLSXLayout *XLSXLayout
}

// GetParserWith is GetParserFor that also applies an Excel column layout.
func GetParserWith(format string, o ParseOptions) (Parser, error) {
	if format == "xlsx" && o.XLSXLayout != nil {
		return XLSX{PlainText: !o.InlineHTML, Layout: o.XLSXLayout}, nil
	}
	return GetParserFor(format, o.InlineHTML)
}

// FileXLSXLayout decodes the layout stored on a file row; nil when none.
func FileXLSXLayout(raw []byte) (*XLSXLayout, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	return ParseXLSXLayout(raw)
}
