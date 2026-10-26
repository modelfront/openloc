package xlsx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/xuri/excelize/v2"
	"openloc/internal/langcode"
	"openloc/internal/parsers/common"
)

// langCodeRe matches ISO 639-1 (2-letter), ISO 639-2 (3-letter), and BCP 47 codes.
// Examples: "en", "fr", "de", "pt-BR", "zh-Hans", "en-US", "sr-Latn"
var langCodeRe = regexp.MustCompile(`(?i)^[a-z]{2,3}(-[a-z]{2,4})?$`)

// xlsxMeta stores column mapping in the Segment.Context field for Reconstruct.
type xlsxMeta struct {
	Sheet      string         `json:"sheet"`
	SourceCol  int            `json:"source_col"`
	TargetCols map[string]int `json:"target_cols"` // normalized lang → column index (0-based)
}

// XLSX parses Excel workbooks in one of two modes, decided by row 1 of the
// first sheet:
//   - bilingual: row 1 holds language codes; the first language column is the
//     source and the rest are targets, on the first sheet;
//   - cells: anything else; every text cell of every visible sheet is a
//     segment (see xlsx_cells.go).
type XLSX struct {
	// PlainText says the file's strings are plain text, not HTML (see GetParserFor).
	PlainText bool
	// Layout, when set, reads the sheets and columns the uploader chose instead
	// of either mode above (see xlsx_layout.go); the export writes each
	// translation back into its own cell, as in the cell mode.
	Layout *XLSXLayout
}

func (p XLSX) Parse(content []byte) (_ []common.Segment, err error) {
	defer recoverXLSX(context.Background(), "xlsx", &err)
	f, err := openXLSX(content)
	if err != nil {
		return nil, fmt.Errorf("xlsx: open failed: %w", err)
	}
	defer f.Close()

	if p.Layout != nil {
		return parseXLSXLayout(f, content, *p.Layout, p.PlainText)
	}
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("xlsx: no sheets found")
	}
	sheet := sheets[0]

	header, err := xlsxFirstRow(f, sheet)
	if err != nil {
		return nil, err
	}
	sourceCol, targetCols, err := detectLanguageColumns(header)
	if err != nil {
		return parseXLSXCells(f, content, p.PlainText)
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("xlsx: read rows: %w", err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%w: a header row and no data rows", common.ErrXLSXEmpty)
	}

	// Store metadata for Reconstruct
	meta := xlsxMeta{
		Sheet:      sheet,
		SourceCol:  sourceCol,
		TargetCols: targetCols,
	}
	metaJSON, _ := json.Marshal(meta)

	startRow := firstContentRow(false)

	var segments []common.Segment
	for i, row := range rows[startRow:] {
		excelRow := i + startRow + 1 // 1-indexed
		sourceText := cellAt(row, sourceCol)
		if strings.TrimSpace(sourceText) == "" {
			continue
		}

		srcPlain, srcRich := common.ImportString(sourceText, p.PlainText)
		seg := common.Segment{
			Key:      fmt.Sprintf("row_%d", excelRow),
			Text:     srcPlain,
			RichText: srcRich,
			Position: len(segments),
			Context:  string(metaJSON),
		}

		segments = append(segments, seg)
	}

	if len(segments) == 0 {
		return nil, common.ErrXLSXEmpty
	}

	return segments, nil
}

var _ common.LangAwareReconstructor = XLSX{}

// Reconstruct writes translations into their cells (cell mode), or into the
// workbook's only target column. A workbook with several target columns fails
// here: the export uses ReconstructForLang.
func (p XLSX) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	return p.reconstruct(context.Background(), original, translations, "")
}

// ReconstructForLang writes translations into their cells (cell mode), or into
// the column for the target language (see xlsxTargetColumn).
func (p XLSX) ReconstructForLang(ctx context.Context, original []byte, translations map[string]string, langs common.ExportLangs) ([]byte, error) {
	return p.reconstruct(ctx, original, translations, langs.Target)
}

