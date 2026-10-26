package xlsx

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// XLSXInspection describes a workbook for the upload's column-layout step:
// whether it is bilingual (row 1 holds language codes, so no layout is
// needed), the columns of every visible sheet, and a suggested layout
// otherwise.
type XLSXInspection struct {
	Bilingual bool            `json:"bilingual"`
	Sheets    []XLSXSheetInfo `json:"sheets"`
	Suggested *XLSXLayout     `json:"suggested_layout,omitempty"`
}

// XLSXSheetInfo is a visible sheet and the columns that hold text, left to
// right: the columns a layout may choose.
type XLSXSheetInfo struct {
	Name    string           `json:"name"`
	Columns []XLSXColumnInfo `json:"columns"`
}

// XLSXColumnInfo is a column that holds text, with its text in row 1.
type XLSXColumnInfo struct {
	Letter string `json:"letter"`
	Title  string `json:"title,omitempty"`
}

// titleRunes caps a column title.
const titleRunes = 120

// InspectXLSX reads each visible sheet once as a stream; the first sheet is
// read even when hidden, since row 1 there decides whether the workbook is
// bilingual.
func InspectXLSX(content []byte) (_ XLSXInspection, err error) {
	defer recoverXLSX(context.Background(), "xlsx inspect", &err)
	f, err := openXLSX(content)
	if err != nil {
		return XLSXInspection{}, fmt.Errorf("xlsx inspect: open failed: %w", err)
	}
	defer f.Close()
	stringCells, err := xlsxStringCells(content)
	if err != nil {
		return XLSXInspection{}, err
	}

	var out XLSXInspection
	suggested := &XLSXLayout{}
	for i, name := range f.GetSheetList() {
		visible, err := f.GetSheetVisible(name)
		if err != nil {
			return XLSXInspection{}, fmt.Errorf("xlsx inspect: sheet %q visibility: %w", name, err)
		}
		if !visible && i > 0 {
			continue
		}
		scan, err := scanSheetForLayout(f, name, stringCells[name])
		if err != nil {
			return XLSXInspection{}, err
		}
		if i == 0 {
			_, _, detectErr := detectLanguageColumns(scan.firstRow)
			out.Bilingual = detectErr == nil
		}
		if !visible {
			continue
		}
		out.Sheets = append(out.Sheets, XLSXSheetInfo{Name: name, Columns: scan.textColumns()})
		if scan.sourceCol > 0 {
			suggested.Sheets = append(suggested.Sheets, suggestSheetLayout(name, scan))
		}
	}
	if !out.Bilingual && len(suggested.Sheets) > 0 {
		out.Suggested = suggested
	}
	return out, nil
}

type sheetLayoutScan struct {
	rows       int
	firstRow   []string
	headerLike bool
	letters    map[int]int // 1-based column → letters in its text cells
	sourceCol  int         // the column with the most letters; 0 when none
}

func scanSheetForLayout(f *excelize.File, sheet string, stringCells map[string]bool) (sheetLayoutScan, error) {
	var scan sheetLayoutScan
	rows, err := f.Rows(sheet)
	if err != nil {
		return scan, fmt.Errorf("xlsx inspect: rows of %q: %w", sheet, err)
	}
	defer rows.Close()
	scan.letters = map[int]int{}
	firstRowText := 0
	for rows.Next() {
		scan.rows++
		cols, err := rows.Columns()
		if err != nil {
			return scan, fmt.Errorf("xlsx inspect: row %d of %q: %w", scan.rows, sheet, err)
		}
		if scan.rows == 1 {
			scan.firstRow = cols
			scan.headerLike = true
		}
		for c, v := range cols {
			if v == "" {
				continue
			}
			cell, err := excelize.CoordinatesToCellName(c+1, scan.rows)
			if err != nil {
				return scan, fmt.Errorf("xlsx inspect: cell name: %w", err)
			}
			text := stringCells[cell]
			if scan.rows == 1 {
				if text {
					firstRowText++
				} else {
					scan.headerLike = false
				}
			}
			if n := countLetters(v); text && n > 0 {
				scan.letters[c+1] += n
			}
		}
	}
	if err := rows.Error(); err != nil {
		return scan, fmt.Errorf("xlsx inspect: rows of %q: %w", sheet, err)
	}
	// A header is a row of at least two text headings above other rows; a
	// one-column list starts with content.
	scan.headerLike = scan.headerLike && firstRowText >= 2 && scan.rows >= 2
	best := 0
	for col, n := range scan.letters {
		if n > best || (n == best && col < scan.sourceCol) {
			best, scan.sourceCol = n, col
		}
	}
	return scan, nil
}

// suggestSheetLayout picks the column with the most text.
func suggestSheetLayout(name string, scan sheetLayoutScan) XLSXSheetLayout {
	col, _ := excelize.ColumnNumberToName(scan.sourceCol)
	return XLSXSheetLayout{Name: name, HeaderRow: scan.headerLike, Columns: []string{col}}
}

// textColumns lists the columns that hold text, left to right, with their
// row 1 text as the title.
func (scan sheetLayoutScan) textColumns() []XLSXColumnInfo {
	cols := make([]int, 0, len(scan.letters))
	for c := range scan.letters {
		cols = append(cols, c)
	}
	sort.Ints(cols)
	out := make([]XLSXColumnInfo, len(cols))
	for i, c := range cols {
		letter, _ := excelize.ColumnNumberToName(c)
		title := strings.TrimSpace(cellAt(scan.firstRow, c-1))
		if utf8.RuneCountInString(title) > titleRunes {
			title = string([]rune(title)[:titleRunes]) + "…"
		}
		out[i] = XLSXColumnInfo{Letter: letter, Title: title}
	}
	return out
}

func countLetters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}
