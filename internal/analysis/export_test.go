package analysis

import (
	"bytes"
	"encoding/csv"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/xuri/excelize/v2"
	db "openloc/internal/database/sqlcgen"
)

// Two German files and one French file. Each band gets distinct numbers so a
// column read from the wrong band shows up as a wrong value.
func exportFixture() []db.JobAnalysis {
	return []db.JobAnalysis{
		{JobNameSnapshot: "b.docx", TargetLanguage: "de-DE",
			NewSegments: 4, NewWords: 40, NewCharacters: 400, NewWordsAutoApproved: 10,
			Tm100Segments: 2, Tm100Words: 20, Tm100Characters: 200, Tm100SegmentsAutoApproved: 1},
		{JobNameSnapshot: "a.docx", TargetLanguage: "fr-FR",
			RepetitionsSegments: 1, RepetitionsWords: 3, RepetitionsCharacters: 15},
		{JobNameSnapshot: "a.docx", TargetLanguage: "de-DE",
			Tm101Segments: 5, Tm101Words: 50, Tm101Characters: 500, Tm101CharactersAutoApproved: 500,
			Tm6584Segments: 1, Tm6584Words: 7, Tm6584Characters: 30,
			Tm8599Segments: 3, Tm8599Words: 9, Tm8599Characters: 45, Tm8599WordsAutoApproved: 2},
	}
}

// col returns the header index of name, failing when it is missing.
func col(t *testing.T, header []string, name string) int {
	t.Helper()
	for i, h := range header {
		if h == name {
			return i
		}
	}
	t.Fatalf("column %q missing from header %v", name, header)
	return -1
}

func TestExportHeader_EveryBandWithAllMetricsAndNoTotal(t *testing.T) {
	h := exportHeader()
	if len(h) != 2+6*6 {
		t.Fatalf("columns: want 38, got %d", len(h))
	}
	want := []string{"File", "Target language",
		"Repetitions segments", "Repetitions segments auto-approved",
		"Repetitions words", "Repetitions words auto-approved",
		"Repetitions characters", "Repetitions characters auto-approved",
		"New segments"}
	for i, w := range want {
		if h[i] != w {
			t.Errorf("column %d: want %q, got %q", i, w, h[i])
		}
	}
	if got := h[len(h)-1]; got != "101% characters auto-approved" {
		t.Errorf("last column: want %q, got %q", "101% characters auto-approved", got)
	}
	for _, c := range h {
		if strings.HasPrefix(c, "Total") {
			t.Errorf("unexpected total column %q", c)
		}
	}
	// The bands appear in the Analyses tab's order.
	order := []string{"Repetitions words", "New words", "65–84% words", "85–99% words", "100% words", "101% words"}
	prev := -1
	for _, name := range order {
		i := col(t, h, name)
		if i <= prev {
			t.Errorf("%q is out of order", name)
		}
		prev = i
	}
}

func TestExportRows_SortedWithEveryBand(t *testing.T) {
	rows := exportRows(exportFixture())
	if len(rows) != 3 {
		t.Fatalf("rows: want 3, got %d", len(rows))
	}
	got := []string{rows[0].TargetLanguage + "/" + rows[0].File, rows[1].TargetLanguage + "/" + rows[1].File, rows[2].TargetLanguage + "/" + rows[2].File}
	want := []string{"de-DE/a.docx", "de-DE/b.docx", "fr-FR/a.docx"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: want %s, got %s", i, want[i], got[i])
		}
	}

	h := exportHeader()
	nums := rows[0].numbers() // de-DE/a.docx
	val := func(name string) int64 { return nums[col(t, h, name)-2] }
	if v := val("101% words"); v != 50 {
		t.Errorf("101%% words: want 50, got %d", v)
	}
	if v := val("101% characters auto-approved"); v != 500 {
		t.Errorf("101%% characters auto-approved: want 500, got %d", v)
	}
	if v := val("85–99% words auto-approved"); v != 2 {
		t.Errorf("85–99%% words auto-approved: want 2, got %d", v)
	}
	if v := val("65–84% segments"); v != 1 {
		t.Errorf("65–84%% segments: want 1, got %d", v)
	}
	if len(nums) != 6*6 {
		t.Errorf("numbers per row: want 36, got %d", len(nums))
	}
}