// reconstruct writes translations into their cells for a workbook without
// language codes, else into one column of the first sheet (targetLang may be
// empty only when the sheet has a single target column). Row keys ("row_N")
// come from stored files segmented by column A; their translations go to
// column B, next to column A.
func (p XLSX) reconstruct(ctx context.Context, original []byte, translations map[string]string, targetLang string) (_ []byte, err error) {
	defer recoverXLSX(ctx, "xlsx reconstruct", &err)
	f, err := openXLSX(original)
	if err != nil {
		return nil, fmt.Errorf("xlsx reconstruct: open failed: %w", err)
	}
	defer f.Close()

	if p.Layout != nil {
		if err := writeXLSXCells(f, translations, p.PlainText); err != nil {
			return nil, fmt.Errorf("xlsx reconstruct: %w", err)
		}
		return writeXLSX(f)
	}
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("xlsx reconstruct: no sheets")
	}
	sheet := sheets[0]

	header, err := xlsxFirstRow(f, sheet)
	if err != nil {
		return nil, fmt.Errorf("xlsx reconstruct: %w", err)
	}
	sourceCol, targetCols, detectErr := detectLanguageColumns(header)
	monolingual := detectErr != nil
	if monolingual && oneLanguageAmongHeadings(header) {
		return nil, fmt.Errorf("xlsx reconstruct: %w", &common.ExportError{Msg: "Row 1 has only one language code, so the export cannot tell which column is the job's language. Head each language column with its code, such as fr or pt-BR, or export as bilingual XLIFF."})
	}
	if monolingual && !xlsxRowKeyed(translations) {
		if err := writeXLSXCells(f, translations, p.PlainText); err != nil {
			return nil, fmt.Errorf("xlsx reconstruct: %w", err)
		}
		return writeXLSX(f)
	}
	// Row keys and language columns write by row: the whole sheet is read.
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("xlsx reconstruct: read rows: %w", err)
	}
	if monolingual {
		sourceCol = 0
	}
	if !monolingual && len(rows) < 2 {
		return nil, fmt.Errorf("xlsx reconstruct: need header + data rows")
	}
	writeCol := 1
	if !monolingual {
		writeCol, err = xlsxTargetColumn(targetCols, targetLang)
		if err != nil {
			return nil, fmt.Errorf("xlsx reconstruct: %w", err)
		}
	}

	startRow := firstContentRow(monolingual)
	for i, row := range rows[startRow:] {
		excelRow := i + startRow + 1 // 1-indexed
		key := fmt.Sprintf("row_%d", excelRow)
		if translated, ok := translations[key]; ok && translated != "" {
			cell, _ := excelize.CoordinatesToCellName(writeCol+1, excelRow)
			if err := f.SetCellValue(sheet, cell, common.ExportString(translated, cellAt(row, sourceCol), p.PlainText)); err != nil {
				return nil, fmt.Errorf("xlsx reconstruct: set cell %s: %w", cell, err)
			}
		}
	}

	return writeXLSX(f)
}

func writeXLSX(f *excelize.File) ([]byte, error) {
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, fmt.Errorf("xlsx reconstruct: write: %w", err)
	}
	return buf.Bytes(), nil
}

// xlsxTargetColumn picks the column that receives targetLang's translations:
// the column with that exact language, else the one column whose bare base
// code matches it ("fr-ca" finds "fr", "fr" finds "fr-ca"). Any other case is
// an error, not a guess: a wrong column overwrites another language's text.
// Only a call without a language may take the workbook's only target column.
func xlsxTargetColumn(targetCols map[string]int, targetLang string) (int, error) {
	lang := langcode.Normalize(targetLang)
	langs := strings.Join(slices.Sorted(maps.Keys(targetCols)), ", ")
	if lang == "" {
		if len(targetCols) == 1 {
			for _, col := range targetCols {
				return col, nil
			}
		}
		return 0, fmt.Errorf("workbook has %d target columns (%s), so the target language is required", len(targetCols), langs)
	}
	if col, ok := targetCols[lang]; ok {
		return col, nil
	}
	// A bare base code and its regions match each other (fr ↔ fr-CA); two
	// regions of one base (zh-Hans ↔ zh-Hant) are different languages.
	var related []string
	for l := range targetCols {
		if langcode.BaseLang(l) == lang || langcode.BaseLang(lang) == l {
			related = append(related, l)
		}
	}
	switch len(related) {
	case 1:
		return targetCols[related[0]], nil
	case 0:
		return 0, &common.ExportError{Msg: fmt.Sprintf("No target column for language %q (target columns: %s). Add the column to the workbook, or export as bilingual XLIFF.", targetLang, langs)}
	default:
		slices.Sort(related)
		return 0, &common.ExportError{Msg: fmt.Sprintf("Language %q matches several target columns (%s). Give the job's language its own column, or export as bilingual XLIFF.", targetLang, strings.Join(related, ", "))}
	}
}

// XLSXTarget is one target cell: its plain text, and its inline tags in the
// editor's XLIFF form (empty when it has none), as Parse reads a source cell.
type XLSXTarget struct {
	Text     string
	RichText string
}

// ExtractXLSXTargets returns the text already in the workbook for each of
// targetLangs: lang → {row_key → target}. Parse leaves Segment.Target empty,
// so this is the one path that imports target text. Each language reads the
// column the export writes for it (xlsxTargetColumn), so a `fr` column feeds a
// `fr-CA` job; a language with no column, or with two that fit, is left out,
// as are columns no target language reads.
func ExtractXLSXTargets(content []byte, targetLangs []string) (map[string]map[string]XLSXTarget, error) {
	return XLSX{}.ExtractTargets(content, targetLangs)
}

