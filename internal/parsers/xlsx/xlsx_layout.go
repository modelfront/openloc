package xlsx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
	"openloc/internal/parsers/common"
)

// XLSXLayout is the uploader's answer for a workbook without language codes
// in row 1: the sheets to translate and, per sheet, the columns that hold the
// text, or none for every text cell of the sheet. Sheets left out are not
// read. Segments are keyed "<sheet>!<cell>", as in the cell mode, and the
// export writes each translation back into its own cell.
type XLSXLayout struct {
	Sheets []XLSXSheetLayout `json:"sheets"`
}

// XLSXSheetLayout is one sheet of an XLSXLayout.
type XLSXSheetLayout struct {
	Name string `json:"name"`
	// HeaderRow: row 1 names the columns and is not translated.
	HeaderRow bool `json:"header_row,omitempty"`
	// Columns are the letters ("B") of the columns to translate; none for
	// every text cell of the sheet.
	Columns []string `json:"columns,omitempty"`
}

func layoutErrorf(format string, args ...any) error {
	return &common.XLSXLayoutError{Msg: fmt.Sprintf(format, args...)}
}

// ParseXLSXLayout decodes a layout and normalizes its columns. It does not
// check it against a workbook: see Validate.
func ParseXLSXLayout(raw []byte) (*XLSXLayout, error) {
	var l XLSXLayout
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, layoutErrorf("The Excel column layout is not valid JSON.")
	}
	l.Normalize()
	return &l, nil
}

// Normalize upper-cases the columns and drops repeats.
func (l *XLSXLayout) Normalize() {
	for i := range l.Sheets {
		s := &l.Sheets[i]
		seen := map[string]bool{}
		cols := s.Columns[:0]
		for _, c := range s.Columns {
			c = strings.ToUpper(strings.TrimSpace(c))
			if !seen[c] {
				seen[c] = true
				cols = append(cols, c)
			}
		}
		s.Columns = cols
	}
}

// Validate checks the layout against the workbook: at least one sheet, every
// sheet exists, is visible and is listed once, and every column is a real
// one. It runs checkXLSX, which reads the sheet list, and never opens the
// workbook.
func (l XLSXLayout) Validate(content []byte) (err error) {
	defer recoverXLSX(context.Background(), "xlsx layout", &err)
	if len(l.Sheets) == 0 {
		return layoutErrorf("Choose at least one sheet to translate.")
	}
	pkg, err := checkXLSX(content)
	if err != nil {
		return fmt.Errorf("xlsx layout: %w", err)
	}
	hidden := make(map[string]bool, len(pkg.sheets))
	for _, s := range pkg.sheets {
		hidden[s.name] = s.hidden
	}
	seen := map[string]bool{}
	for _, s := range l.Sheets {
		isHidden, exists := hidden[s.Name]
		if !exists {
			return layoutErrorf("The workbook has no sheet named %q.", s.Name)
		}
		if isHidden {
			return layoutErrorf("Sheet %q is hidden. Show it in Excel to translate it.", s.Name)
		}
		if seen[s.Name] {
			return layoutErrorf("Sheet %q is listed twice.", s.Name)
		}
		seen[s.Name] = true
		for _, c := range s.Columns {
			if !validColumn(c) {
				return layoutErrorf("Sheet %q: %q is not a column.", s.Name, c)
			}
		}
	}
	return nil
}

func validColumn(col string) bool {
	n, err := excelize.ColumnNameToNumber(col)
	return err == nil && n >= 1 && n <= maxXLSXCols
}

// parseXLSXLayout returns a segment per text cell of the layout's sheets, in
// layout order and then row by row, left to right: the chosen columns' cells,
// or every cell of a sheet without any, below row 1 when it is a header.
func parseXLSXLayout(f *excelize.File, content []byte, l XLSXLayout, plainText bool) ([]common.Segment, error) {
	if reads, err := layoutReadsLanguageColumn(f, l); err != nil {
		return nil, err
	} else if reads {
		return nil, layoutErrorf("This workbook has language codes in row 1, so it is read by them. Upload it without a column layout.")
	}
	stringCells, err := xlsxStringCells(content)
	if err != nil {
		return nil, err
	}
	var segments []common.Segment
	for _, s := range l.Sheets {
		cols := map[int]bool{}
		for _, c := range s.Columns {
			n, err := excelize.ColumnNameToNumber(c)
			if err != nil {
				return nil, layoutErrorf("Sheet %q: %q is not a column.", s.Name, c)
			}
			cols[n] = true
		}
		keep := func(row, col int) bool {
			return !(s.HeaderRow && row == 1) && (len(cols) == 0 || cols[col])
		}
		if segments, err = appendTextCells(segments, f, s.Name, stringCells[s.Name], keep, plainText); err != nil {
			return nil, err
		}
	}
	if len(segments) == 0 {
		return nil, &common.XLSXNoTextError{Msg: "The chosen sheets and columns hold no text to translate."}
	}
	return segments, nil
}

// layoutReadsLanguageColumn reports a layout that would read a column headed
// by a language code in row 1 of the first sheet (the codes Parse reads
// without a layout) as source text: a chosen column there, or that whole
// sheet. Short headings such as "ID" or "SKU" read as codes too, so other
// columns stay choosable.
func layoutReadsLanguageColumn(f *excelize.File, l XLSXLayout) (bool, error) {
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return false, nil
	}
	var first *XLSXSheetLayout
	for i := range l.Sheets {
		if l.Sheets[i].Name == sheets[0] {
			first = &l.Sheets[i]
		}
	}
	if first == nil {
		return false, nil
	}
	header, err := xlsxFirstRow(f, sheets[0])
	if err != nil {
		return false, err
	}
	sourceCol, targetCols, err := detectLanguageColumns(header)
	if err != nil {
		return false, nil
	}
	if len(first.Columns) == 0 {
		return true, nil
	}
	languages := map[int]bool{sourceCol: true}
	for _, col := range targetCols {
		languages[col] = true
	}
	for _, letter := range first.Columns {
		if n, err := excelize.ColumnNameToNumber(letter); err == nil && languages[n-1] {
			return true, nil
		}
	}
	return false, nil
}

// xlsxFirstRow is row 1 of a sheet, read as a stream: the rest of the sheet
// is not decoded.
func xlsxFirstRow(f *excelize.File, sheet string) ([]string, error) {
	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("xlsx: rows of %q: %w", sheet, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Error()
	}
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("xlsx: row 1 of %q: %w", sheet, err)
	}
	return cols, nil
}
