package xlsx

import (
	"bytes"
	"fmt"
	"openloc/internal/parsers/xlsx/xlsxtest"
	"testing"

	"github.com/xuri/excelize/v2"
)

// Every segment Parse emits for a workbook without language codes comes back
// translated from Reconstruct, into its own cell — row 1 included, since it
// is content in this mode.
func TestXLSXMonolingual_Row1TranslationSurvivesExport(t *testing.T) {
	content := xlsxtest.BuildXLSX(t,
		[]string{"Hello world"},
		[][]string{
			{"Thank you"},
			{"Goodbye"},
		},
	)

	p := XLSX{}
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(segments) != 3 || segments[0].Key != "Sheet1!A1" {
		t.Fatalf("expected 3 segments starting at Sheet1!A1, got %d (first key %q)", len(segments), segments[0].Key)
	}
	sources := make([]string, len(segments))
	for i, seg := range segments {
		sources[i] = seg.Text
	}

	translations := make(map[string]string, len(segments))
	for _, seg := range segments {
		translations[seg.Key] = "T:" + seg.Text
	}

	out, err := p.Reconstruct(content, translations)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("open reconstructed: %v", err)
	}
	defer f.Close()

	for row := 1; row <= len(segments); row++ {
		got, err := f.GetCellValue("Sheet1", fmt.Sprintf("A%d", row))
		if err != nil {
			t.Fatalf("read A%d: %v", row, err)
		}
		if want := "T:" + sources[row-1]; got != want {
			t.Errorf("A%d = %q, want %q (translation of row %d dropped on export)", row, got, want, row)
		}
	}
}