// ExtractTargets is ExtractXLSXTargets for a workbook whose cells are plain
// text when p.PlainText is set.
func (p XLSX) ExtractTargets(content []byte, targetLangs []string) (_ map[string]map[string]XLSXTarget, err error) {
	// A layout's translations go back into their source cells: there is no
	// target column to read, so the workbook is not opened.
	if p.Layout != nil {
		return nil, nil
	}
	defer recoverXLSX(context.Background(), "xlsx extract", &err)
	f, err := openXLSX(content)
	if err != nil {
		return nil, fmt.Errorf("xlsx extract: open failed: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("xlsx extract: no sheets")
	}
	sheet := sheets[0]

	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("xlsx extract: read rows: %w", err)
	}
	if len(rows) < 2 {
		return nil, nil
	}

	sourceCol, targetCols, err := detectLanguageColumns(rows[0])
	if err != nil {
		return nil, fmt.Errorf("xlsx extract: %w", err)
	}

	langCols := make(map[string]int, len(targetLangs))
	result := make(map[string]map[string]XLSXTarget, len(targetLangs))
	for _, lang := range targetLangs {
		if col, err := xlsxTargetColumn(targetCols, lang); err == nil {
			langCols[lang] = col
			result[lang] = make(map[string]XLSXTarget)
		}
	}

	for i, row := range rows[1:] {
		excelRow := i + 2
		sourceText := cellAt(row, sourceCol)
		if strings.TrimSpace(sourceText) == "" {
			continue
		}
		key := fmt.Sprintf("row_%d", excelRow)
		for lang, col := range langCols {
			target := cellAt(row, col)
			if strings.TrimSpace(target) != "" {
				plain, rich := common.ImportString(target, p.PlainText)
				result[lang][key] = XLSXTarget{Text: plain, RichText: rich}
			}
		}
	}

	return result, nil
}

type xlsxLangCol struct {
	lang string // normalized
	col  int
}

// headerLanguageCells returns the header cells that hold a language code, in
// column order: the whole cell ("en-GB"), or one line of it
// ("English (UK)\nen-GB").
// isLanguageHeading reports a heading written as a code ("fr", "pt-BR") that
// names a language: "SKU" has the shape of a code but is not one.
func isLanguageHeading(s string) bool {
	return langCodeRe.MatchString(s) && langcode.Known(s)
}

func headerLanguageCells(header []string) []xlsxLangCol {
	var detected []xlsxLangCol
	for i, cell := range header {
		trimmed := strings.TrimSpace(cell)
		if trimmed == "" {
			continue
		}
		if isLanguageHeading(trimmed) {
			detected = append(detected, xlsxLangCol{lang: langcode.Normalize(trimmed), col: i})
			continue
		}
		for _, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			if isLanguageHeading(line) {
				detected = append(detected, xlsxLangCol{lang: langcode.Normalize(line), col: i})
				break
			}
		}
	}
	return detected
}

// oneLanguageAmongHeadings reports a header row with exactly one language
// code next to other headings: a multi-language sheet whose other columns use
// codes langCodeRe does not read (es-419, pt_BR). A one-column sheet whose
// first cell is a short word ("OK") is not one.
func oneLanguageAmongHeadings(header []string) bool {
	if len(headerLanguageCells(header)) != 1 {
		return false
	}
	filled := 0
	for _, cell := range header {
		if strings.TrimSpace(cell) != "" {
			filled++
		}
	}
	return filled > 1
}

// detectLanguageColumns reads the header row and identifies source + target language columns.
// Returns: sourceCol index (0-based), map of normalized lang → column index for targets.
func detectLanguageColumns(header []string) (int, map[string]int, error) {
	// First pass: detect a dedicated "source text" column by header name.
	// Common headers: "Source text", "Source", "Source (en)", etc.
	sourceTextCol := -1
	sourceTextRe := regexp.MustCompile(`(?i)^source\b`)
	for i, cell := range header {
		trimmed := strings.TrimSpace(cell)
		if sourceTextRe.MatchString(trimmed) {
			sourceTextCol = i
			break
		}
	}

	detected := headerLanguageCells(header)

	// If we found a "Source text" column and language columns are all targets,
	// use the source text column as source and all language columns as targets.
	if sourceTextCol >= 0 && len(detected) >= 1 {
		targetCols := make(map[string]int, len(detected))
		for _, d := range detected {
			targetCols[d.lang] = d.col
		}
		return sourceTextCol, targetCols, nil
	}

	if len(detected) < 2 {
		return 0, nil, fmt.Errorf("need at least 2 language columns in header row, found %d (headers: %v)", len(detected), header)
	}

	// First language column is source, rest are targets
	sourceCol := detected[0].col
	targetCols := make(map[string]int, len(detected)-1)
	for _, d := range detected[1:] {
		targetCols[d.lang] = d.col
	}

	return sourceCol, targetCols, nil
}

// cellAt safely gets a cell value from a row slice.
func cellAt(row []string, col int) string {
	if col >= len(row) {
		return ""
	}
	return row[col]
}

// firstContentRow is the 0-based index of the first row that holds segments.
// A file with language columns spends row 1 on them. A file without them has
// no header: row 1 is content, imported as a segment and exported translated.
func firstContentRow(monolingual bool) int {
	if monolingual {
		return 0
	}
	return 1
}