func TestWriteCSV_BOMHeaderAndRowsNoTotals(t *testing.T) {
	var buf bytes.Buffer
	if err := writeCSV(&buf, exportRows(exportFixture())); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("\xEF\xBB\xBF")) {
		t.Fatalf("missing UTF-8 BOM")
	}
	records, err := csv.NewReader(bytes.NewReader(buf.Bytes()[3:])).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("lines: want header + 3 rows (no totals), got %d", len(records))
	}
	if strings.Join(records[0], ",") != strings.Join(exportHeader(), ",") {
		t.Errorf("header mismatch: %v", records[0])
	}
	i := col(t, records[0], "101% words")
	if records[1][0] != "a.docx" || records[1][1] != "de-DE" || records[1][i] != "50" {
		t.Errorf("first row: want a.docx/de-DE/50, got %s/%s/%s", records[1][0], records[1][1], records[1][i])
	}
	if len(records[1]) != 38 {
		t.Errorf("fields per line: want 38, got %d", len(records[1]))
	}
}

func TestWriteCSV_FormulaLikeNamesAreText(t *testing.T) {
	rows := exportRows([]db.JobAnalysis{{JobNameSnapshot: "=HYPERLINK(\"x\")", TargetLanguage: "de"}, {JobNameSnapshot: "@sum.txt", TargetLanguage: "de"}})
	var buf bytes.Buffer
	if err := writeCSV(&buf, rows); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(bytes.NewReader(buf.Bytes()[3:])).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records[1:] {
		if !strings.HasPrefix(r[0], "'") {
			t.Errorf("file %q is not escaped", r[0])
		}
	}
	if csvText("plain-name.txt") != "plain-name.txt" {
		t.Errorf("a normal name must stay unchanged")
	}
}

func TestWriteXLSX_LanguagesSeparatedByABlankRowAndAboutSheet(t *testing.T) {
	a := db.Analysis{
		Name:           "Quote batch",
		SourceLanguage: "en",
		CreatedAt:      pgtype.Timestamptz{Time: time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC), Valid: true},
		CompletedAt:    pgtype.Timestamptz{Time: time.Date(2026, 10, 1, 9, 31, 0, 0, time.UTC), Valid: true},
	}
	var buf bytes.Buffer
	if err := writeXLSX(&buf, a, exportRows(exportFixture()), time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if sheets := f.GetSheetList(); len(sheets) != 2 || sheets[0] != "Analysis" || sheets[1] != "About" {
		t.Fatalf("sheets: want [Analysis About], got %v", sheets)
	}
	rows, err := f.GetRows("Analysis")
	if err != nil {
		t.Fatal(err)
	}
	// header, de a, de b, blank, fr a — no total rows, no blank after the last.
	if len(rows) != 5 {
		t.Fatalf("rows: want 5, got %d: %v", len(rows), rows)
	}
	want := [][2]string{{"a.docx", "de-DE"}, {"b.docx", "de-DE"}, {"", ""}, {"a.docx", "fr-FR"}}
	for i, w := range want {
		r := append(rows[i+1], "", "")
		if r[0] != w[0] || r[1] != w[1] {
			t.Errorf("row %d: want %v, got %v", i+2, w, r[:2])
		}
	}
	if len(rows[3]) != 0 {
		t.Errorf("row 4 should be empty, got %v", rows[3])
	}
	for _, r := range rows {
		if len(r) > 0 && r[0] == "Total" {
			t.Errorf("unexpected total row: %v", r)
		}
	}
	tw := col(t, rows[0], "101% words")
	if rows[1][tw] != "50" {
		t.Errorf("de-DE a.docx 101%% words: want 50, got %s", rows[1][tw])
	}

	// Numbers are number cells, not text. excelize leaves the type of a
	// number cell unset (Excel's default is numeric).
	cell, err := excelize.CoordinatesToCellName(tw+1, 2)
	if err != nil {
		t.Fatal(err)
	}
	typ, err := f.GetCellType("Analysis", cell)
	if err != nil {
		t.Fatal(err)
	}
	if typ == excelize.CellTypeSharedString || typ == excelize.CellTypeInlineString {
		t.Errorf("number cell %s: want a number, got a text cell (type %v)", cell, typ)
	}

	about, err := f.GetRows("About")
	if err != nil {
		t.Fatal(err)
	}
	if about[0][1] != "Quote batch" || about[1][1] != "en" || about[4][1] != "2026-10-19 12:00 UTC" {
		t.Errorf("about sheet: %v", about)
	}
}

