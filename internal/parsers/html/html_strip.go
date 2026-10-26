package html

import (
	"regexp"
	"strings"
)

// formattingTagRe matches HTML formatting tags: <strong>, <em>, <sup>, <sub>, <b>, <i>
// and their closing variants. Also strips <p> wrappers.
var formattingTagRe = regexp.MustCompile(`</?(?:strong|em|sup|sub|b|i|p)>`)

// StripHTMLFormatting removes inline HTML formatting tags from text,
// keeping only the text content. Used for export formats that don't support HTML
// (XLIFF, plain text, JSON, PO, Markdown).
//
// Example: "This is <strong>bold</strong> text" → "This is bold text"
func StripHTMLFormatting(s string) string {
	result := formattingTagRe.ReplaceAllString(s, "")
	// Decode common HTML entities that TipTap might produce
	result = strings.ReplaceAll(result, "&amp;", "&")
	result = strings.ReplaceAll(result, "&lt;", "<")
	result = strings.ReplaceAll(result, "&gt;", ">")
	result = strings.ReplaceAll(result, "&quot;", `"`)
	return strings.TrimSpace(result)
}

// HasHTMLFormatting returns true if the text contains HTML formatting tags.
func HasHTMLFormatting(s string) bool {
	return formattingTagRe.MatchString(s)
}

// StripFormattingTags removes the formatting tags from rich text and leaves
// everything else, chips and character data alike, as it is.
func StripFormattingTags(s string) string {
	return formattingTagRe.ReplaceAllString(s, "")
}
