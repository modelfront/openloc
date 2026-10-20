package analysis

import (
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
	db "openloc/internal/database/sqlcgen"
)

// A saved analysis as a spreadsheet: one row per file × target language, and
// for every band its segments, words and characters, each with its
// auto-approved subset. CSV and XLSX carry the same rows; XLSX leaves an
// empty row between target languages so each language reads as its own block.

type exportFormat string

const (
	exportCSV  exportFormat = "csv"
	exportXLSX exportFormat = "xlsx"
)

func parseExportFormat(s string) (exportFormat, bool) {
	switch f := exportFormat(s); f {
	case exportCSV, exportXLSX:
		return f, true
	}
	return "", false
}

func (f exportFormat) contentType() string {
	if f == exportXLSX {
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	return "text/csv; charset=utf-8"
}

// bandCounts is one band's numbers for one row, in column order.
type bandCounts struct {
	Segments, SegmentsAutoApproved     int64
	Words, WordsAutoApproved           int64
	Characters, CharactersAutoApproved int64
}

func (c bandCounts) values() []int64 {
	return []int64{c.Segments, c.SegmentsAutoApproved, c.Words, c.WordsAutoApproved, c.Characters, c.CharactersAutoApproved}
}

var bandColumnSuffixes = []string{
	"segments", "segments auto-approved",
	"words", "words auto-approved",
	"characters", "characters auto-approved",
}

func counts(seg, segAA, words, wordsAA, chars, charsAA int32) bandCounts {
	return bandCounts{int64(seg), int64(segAA), int64(words), int64(wordsAA), int64(chars), int64(charsAA)}
}

// exportBands is the band order and labels of the Analyses tab
// (ANALYSIS_BANDS in web/src/api/analyses.ts).
var exportBands = []struct {
	label  string
	counts func(r db.JobAnalysis) bandCounts
}{
	{"Repetitions", func(r db.JobAnalysis) bandCounts {
		return counts(r.RepetitionsSegments, r.RepetitionsSegmentsAutoApproved, r.RepetitionsWords, r.RepetitionsWordsAutoApproved, r.RepetitionsCharacters, r.RepetitionsCharactersAutoApproved)
	}},
	{"New", func(r db.JobAnalysis) bandCounts {
		return counts(r.NewSegments, r.NewSegmentsAutoApproved, r.NewWords, r.NewWordsAutoApproved, r.NewCharacters, r.NewCharactersAutoApproved)
	}},
	{"65–84%", func(r db.JobAnalysis) bandCounts {
		return counts(r.Tm6584Segments, r.Tm6584SegmentsAutoApproved, r.Tm6584Words, r.Tm6584WordsAutoApproved, r.Tm6584Characters, r.Tm6584CharactersAutoApproved)
	}},
	{"85–99%", func(r db.JobAnalysis) bandCounts {
		return counts(r.Tm8599Segments, r.Tm8599SegmentsAutoApproved, r.Tm8599Words, r.Tm8599WordsAutoApproved, r.Tm8599Characters, r.Tm8599CharactersAutoApproved)
	}},
	{"100%", func(r db.JobAnalysis) bandCounts {
		return counts(r.Tm100Segments, r.Tm100SegmentsAutoApproved, r.Tm100Words, r.Tm100WordsAutoApproved, r.Tm100Characters, r.Tm100CharactersAutoApproved)
	}},
	{"101%", func(r db.JobAnalysis) bandCounts {
		return counts(r.Tm101Segments, r.Tm101SegmentsAutoApproved, r.Tm101Words, r.Tm101WordsAutoApproved, r.Tm101Characters, r.Tm101CharactersAutoApproved)
	}},
}

// exportRow is one file × target language. Bands holds every exportBands
// entry in order.
type exportRow struct {
	File           string
	TargetLanguage string
	Bands          []bandCounts
}

func (r exportRow) numbers() []int64 {
	out := make([]int64, 0, len(r.Bands)*len(bandColumnSuffixes))
	for _, b := range r.Bands {
		out = append(out, b.values()...)
	}
	return out
}

func exportHeader() []string {
	h := []string{"File", "Target language"}
	for _, b := range exportBands {
		for _, s := range bandColumnSuffixes {
			h = append(h, b.label+" "+s)
		}
	}
	return h
}

// exportRows turns the breakdown rows into export rows, sorted by target
// language, then file name, so two downloads of one analysis match line for
// line.
func exportRows(breakdowns []db.JobAnalysis) []exportRow {
	rows := make([]exportRow, 0, len(breakdowns))
	for _, b := range breakdowns {
		row := exportRow{File: b.JobNameSnapshot, TargetLanguage: b.TargetLanguage, Bands: make([]bandCounts, len(exportBands))}
		for i, band := range exportBands {
			row.Bands[i] = band.counts(b)
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].TargetLanguage != rows[j].TargetLanguage {
			return rows[i].TargetLanguage < rows[j].TargetLanguage
		}
		return rows[i].File < rows[j].File
	})
	return rows
}

