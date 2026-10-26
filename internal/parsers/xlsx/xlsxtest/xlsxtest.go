package xlsxtest

import (
	"bytes"
	"testing"

	"github.com/xuri/excelize/v2"
)

// BuildXLSX creates an in-memory XLSX file with the given header and rows.
func BuildXLSX(t *testing.T, header []string, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	sheet := "Sheet1"

	for i, cell := range header {
		cellName, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cellName, cell)
	}
	for r, row := range rows {
		for c, cell := range row {
			cellName, _ := excelize.CoordinatesToCellName(c+1, r+2)
			f.SetCellValue(sheet, cellName, cell)
		}
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("BuildXLSX: %v", err)
	}
	f.Close()
	return buf.Bytes()
}

// ReadCells returns the values of the named cells on Sheet1.
func ReadCells(t *testing.T, workbook []byte, cells ...string) map[string]string {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(workbook))
	if err != nil {
		t.Fatalf("open exported workbook: %v", err)
	}
	defer f.Close()
	got := make(map[string]string, len(cells))
	for _, cell := range cells {
		v, err := f.GetCellValue("Sheet1", cell)
		if err != nil {
			t.Fatalf("read %s: %v", cell, err)
		}
		got[cell] = v
	}
	return got
}
