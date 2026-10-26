package docx

import (
	"openloc/internal/parsers/common"
)

// DocxSidecar delegates DOCX parsing to the Python parser sidecar.
type DocxSidecar struct {
	BaseURL string
}

func (d DocxSidecar) Parse(content []byte) ([]common.Segment, error) {
	return common.SidecarParse(d.BaseURL, "/parse/docx", content)
}

// Reconstruct sends translations with their chips; the sidecar rebuilds bold,
// italic and links from them.
func (d DocxSidecar) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	return common.SidecarReconstruct(d.BaseURL, "/reconstruct/docx", original, translations)
}
