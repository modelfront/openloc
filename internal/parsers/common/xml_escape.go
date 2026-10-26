package common

import (
	"strings"
)

// XMLEscapeAttr escapes a string for use in an XML attribute value.
func XMLEscapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// XMLEscapeText escapes translated text for XML content.
// Only escapes the 3 required XML entities — does not escape quotes
// since we're in element content, not attributes.
func XMLEscapeText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
