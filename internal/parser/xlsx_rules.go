package parser

import "slices"

// LayoutFor is the layout that rules naming sheets and columns give this
// workbook: every visible sheet named in sheets (all visible sheets when
// sheets is empty), each with headerRow, columns and targets. nil when the
// workbook is bilingual, so row 1 reads it, or when no named sheet is a
// visible one.
func (in XLSXInspection) LayoutFor(sheets []string, headerRow bool, columns []string, targets []XLSXTargetColumn) *XLSXLayout {
	if in.Bilingual {
		return nil
	}
	var l XLSXLayout
	for _, s := range in.Sheets {
		if len(sheets) > 0 && !slices.Contains(sheets, s.Name) {
			continue
		}
		l.Sheets = append(l.Sheets, XLSXSheetLayout{Name: s.Name, HeaderRow: headerRow, Columns: slices.Clone(columns), TargetColumns: slices.Clone(targets)})
	}
	if len(l.Sheets) == 0 {
		return nil
	}
	l.Normalize()
	return &l
}