// writeCSV writes a UTF-8 BOM (Excel reads a CSV without one as Latin-1),
// the header and one line per row.
func writeCSV(w io.Writer, rows []exportRow) error {
	if _, err := io.WriteString(w, "\uFEFF"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(exportHeader()); err != nil {
		return err
	}
	for _, r := range rows {
		line := []string{csvText(r.File), csvText(r.TargetLanguage)}
		for _, n := range r.numbers() {
			line = append(line, strconv.FormatInt(n, 10))
		}
		if err := cw.Write(line); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// csvText stops a spreadsheet from running a file name as a formula: a cell
// starting with = + - @ tab or CR gets a leading apostrophe.
func csvText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

const (
	sheetAnalysis = "Analysis"
	sheetAbout    = "About"
)

// writeXLSX writes two sheets: the table, with an empty row between target
// languages, and what the table is. Text goes in as text cells, never as
// formulas.
func writeXLSX(w io.Writer, a db.Analysis, rows []exportRow, exportedAt time.Time) (err error) {
	f := excelize.NewFile()
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	if err := f.SetSheetName("Sheet1", sheetAnalysis); err != nil {
		return err
	}
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return err
	}

	// The header wraps onto up to three lines ("Repetitions / characters /
	// auto-approved"), so the number columns only need the longest word.
	headerStyle, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"},
	})
	if err != nil {
		return err
	}
	header := exportHeader()
	if err := f.SetSheetRow(sheetAnalysis, "A1", &header); err != nil {
		return err
	}
	if err := f.SetRowStyle(sheetAnalysis, 1, 1, headerStyle); err != nil {
		return err
	}
	if err := f.SetRowHeight(sheetAnalysis, 1, 3*15); err != nil {
		return err
	}
	line := 2
	for i, r := range rows {
		if i > 0 && r.TargetLanguage != rows[i-1].TargetLanguage {
			line++
		}
		cells := []any{r.File, r.TargetLanguage}
		for _, n := range r.numbers() {
			cells = append(cells, n)
		}
		cell, err := excelize.CoordinatesToCellName(1, line)
		if err != nil {
			return err
		}
		if err := f.SetSheetRow(sheetAnalysis, cell, &cells); err != nil {
			return err
		}
		line++
	}
	if err := f.SetPanes(sheetAnalysis, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}
	if err := f.SetColWidth(sheetAnalysis, "A", "A", 40); err != nil {
		return err
	}
	if err := f.SetColWidth(sheetAnalysis, "B", "B", 16); err != nil {
		return err
	}
	last, err := excelize.ColumnNumberToName(len(header))
	if err != nil {
		return err
	}
	if err := f.SetColWidth(sheetAnalysis, "C", last, 15); err != nil {
		return err
	}

	if err := writeAboutSheet(f, a, exportedAt, bold); err != nil {
		return err
	}
	return f.Write(w)
}

func writeAboutSheet(f *excelize.File, a db.Analysis, exportedAt time.Time, bold int) error {
	if _, err := f.NewSheet(sheetAbout); err != nil {
		return err
	}
	completed := ""
	if a.CompletedAt.Valid {
		completed = formatUTC(a.CompletedAt.Time)
	}
	about := [][2]string{
		{"Analysis", a.Name},
		{"Source language", a.SourceLanguage},
		{"Created", formatUTC(a.CreatedAt.Time)},
		{"Completed", completed},
		{"Exported", formatUTC(exportedAt)},
	}
	for i, kv := range about {
		row := []any{kv[0], kv[1]}
		if err := f.SetSheetRow(sheetAbout, fmt.Sprintf("A%d", i+1), &row); err != nil {
			return err
		}
	}
	if err := f.SetColStyle(sheetAbout, "A", bold); err != nil {
		return err
	}
	if err := f.SetColWidth(sheetAbout, "A", "A", 18); err != nil {
		return err
	}
	return f.SetColWidth(sheetAbout, "B", "B", 60)
}

func formatUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

var unsafeFilenameChars = regexp.MustCompile(`[\\/:*?"<>|]+`)

// exportFilename is "<analysis name> <yyyy-mm-dd>.<ext>": dated so two
// downloads do not collide and the file says when it was taken.
func exportFilename(name string, f exportFormat, now time.Time) string {
	safe := strings.Join(strings.Fields(unsafeFilenameChars.ReplaceAllString(name, "-")), " ")
	if r := []rune(safe); len(r) > 100 {
		safe = strings.TrimSpace(string(r[:100]))
	}
	if safe == "" {
		safe = "analysis"
	}
	return fmt.Sprintf("%s %s.%s", safe, now.UTC().Format("2006-01-02"), f)
}
