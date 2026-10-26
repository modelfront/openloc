package parsers

import (
	"fmt"
	"openloc/internal/parsers/android_xml"
	"openloc/internal/parsers/apple_strings"
	"openloc/internal/parsers/common"
	"openloc/internal/parsers/docx"
	"openloc/internal/parsers/html"
	"openloc/internal/parsers/json_i18n"
	"openloc/internal/parsers/markdown"
	"openloc/internal/parsers/pdf"
	"openloc/internal/parsers/po"
	"openloc/internal/parsers/pptx"
	"openloc/internal/parsers/txt"
	"openloc/internal/parsers/xliff"
	"openloc/internal/parsers/xliff2"
	"openloc/internal/parsers/xlsx"
	"openloc/internal/parsers/xml"
)

// GetParser returns the appropriate parser for a file format. Formats whose
// blocks are prose (HTML, Markdown, TXT, JSON, DOCX, PDF) are split into one
// segment per sentence by SentenceSegmenter; XML splits sentences itself.
func GetParser(format string) (common.Parser, error) {
	switch format {
	case "json_i18n":
		return common.SentenceSegmenter{Inner: json_i18n.JSONi18n{}, RichExport: true}, nil
	case "xliff":
		return xliff.XLIFF{}, nil
	case "xliff2":
		return xliff2.XLIFF2{}, nil
	case "po", "pot":
		return po.PO{}, nil
	case "html":
		return common.SentenceSegmenter{Inner: html.HTML{}, RichExport: true}, nil
	case "markdown":
		return common.SentenceSegmenter{Inner: markdown.Markdown{}}, nil
	case "apple_strings":
		return apple_strings.AppleStrings{}, nil
	case "android_xml":
		return android_xml.AndroidXML{}, nil
	case "xml":
		return xml.XML{}, nil
	case "xlsx":
		return xlsx.XLSX{}, nil
	case "txt":
		return common.SentenceSegmenter{Inner: txt.TXT{}, RichExport: true}, nil
	case "pptx":
		return pptx.PPTX{}, nil
	case "docx":
		return common.SentenceSegmenter{Inner: docx.DocxSidecar{BaseURL: common.SidecarURL}, RichExport: true}, nil
	case "pdf":
		return common.SentenceSegmenter{Inner: pdf.PdfSidecar{BaseURL: common.SidecarURL}}, nil
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}
