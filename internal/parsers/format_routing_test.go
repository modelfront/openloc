package parsers

import (
	"context"
	"openloc/internal/parsers/common"
	"openloc/internal/parsers/json_i18n"
	"openloc/internal/parsers/xliff2"
	"openloc/internal/parsers/xlsx"
	"testing"
)

func TestGetParser(t *testing.T) {
	p, err := GetParser("json_i18n")
	if err != nil {
		t.Fatalf("GetParser(json_i18n) failed: %v", err)
	}
	if ss, ok := p.(common.SentenceSegmenter); !ok || ss.Inner != (json_i18n.JSONi18n{}) {
		t.Fatalf("GetParser(json_i18n) = %#v, want JSONi18n split into sentences", p)
	}

	// XLIFF 2.x is its own format: a 2.x file parses with the 2.x reader, not the 1.2 one.
	p, err = GetParser("xliff2")
	if err != nil {
		t.Fatalf("GetParser(xliff2) failed: %v", err)
	}
	if _, ok := p.(xliff2.XLIFF2); !ok {
		t.Fatalf("GetParser(xliff2) = %#v, want xliff2.XLIFF2", p)
	}

	_, err = GetParser("unknown")
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}

// exportXLSX reconstructs a workbook the way pipeline.Service.ExportFile does:
// it gets the parser by format and, because XLSX implements
// LangAwareReconstructor, passes the job's language.

func exportXLSX(t *testing.T, original []byte, translations map[string]string, targetLang string) ([]byte, error) {
	t.Helper()
	return xlsx.XLSX{}.ReconstructForLang(context.Background(), original, translations, common.ExportLangs{Target: targetLang})
}
