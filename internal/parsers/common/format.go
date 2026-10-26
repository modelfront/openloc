package common

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// FormatFromFilename detects the file format from the filename extension.
func FormatFromFilename(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".json"):
		return "json_i18n"
	case strings.HasSuffix(lower, ".po"):
		return "po"
	case strings.HasSuffix(lower, ".pot"):
		return "pot"
	case strings.HasSuffix(lower, ".sdlxliff"), strings.HasSuffix(lower, ".mxliff"), strings.HasSuffix(lower, ".mqxliff"):
		return "xliff"
	case strings.HasSuffix(lower, ".xliff"), strings.HasSuffix(lower, ".xlf"):
		return "xliff"
	case strings.HasSuffix(lower, ".md"):
		return "markdown"
	case strings.HasSuffix(lower, ".html"), strings.HasSuffix(lower, ".htm"):
		return "html"
	case strings.HasSuffix(lower, ".pdf"):
		return "pdf"
	case strings.HasSuffix(lower, ".docx"):
		return "docx"
	case strings.HasSuffix(lower, ".pptx"):
		return "pptx"
	case strings.HasSuffix(lower, ".strings"):
		return "apple_strings"
	case strings.HasSuffix(lower, ".xml"):
		return "android_xml"
	case strings.HasSuffix(lower, ".xlsx"):
		return "xlsx"
	case strings.HasSuffix(lower, ".txt"):
		return "txt"
	default:
		return ""
	}
}

// DetectFormat resolves a file to a format, sniffing the content where the
// extension alone can't decide.
//
// An XLIFF 2.x document is "xliff2" whatever its extension: the .xliff and
// .xlf extensions cover both XLIFF versions, and a 2.x document can also come
// as .xml. The root element tells the 2.x documents apart from 1.2.
//
// .xml is ambiguous: Android string resources and generic XML documents
// (SCHEMA ST4, DocBook, DITA, in-house schemas) share the extension but need
// completely different parsers. The root element tells them apart. Every other extension resolves by name alone.
//
// Callers that have the file content should prefer this over
// FormatFromFilename.
func DetectFormat(name string, content []byte) string {
	format := FormatFromFilename(name)
	if (format == "xliff" || format == "android_xml") && isXLIFF2(content) {
		return "xliff2"
	}
	if format == "android_xml" && !isAndroidResources(content) {
		return "xml"
	}
	return format
}

// isXLIFF2 reports whether the document's root is an <xliff> of version 2.x.
func isXLIFF2(content []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(content))
	dec.Entity = xml.HTMLEntity
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if root, ok := tok.(xml.StartElement); ok {
			return root.Name.Local == "xliff" && strings.HasPrefix(XMLAttr(root, "version"), "2.")
		}
	}
}

// isAndroidResources reports whether the document's root element is
// <resources>, the Android string resource container.
func isAndroidResources(content []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(content))
	dec.Entity = xml.HTMLEntity
	for {
		tok, err := dec.Token()
		if err != nil {
			// Unreadable or empty: fall back to the historical mapping rather
			// than rerouting a file we can't inspect.
			return true
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local == "resources"
		}
	}
}
