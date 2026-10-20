package parser

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// A workbook without language codes in row 1 is translated cell by cell:
// every text cell of every visible sheet is a segment, and the export writes
// each translation back into its own cell.

// cellsWorkbook builds the workbook the tests below share. Sheet1 mixes text
// with cells that are not text; Sheet2 is a second visible tab; "Lookup" is
// hidden.
func cellsWorkbook(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	set := func(sheet, cell string, v any) {
		t.Helper()
		if err := f.SetCellValue(sheet, cell, v); err != nil {
			t.Fatalf("set %s!%s: %v", sheet, cell, err)
		}
	}
	set("Sheet1", "A1", "Title")
	set("Sheet1", "B1", "Notes")
	set("Sheet1", "A2", "Hello world")
	set("Sheet1", "B2", 42) // number
	set("Sheet1", "C2", "Thank you")
	set("Sheet1", "A3", time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)) // date
	set("Sheet1", "B3", true)                                          // boolean
	set("Sheet1", "C3", "00123")                                       // text with no letters
	if err := f.SetCellFormula("Sheet1", "A4", `CONCATENATE(A2," again")`); err != nil {
		t.Fatalf("formula: %v", err)
	}
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		t.Fatalf("style: %v", err)
	}
	if err := f.SetCellStyle("Sheet1", "A2", "A2", bold); err != nil {
		t.Fatalf("set style: %v", err)
	}

	if _, err := f.NewSheet("Sheet2"); err != nil {
		t.Fatalf("new sheet: %v", err)
	}
	set("Sheet2", "B3", "Second tab text")

	if _, err := f.NewSheet("Lookup"); err != nil {
		t.Fatalf("new sheet: %v", err)
	}
	set("Lookup", "A1", "Hidden code list")
	if err := f.SetSheetVisible("Lookup", false); err != nil {
		t.Fatalf("hide sheet: %v", err)
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	return buf.Bytes()
}