// A header fits its column when it fits on the lines the header row has room
// for: one line unless the cell wraps, and then every word must fit the
// column's width on its own. Widths are in characters of the default font,
// less one for the cell's padding; a line of 11pt text needs 15 points.
func headerFits(text string, width float64, wrap bool, rowHeight float64) bool {
	room := int(width) - 1
	if !wrap {
		return len([]rune(text)) <= room
	}
	lines, line := 1, 0
	for _, w := range strings.Fields(text) {
		n := len([]rune(w))
		if n > room {
			return false
		}
		switch {
		case line == 0:
			line = n
		case line+1+n <= room:
			line += 1 + n
		default:
			lines++
			line = n
		}
	}
	return float64(lines)*15 <= rowHeight
}

func TestWriteXLSX_EveryHeaderFitsItsColumn(t *testing.T) {
	var buf bytes.Buffer
	if err := writeXLSX(&buf, db.Analysis{}, exportRows(exportFixture()), time.Now()); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	height, err := f.GetRowHeight("Analysis", 1)
	if err != nil {
		t.Fatal(err)
	}
	for i, h := range exportHeader() {
		col, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			t.Fatal(err)
		}
		width, err := f.GetColWidth("Analysis", col)
		if err != nil {
			t.Fatal(err)
		}
		styleID, err := f.GetCellStyle("Analysis", col+"1")
		if err != nil {
			t.Fatal(err)
		}
		style, err := f.GetStyle(styleID)
		if err != nil {
			t.Fatal(err)
		}
		wrap := style.Alignment != nil && style.Alignment.WrapText
		if !headerFits(h, width, wrap, height) {
			t.Errorf("%s1 %q does not fit: width %.0f, wrap %v, row height %.0f", col, h, math.Round(width), wrap, height)
		}
	}
}

func TestWriteXLSX_FormulaLikeNameStaysText(t *testing.T) {
	var buf bytes.Buffer
	rows := exportRows([]db.JobAnalysis{{JobNameSnapshot: "=1+1", TargetLanguage: "de"}})
	if err := writeXLSX(&buf, db.Analysis{}, rows, time.Now()); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	formula, err := f.GetCellFormula("Analysis", "A2")
	if err != nil {
		t.Fatal(err)
	}
	if formula != "" {
		t.Errorf("A2 holds a formula %q; want plain text", formula)
	}
	if v, _ := f.GetCellValue("Analysis", "A2"); v != "=1+1" {
		t.Errorf("A2: want the name as text, got %q", v)
	}
}

func TestExportFilename(t *testing.T) {
	at := time.Date(2026, 10, 19, 23, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		f    exportFormat
		want string
	}{
		{"Analysis 2026-10-01", exportCSV, "Analysis 2026-10-01 2026-10-19.csv"},
		{"a/b: c?", exportXLSX, "a-b- c- 2026-10-19.xlsx"},
		{"   ", exportCSV, "analysis 2026-10-19.csv"},
	}
	for _, c := range cases {
		if got := exportFilename(c.name, c.f, at); got != c.want {
			t.Errorf("exportFilename(%q): want %q, got %q", c.name, c.want, got)
		}
	}
}

func TestParseExportFormat(t *testing.T) {
	for _, s := range []string{"csv", "xlsx"} {
		if _, ok := parseExportFormat(s); !ok {
			t.Errorf("%q should be accepted", s)
		}
	}
	for _, s := range []string{"", "CSV", "xls", "pdf"} {
		if _, ok := parseExportFormat(s); ok {
			t.Errorf("%q should be rejected", s)
		}
	}
}
