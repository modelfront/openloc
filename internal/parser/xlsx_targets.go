package parser

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/xuri/excelize/v2"

	"openloc/internal/langcode"
)

// A sheet of an XLSXLayout with target columns is multilingual: it has one
// source column, and the export writes each target language into its own
// column on the same row, keeping the source text. Without target columns,
// the export writes each translation back into its source cell.

// MaxXLSXTargetColumns caps the target columns of one sheet or profile.
const MaxXLSXTargetColumns = 100

// XLSXTargetColumn is the column ("C") the export writes one target language
// ("fr") into.
type XLSXTargetColumn struct {
	Language string `json:"language"`
	Column   string `json:"column"`
}

// NormalizeXLSXTargets trims each pair, normalizes its language code,
// upper-cases its column and drops pairs with neither. Repeats stay, for
// ValidateXLSXTargets to name.
func NormalizeXLSXTargets(targets []XLSXTargetColumn) []XLSXTargetColumn {
	out := []XLSXTargetColumn{}
	for _, t := range targets {
		t.Language = langcode.Normalize(t.Language)
		t.Column = strings.ToUpper(strings.TrimSpace(t.Column))
		if t.Language != "" || t.Column != "" {
			out = append(out, t)
		}
	}
	return out
}

// ValidateXLSXTargets checks normalized target columns against the source
// columns: exactly one source column, each language a known code listed
// once, each column a real one that is neither the source nor another
// language's. The error is an *XLSXLayoutError.
func ValidateXLSXTargets(columns []string, targets []XLSXTargetColumn) error {
	if len(targets) == 0 {
		return nil
	}
	if len(targets) > MaxXLSXTargetColumns {
		return layoutErrorf("At most %d languages can have their own column.", MaxXLSXTargetColumns)
	}
	if len(columns) != 1 {
		return layoutErrorf("Choose exactly one source column for the translations to go next to.")
	}
	source := columns[0]
	langs := make(map[string]bool, len(targets))
	owner := make(map[string]string, len(targets))
	for _, t := range targets {
		if !langcode.Known(t.Language) {
			return layoutErrorf("%q is not a language code, such as fr or pt-BR.", t.Language)
		}
		if !validColumn(t.Column) {
			return layoutErrorf("%q is not a column, such as C.", t.Column)
		}
		if t.Column == source {
			return layoutErrorf("Column %s holds the source text, so %s cannot be written into it.", source, t.Language)
		}
		if langs[t.Language] {
			return layoutErrorf("%s has two target columns.", t.Language)
		}
		if other, taken := owner[t.Column]; taken {
			return layoutErrorf("Column %s is the target of both %s and %s.", t.Column, other, t.Language)
		}
		langs[t.Language] = true
		owner[t.Column] = t.Language
	}
	return nil
}

// layoutTargetColumns returns, per sheet with target columns, the 1-based
// column that receives targetLang (see langColumn). A sheet without a column
// for the language refuses the export: writing it into the source cell would
// lose the source.
func layoutTargetColumns(l XLSXLayout, targetLang string) (map[string]int, error) {
	dest := map[string]int{}
	for _, s := range l.Sheets {
		if len(s.TargetColumns) == 0 {
			continue
		}
		cols := make(map[string]int, len(s.TargetColumns))
		for _, t := range s.TargetColumns {
			n, err := excelize.ColumnNameToNumber(t.Column)
			if err != nil {
				return nil, fmt.Errorf("sheet %q: target column %q: %w", s.Name, t.Column, err)
			}
			cols[t.Language] = n - 1
		}
		col, err := xlsxTargetColumn(cols, targetLang)
		if err != nil {
			lang := langcode.Normalize(targetLang)
			if lang == "" || len(relatedLangs(cols, lang)) > 0 {
				return nil, fmt.Errorf("sheet %q: %w", s.Name, err)
			}
			return nil, &ExportError{Msg: fmt.Sprintf("The file's Excel layout gives %s no column on sheet %q (target columns: %s). Upload the file again with a filter profile that maps %s to a column, or export as bilingual XLIFF.",
				targetLang, s.Name, strings.Join(slices.Sorted(maps.Keys(cols)), ", "), targetLang)}
		}
		dest[s.Name] = col + 1
	}
	return dest, nil
}

