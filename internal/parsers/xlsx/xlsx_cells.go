package xlsx

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"
	"openloc/internal/parsers/common"
)

// A workbook without language codes in row 1 is translated cell by cell:
// every text cell of every visible sheet is a segment, keyed "<sheet>!<cell>",
// and the export writes each translation back into its own cell.
//
// Cells that hold no text to translate are left alone: numbers, dates,
// booleans, formulas (their value is computed) and text without a letter
// ("00123", "—"). Hidden sheets are skipped.

// xlsxCellMeta is the Segment.Context of a cell segment.
type xlsxCellMeta struct {
	Sheet string `json:"sheet"`
	Cell  string `json:"cell"`
}

// xlsxRowKeyRe matches row keys ("row_12"): stored files segmented by
// column A carry them, and their translations export to column B.
var xlsxRowKeyRe = regexp.MustCompile(`^row_\d+$`)

func xlsxCellKey(sheet, cell string) string { return sheet + "!" + cell }

// splitXLSXCellKey splits at the last "!": a cell reference never holds one,
// a sheet name may.
func splitXLSXCellKey(key string) (sheet, cell string, ok bool) {
	i := strings.LastIndex(key, "!")
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

// parseXLSXCells returns a segment per text cell of the visible sheets, in
// reading order: sheet by sheet, row by row, left to right. content is the
// workbook f was opened from; xlsxStringCells reads the cell types from it.
// plainText takes the strings as plain text rather than HTML (XLSX.PlainText).
func parseXLSXCells(f *excelize.File, content []byte, plainText bool) ([]common.Segment, error) {
	stringCells, err := xlsxStringCells(content)
	if err != nil {
		return nil, err
	}
	var segments []common.Segment
	for _, sheet := range f.GetSheetList() {
		visible, err := f.GetSheetVisible(sheet)
		if err != nil {
			return nil, fmt.Errorf("xlsx: sheet %q visibility: %w", sheet, err)
		}
		if !visible {
			continue
		}
		if segments, err = appendTextCells(segments, f, sheet, stringCells[sheet], nil, plainText); err != nil {
			return nil, err
		}
	}
	if len(segments) == 0 {
		return nil, &common.XLSXNoTextError{Msg: "The workbook has no text to translate on its visible sheets."}
	}
	return segments, nil
}

// appendTextCells appends a segment per text cell of sheet, row by row, left
// to right. keep, when set, picks cells by their 1-based row and column.
func appendTextCells(segments []common.Segment, f *excelize.File, sheet string, stringCells map[string]bool, keep func(row, col int) bool, plainText bool) ([]common.Segment, error) {
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("xlsx: read rows of %q: %w", sheet, err)
	}
	for r, row := range rows {
		for c, value := range row {
			if !hasLetter(value) || (keep != nil && !keep(r+1, c+1)) {
				continue
			}
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				return nil, fmt.Errorf("xlsx: cell name: %w", err)
			}
			if !stringCells[cell] {
				continue
			}
			meta, err := json.Marshal(xlsxCellMeta{Sheet: sheet, Cell: cell})
			if err != nil {
				return nil, fmt.Errorf("xlsx: cell metadata: %w", err)
			}
			plain, rich := common.ImportString(value, plainText)
			segments = append(segments, common.Segment{
				Key:      xlsxCellKey(sheet, cell),
				Text:     plain,
				RichText: rich,
				Position: len(segments),
				Context:  string(meta),
			})
		}
	}
	return segments, nil
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// xlsxRowKeyed reports translations keyed by row ("row_N"), the keys of a
// stored file segmented by column A.
func xlsxRowKeyed(translations map[string]string) bool {
	if len(translations) == 0 {
		return false
	}
	for key := range translations {
		if !xlsxRowKeyRe.MatchString(key) {
			return false
		}
	}
	return true
}

// writeXLSXCells writes each translation into the cell its key names, keeping
// the cell's style. The cell's own source text decides the markup written
// back (ExportString).
func writeXLSXCells(f *excelize.File, translations map[string]string, plainText bool) error {
	for key, translated := range translations {
		if translated == "" {
			continue
		}
		sheet, cell, ok := splitXLSXCellKey(key)
		if !ok {
			return fmt.Errorf("segment key %q names no cell", key)
		}
		if idx, err := f.GetSheetIndex(sheet); err != nil || idx < 0 {
			return fmt.Errorf("segment key %q names a sheet the workbook does not have", key)
		}
		if _, _, err := excelize.CellNameToCoordinates(cell); err != nil {
			return fmt.Errorf("segment key %q names no cell: %w", key, err)
		}
		source, err := f.GetCellValue(sheet, cell)
		if err != nil {
			return fmt.Errorf("read cell %s: %w", key, err)
		}
		if err := f.SetCellValue(sheet, cell, common.ExportString(translated, source, plainText)); err != nil {
			return fmt.Errorf("set cell %s: %w", key, err)
		}
	}
	return nil
}