func TestXLSXCells_ParsesEveryTextCellOfEveryVisibleSheet(t *testing.T) {
	segs, err := XLSX{}.Parse(cellsWorkbook(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []struct{ key, text string }{
		{"Sheet1!A1", "Title"},
		{"Sheet1!B1", "Notes"},
		{"Sheet1!A2", "Hello world"},
		{"Sheet1!C2", "Thank you"},
		{"Sheet2!B3", "Second tab text"},
	}
	if len(segs) != len(want) {
		t.Fatalf("got %d segments %+v, want %d (numbers, dates, booleans, formulas, letterless text and the hidden sheet skipped)", len(segs), segs, len(want))
	}
	for i, w := range want {
		s := segs[i]
		if s.Key != w.key || s.Text != w.text || s.Position != i {
			t.Errorf("segment %d = {%s %q pos %d}, want {%s %q pos %d}", i, s.Key, s.Text, s.Position, w.key, w.text, i)
		}
	}
	var meta struct{ Sheet, Cell string }
	if err := json.Unmarshal([]byte(segs[4].Context), &meta); err != nil || meta.Sheet != "Sheet2" || meta.Cell != "B3" {
		t.Errorf("context = %q, want the sheet and cell (err %v)", segs[4].Context, err)
	}
}

func TestXLSXCells_ExportWritesEachTranslationIntoItsCell(t *testing.T) {
	original := cellsWorkbook(t)
	segs, err := XLSX{}.Parse(original)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	translations := map[string]string{}
	for _, s := range segs {
		translations[s.Key] = "ES " + s.Text
	}

	// The job's language plays no part: there are no language columns.
	for name, export := range map[string]func() ([]byte, error){
		"Reconstruct": func() ([]byte, error) { return XLSX{}.Reconstruct(original, translations) },
		"ReconstructForLang": func() ([]byte, error) {
			return XLSX{}.ReconstructForLang(context.Background(), original, translations, ExportLangs{Target: "es"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := export()
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			f, err := excelize.OpenReader(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer f.Close()
			get := func(sheet, cell string) string {
				v, _ := f.GetCellValue(sheet, cell)
				return v
			}
			for _, c := range []struct{ sheet, cell, want string }{
				{"Sheet1", "A1", "ES Title"},
				{"Sheet1", "B1", "ES Notes"},
				{"Sheet1", "A2", "ES Hello world"},
				{"Sheet1", "C2", "ES Thank you"},
				{"Sheet2", "B3", "ES Second tab text"},
				{"Sheet1", "B2", "42"},               // untouched number
				{"Sheet1", "C3", "00123"},            // untouched letterless text
				{"Lookup", "A1", "Hidden code list"}, // untouched hidden sheet
			} {
				if got := get(c.sheet, c.cell); got != c.want {
					t.Errorf("%s!%s = %q, want %q", c.sheet, c.cell, got, c.want)
				}
			}
			if formula, _ := f.GetCellFormula("Sheet1", "A4"); formula == "" {
				t.Error("Sheet1!A4 lost its formula")
			}
			if style, _ := f.GetCellStyle("Sheet1", "A2"); style == 0 {
				t.Error("Sheet1!A2 lost its bold style")
			}
			if got := get("Sheet1", "D2"); got != "" {
				t.Errorf("Sheet1!D2 = %q, want nothing written next to the source", got)
			}
		})
	}
}

// The key splits at its last "!", so a sheet name may contain one.
func TestXLSXCells_SheetNameWithExclamationMark(t *testing.T) {
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "Q&A!"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := f.SetCellValue("Q&A!", "B2", "Any questions?"); err != nil {
		t.Fatalf("set: %v", err)
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}

	segs, err := XLSX{}.Parse(buf.Bytes())
	if err != nil || len(segs) != 1 || segs[0].Key != "Q&A!!B2" {
		t.Fatalf("Parse = %+v, %v; want one segment keyed Q&A!!B2", segs, err)
	}
	out, err := XLSX{}.Reconstruct(buf.Bytes(), map[string]string{segs[0].Key: "¿Preguntas?"})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	g, err := excelize.OpenReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()
	if got, _ := g.GetCellValue("Q&A!", "B2"); got != "¿Preguntas?" {
		t.Errorf("Q&A!!B2 = %q, want the translation", got)
	}
}

func TestXLSXCells_NoTextCellsIsAnError(t *testing.T) {
	f := excelize.NewFile()
	if err := f.SetCellValue("Sheet1", "A1", 1); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := f.SetCellValue("Sheet1", "B1", 2.5); err != nil {
		t.Fatalf("set: %v", err)
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	segs, err := (XLSX{}).Parse(buf.Bytes())
	var noText *XLSXNoTextError
	if !errors.As(err, &noText) {
		t.Fatalf("Parse = %+v, %v; want an *XLSXNoTextError", segs, err)
	}
	if want := "The workbook has no text to translate on its visible sheets."; noText.Msg != want {
		t.Errorf("message %q, want %q", noText.Msg, want)
	}
}

// A cell stored as t="str" without a formula is a plain string (SheetJS writes
// strings this way); with a formula it is the formula's computed value.
func TestXLSXCells_PlainStrCellIsText(t *testing.T) {
	content := withSheetXML(t, buildXLSX(t, []string{"Start"}, nil), appendRows(
		`<row r="2"><c r="A2" t="str"><v>Plain string cell</v></c>`+
			`<c r="B2" t="str"><f>"Formula"&amp;" text"</f><v>Formula text</v></c></row>`))

	segs, err := XLSX{}.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := segmentKeys(segs), "Sheet1!A1=Start|Sheet1!A2=Plain string cell"; got != want {
		t.Fatalf("segments %q, want %q", got, want)
	}

	out, err := XLSX{}.Reconstruct(content, map[string]string{"Sheet1!A2": "Celda de texto"})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if got := readCells(t, out, "A2")["A2"]; got != "Celda de texto" {
		t.Errorf("A2 = %q, want the translation", got)
	}
}

// The cell scan finds a worksheet through the workbook's relationships, not
// its usual path, and places a cell without an r attribute by its position.
func TestXLSXCells_SheetAtAnotherPathAndCellWithoutReference(t *testing.T) {
	content := withSheetMovedTo(t, buildXLSX(t, []string{"Start"}, nil), "xl/worksheets/moved.xml", appendRows(
		`<row r="2"><c t="inlineStr"><is><t>No reference</t></is></c><c t="inlineStr"><is><t>Second</t></is></c></row>`))

	segs, err := XLSX{}.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := segmentKeys(segs), "Sheet1!A1=Start|Sheet1!A2=No reference|Sheet1!B2=Second"; got != want {
		t.Fatalf("segments %q, want %q", got, want)
	}
}

// HTML in a cell becomes tags unless the file's strings are plain text; either
// way the export writes the cell back in the form it was read.
func TestXLSXCells_PlainTextSetting(t *testing.T) {
	content := buildXLSX(t, []string{"<b>Hello</b> world"}, nil)

	html, err := XLSX{}.Parse(content)
	if err != nil || len(html) != 1 || html[0].Text != "Hello world" || html[0].RichText == "" {
		t.Fatalf("HTML mode = %+v, %v; want the text with its tags as rich text", html, err)
	}
	// The translation keeps the source's tags around new words.
	translated := strings.Replace(strings.Replace(html[0].RichText, "Hello", "Hola", 1), "world", "mundo", 1)
	if !strings.Contains(translated, "Hola") || !strings.Contains(translated, "mundo") {
		t.Fatalf("rich text %q does not hold the words as text", html[0].RichText)
	}
	out, err := XLSX{}.Reconstruct(content, map[string]string{"Sheet1!A1": translated})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if got := readCells(t, out, "A1")["A1"]; got != "<b>Hola</b> mundo" {
		t.Errorf("HTML mode export A1 = %q, want the translation with its tags as HTML", got)
	}

	plain, err := XLSX{PlainText: true}.Parse(content)
	if err != nil || len(plain) != 1 || plain[0].Text != "<b>Hello</b> world" || plain[0].RichText != "" {
		t.Fatalf("plain-text mode = %+v, %v; want the cell text as is", plain, err)
	}
	out, err = XLSX{PlainText: true}.Reconstruct(content, map[string]string{"Sheet1!A1": "<b>Hola</b> mundo"})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if got := readCells(t, out, "A1")["A1"]; got != "<b>Hola</b> mundo" {
		t.Errorf("plain-text export A1 = %q, want the translation as typed", got)
	}
}

// withSheetsSharingPart adds n sheets to the workbook that all point at the
// first sheet's worksheet part, through target.
func withSheetsSharingPart(t *testing.T, content []byte, n int, target string) []byte {
	t.Helper()
	var sheets, rels strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sheets, `<sheet name="Copy%d" sheetId="%d" r:id="rIdCopy%d"></sheet>`, i, 100+i, i)
		fmt.Fprintf(&rels, `<Relationship Id="rIdCopy%d" Target="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet"></Relationship>`, i, target)
	}
	return rewriteZip(t, content, func(w *zip.Writer, f *zip.File) bool {
		var body, at, add string
		switch f.Name {
		case "xl/workbook.xml":
			at, add = "</sheets>", sheets.String()
		case "xl/_rels/workbook.xml.rels":
			at, add = "</Relationships>", rels.String()
		default:
			return false
		}
		if body = readPart(t, f); !strings.Contains(body, at) {
			t.Fatalf("%s has no %s", f.Name, at)
		}
		writePart(t, w, f.Name, strings.Replace(body, at, add+at, 1))
		return true
	})
}

// Two sheets never share a worksheet part in a real workbook. Read as is, a
// small file listing thousands of such sheets would have the one part
// scanned again for each, so every entry point refuses it.
func TestXLSX_SheetsSharingAPartAreMalformed(t *testing.T) {
	// excelize reads a backslash as a path separator, so a target spelled
	// with one names the same part.
	for target, spelling := range map[string]string{
		"worksheets/sheet1.xml":     "slashes",
		`worksheets\sheet1.xml`:     "backslashes",
		`/xl\worksheets/sheet1.xml`: "absolute, mixed",
	} {
		// One more sheet, so it shares the part only with Sheet1, through its
		// own spelling.
		content := withSheetsSharingPart(t, buildXLSX(t, []string{"Start"}, [][]string{{"Hello"}}), 1, target)
		layout := &XLSXLayout{Sheets: []XLSXSheetLayout{{Name: "Sheet1"}}}
		for name, call := range map[string]func() error{
			"Parse":             func() error { _, err := XLSX{}.Parse(content); return err },
			"Parse with layout": func() error { _, err := XLSX{Layout: layout}.Parse(content); return err },
			"InspectXLSX":       func() error { _, err := InspectXLSX(content); return err },
			"Validate":          func() error { return layout.Validate(content) },
			"Reconstruct":       func() error { _, err := XLSX{}.Reconstruct(content, map[string]string{"Sheet1!A1": "x"}); return err },
		} {
			t.Run(spelling+"/"+name, func(t *testing.T) {
				if err := call(); !errors.Is(err, ErrXLSXMalformed) {
					t.Errorf("err = %v, want ErrXLSXMalformed", err)
				}
			})
		}
	}
}

// Some zip writers store part names with backslashes ("xl\workbook.xml").
// excelize reads them as slashes, and so does every check before it.
func TestXLSX_BackslashPartNamesStillOpen(t *testing.T) {
	original := buildXLSX(t, []string{"Start"}, [][]string{{"Hello"}})
	content := rewriteZip(t, original, func(w *zip.Writer, f *zip.File) bool {
		if !strings.Contains(f.Name, "/") {
			return false
		}
		writePart(t, w, strings.ReplaceAll(f.Name, "/", "\\"), readPart(t, f))
		return true
	})
	if !hasPart(t, content, "xl\\workbook.xml") {
		t.Fatal("the rewrite kept the slash part names")
	}

	segs, err := XLSX{}.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := segmentKeys(segs), "Sheet1!A1=Start|Sheet1!A2=Hello"; got != want {
		t.Fatalf("segments %q, want %q", got, want)
	}
	if _, err := InspectXLSX(content); err != nil {
		t.Errorf("InspectXLSX: %v", err)
	}
	out, err := XLSX{}.Reconstruct(content, map[string]string{"Sheet1!A2": "Hola"})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if got := readCells(t, out, "A2")["A2"]; got != "Hola" {
		t.Errorf("A2 = %q, want the translation", got)
	}
}
