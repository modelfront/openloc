package pdf

import (
	"openloc/internal/parsers/common"
)

// PdfSidecar delegates PDF parsing to the Python parser sidecar.
// PDF is import-only — Reconstruct always returns an error.
type PdfSidecar struct {
	BaseURL string
}

func (p PdfSidecar) Parse(content []byte) ([]common.Segment, error) {
	return common.SidecarParse(p.BaseURL, "/parse/pdf", content)
}

// ExportOnlyError marks PdfSidecar as a parser that never writes a file.
func (PdfSidecar) ExportOnlyError() {}

func (p PdfSidecar) Reconstruct(_ []byte, _ map[string]string) ([]byte, error) {
	return nil, &common.ExportError{Msg: "A PDF cannot be exported in its original form. Export as bilingual XLIFF instead."}
}