// Multilingual reports a layout with a sheet that has target columns.
func (l XLSXLayout) Multilingual() bool {
	for _, s := range l.Sheets {
		if len(s.TargetColumns) > 0 {
			return true
		}
	}
	return false
}

// MultilingualXLSX reports a stored layout (files.xlsx_layout) with target
// columns: the file's languages download together, into one workbook.
func MultilingualXLSX(raw []byte) bool {
	l, err := FileXLSXLayout(raw)
	return err == nil && l != nil && l.Multilingual()
}

// CheckSeparateColumns refuses target languages that write into the same
// column of a sheet, such as fr and fr-CA with only an fr column: written
// into one workbook, the later would overwrite the earlier. Each language
// alone has its column, so its own export still works.
func (l XLSXLayout) CheckSeparateColumns(targetLangs []string) error {
	owner := map[string]map[int]string{}
	for _, lang := range targetLangs {
		dest, err := layoutTargetColumns(l, lang)
		if err != nil {
			return err
		}
		for sheet, col := range dest {
			if owner[sheet] == nil {
				owner[sheet] = map[int]string{}
			}
			if other, taken := owner[sheet][col]; taken {
				letter, err := excelize.ColumnNumberToName(col)
				if err != nil {
					return fmt.Errorf("sheet %q: column %d: %w", sheet, col, err)
				}
				return &ExportError{Msg: fmt.Sprintf("%s and %s both go into column %s on sheet %q, so one would overwrite the other. Download them one at a time, or give each its own column in the filter profile.",
					other, langcode.Normalize(lang), letter, sheet)}
			}
			owner[sheet][col] = langcode.Normalize(lang)
		}
	}
	return nil
}

// extractLayoutTargets is ExtractTargets for a layout with target columns:
// for each language with a column (the one its export writes), the text
// already in that column, keyed by the source cell of its row as Parse keys
// a segment. Rows whose source cell is not a segment are read too and left
// for the caller to ignore. A language with no column, or with two that fit,
// is left out. The workbook is opened once and each sheet read once.
func (p XLSX) extractLayoutTargets(content []byte, targetLangs []string) (map[string]map[string]XLSXTarget, error) {
	f, err := openXLSX(content)
	if err != nil {
		return nil, fmt.Errorf("xlsx extract: open failed: %w", err)
	}
	defer f.Close()
	result := make(map[string]map[string]XLSXTarget, len(targetLangs))
	dests := make(map[string]map[string]int, len(targetLangs))
	for _, lang := range targetLangs {
		if dest, err := layoutTargetColumns(*p.Layout, lang); err == nil {
			dests[lang] = dest
			result[lang] = map[string]XLSXTarget{}
		}
	}
	for _, s := range p.Layout.Sheets {
		if len(s.TargetColumns) == 0 || len(s.Columns) != 1 {
			continue
		}
		source, err := excelize.ColumnNameToNumber(s.Columns[0])
		if err != nil {
			return nil, fmt.Errorf("xlsx extract: sheet %q: source column: %w", s.Name, err)
		}
		rows, err := f.GetRows(s.Name)
		if err != nil {
			return nil, fmt.Errorf("xlsx extract: read rows of %q: %w", s.Name, err)
		}
		for r, row := range rows {
			if (s.HeaderRow && r == 0) || !hasLetter(cellAt(row, source-1)) {
				continue
			}
			key, err := excelize.CoordinatesToCellName(source, r+1)
			if err != nil {
				return nil, fmt.Errorf("xlsx extract: cell name: %w", err)
			}
			for lang, dest := range dests {
				col := dest[s.Name]
				if col == 0 {
					continue
				}
				v := cellAt(row, col-1)
				if strings.TrimSpace(v) == "" {
					continue
				}
				plain, rich := importString(v, p.PlainText)
				result[lang][xlsxCellKey(s.Name, key)] = XLSXTarget{Text: plain, RichText: rich}
			}
		}
	}
	return result, nil
}
