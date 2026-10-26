package xlsx

import (
	"bytes"
	"context"
	"errors"
	"openloc/internal/parsers/xlsx/xlsxtest"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"openloc/internal/parsers/common"
)

// layoutWorkbook: "Products" has a header row, an ID column, the English text
// in B and a note in C; "Notes" holds text in two columns without a header;
// "Lookup" is hidden.
func layoutWorkbook(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.SetSheetName("Sheet1", "Products"))
	for cell, v := range map[string]any{
		"A1": "ID", "B1": "English", "C1": "Note",
		"A2": "P-1", "B2": "Blue chair", "C2": "Bestseller",
		"A3": "P-2", "B3": "Red table",
		"A4": "P-3", "B4": 42,
	} {
		must(f.SetCellValue("Products", cell, v))
	}
	_, err := f.NewSheet("Notes")
	must(err)
	must(f.SetCellValue("Notes", "A1", "Handle with care"))
	must(f.SetCellValue("Notes", "B2", "Keep dry"))
	_, err = f.NewSheet("Lookup")
	must(err)
	must(f.SetCellValue("Lookup", "A1", "Hidden list"))
	must(f.SetSheetVisible("Lookup", false))
	var buf bytes.Buffer
	must(f.Write(&buf))
	return buf.Bytes()
}

// testLayout translates the English column of Products and the whole of Notes.
func testLayout() *XLSXLayout {
	return &XLSXLayout{Sheets: []XLSXSheetLayout{
		{Name: "Products", HeaderRow: true, Columns: []string{"B"}},
		{Name: "Notes"},
	}}
}

func segmentKeys(segs []common.Segment) string {
	var out []string
	for _, s := range segs {
		out = append(out, s.Key+"="+s.Text)
	}
	return strings.Join(out, "|")
}

func TestXLSXLayout_ParsesTheChosenColumnOrTheWholeSheet(t *testing.T) {
	segs, err := XLSX{Layout: testLayout()}.Parse(layoutWorkbook(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Products: the header row, the ID and note columns and the number stay
	// out. Notes: every text cell. Lookup is not listed.
	want := "Products!B2=Blue chair|Products!B3=Red table|Notes!A1=Handle with care|Notes!B2=Keep dry"
	if got := segmentKeys(segs); got != want {
		t.Errorf("segments %q, want %q", got, want)
	}
	for i, s := range segs {
		if s.Position != i {
			t.Errorf("segment %s position %d, want %d", s.Key, s.Position, i)
		}
	}
}

// Several columns read row by row, left to right.
func TestXLSXLayout_ParsesSeveralColumns(t *testing.T) {
	l := &XLSXLayout{Sheets: []XLSXSheetLayout{{Name: "Products", HeaderRow: true, Columns: []string{"C", "B"}}}}
	segs, err := XLSX{Layout: l}.Parse(layoutWorkbook(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := segmentKeys(segs), "Products!B2=Blue chair|Products!C2=Bestseller|Products!B3=Red table"; got != want {
		t.Errorf("segments %q, want %q", got, want)
	}
}

func TestXLSXLayout_WholeSheetSkipsAHeaderRow(t *testing.T) {
	l := &XLSXLayout{Sheets: []XLSXSheetLayout{{Name: "Notes", HeaderRow: true}}}
	segs, err := XLSX{Layout: l}.Parse(layoutWorkbook(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := segmentKeys(segs), "Notes!B2=Keep dry"; got != want {
		t.Errorf("segments %q, want %q", got, want)
	}
}

func TestXLSXLayout_NoTextIsAnError(t *testing.T) {
	l := &XLSXLayout{Sheets: []XLSXSheetLayout{{Name: "Products", HeaderRow: true, Columns: []string{"D"}}}}
	segs, err := (XLSX{Layout: l}).Parse(layoutWorkbook(t))
	var noText *common.XLSXNoTextError
	if !errors.As(err, &noText) {
		t.Fatalf("Parse = %+v, %v; want an *XLSXNoTextError", segs, err)
	}
	if want := "The chosen sheets and columns hold no text to translate."; noText.Msg != want {
		t.Errorf("message %q, want %q", noText.Msg, want)
	}
}

// Each translation goes back into its own cell, whatever the job's language.
func TestXLSXLayout_ExportWritesEachTranslationIntoItsCell(t *testing.T) {
	original := layoutWorkbook(t)
	tr := map[string]string{"Products!B2": "Blauer Stuhl", "Products!B3": "Roter Tisch", "Notes!B2": "Trocken lagern"}
	for _, lang := range []string{"de", "pt-BR"} {
		out, err := XLSX{Layout: testLayout()}.ReconstructForLang(context.Background(), original, tr, common.ExportLangs{Target: lang})
		if err != nil {
			t.Fatalf("export %s: %v", lang, err)
		}
		f, err := excelize.OpenReader(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct{ sheet, cell, want string }{
			{"Products", "B2", "Blauer Stuhl"},
			{"Products", "B3", "Roter Tisch"},
			{"Notes", "B2", "Trocken lagern"},
			{"Products", "B1", "English"},       // the header stays
			{"Products", "C2", "Bestseller"},    // a column not chosen stays
			{"Products", "B4", "42"},            // a number stays
			{"Notes", "A1", "Handle with care"}, // untranslated: keeps its source
			{"Lookup", "A1", "Hidden list"},     // a sheet not chosen stays
			{"Products", "D2", ""},              // nothing written beside the source
		} {
			if got, _ := f.GetCellValue(c.sheet, c.cell); got != c.want {
				t.Errorf("%s: %s!%s = %q, want %q", lang, c.sheet, c.cell, got, c.want)
			}
		}
		f.Close()
	}
}

func TestXLSXLayout_HasNoTargetsToImport(t *testing.T) {
	got, err := XLSX{Layout: testLayout()}.ExtractTargets(layoutWorkbook(t), []string{"fr"})
	if err != nil || len(got) != 0 {
		t.Errorf("ExtractTargets = %+v, %v; want nothing", got, err)
	}
}

func TestXLSXLayout_Validate(t *testing.T) {
	content := layoutWorkbook(t)
	if err := testLayout().Validate(content); err != nil {
		t.Fatalf("valid layout: %v", err)
	}
	for name, l := range map[string]XLSXLayout{
		"no sheets":         {},
		"hidden sheet":      {Sheets: []XLSXSheetLayout{{Name: "Lookup"}}},
		"unknown sheet":     {Sheets: []XLSXSheetLayout{{Name: "Nope", Columns: []string{"A"}}}},
		"bad source column": {Sheets: []XLSXSheetLayout{{Name: "Notes", Columns: []string{"1A"}}}},
		"column past XFD":   {Sheets: []XLSXSheetLayout{{Name: "Notes", Columns: []string{"XFE"}}}},
		"sheet twice":       {Sheets: []XLSXSheetLayout{{Name: "Notes"}, {Name: "Notes", Columns: []string{"A"}}}},
		"one bad of two":    {Sheets: []XLSXSheetLayout{{Name: "Notes", Columns: []string{"A", "1A"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			var le *common.XLSXLayoutError
			if err := l.Validate(content); !errors.As(err, &le) {
				t.Errorf("Validate = %v, want an *XLSXLayoutError", err)
			}
		})
	}
}

func TestParseXLSXLayout_NormalizesColumns(t *testing.T) {
	l, err := ParseXLSXLayout([]byte(`{"sheets":[{"name":"Products","columns":[" b ","C","B"]},{"name":"Notes"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(l.Sheets[0].Columns, ","); got != "B,C" || len(l.Sheets[1].Columns) != 0 {
		t.Errorf("layout = %+v, want columns B, C and a whole sheet", l.Sheets)
	}
	var le *common.XLSXLayoutError
	if _, err := ParseXLSXLayout([]byte(`{`)); !errors.As(err, &le) {
		t.Errorf("ParseXLSXLayout({) = %v, want an *XLSXLayoutError", err)
	}
}

func TestInspectXLSX_SuggestsALayout(t *testing.T) {
	in, err := InspectXLSX(layoutWorkbook(t))
	if err != nil {
		t.Fatalf("InspectXLSX: %v", err)
	}
	if in.Bilingual {
		t.Fatal("a workbook without language codes reads as bilingual")
	}
	// Hidden sheets are left out; each sheet lists the columns that hold
	// text (not the number column, not the empty ones), titled by row 1.
	var names []string
	for _, s := range in.Sheets {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "Products,Notes" {
		t.Errorf("sheets %q, want the visible ones in workbook order", names)
	}
	var cols []string
	for _, c := range in.Sheets[0].Columns {
		cols = append(cols, c.Letter+"="+c.Title)
	}
	if got, want := strings.Join(cols, ","), "A=ID,B=English,C=Note"; got != want {
		t.Errorf("Products columns %q, want %q", got, want)
	}
	if got := in.Sheets[1].Columns; len(got) != 2 || got[0].Letter != "A" || got[1].Letter != "B" || got[1].Title != "" {
		t.Errorf("Notes columns %+v, want A titled by its row 1 text and B untitled", got)
	}
	// The column with the most text is the source (B, not the ID column);
	// hidden and textless sheets are left out.
	s := in.Suggested
	if s == nil || len(s.Sheets) != 2 || s.Sheets[0].Name != "Products" || s.Sheets[1].Name != "Notes" {
		t.Fatalf("suggested = %+v", s)
	}
	if p := s.Sheets[0]; !p.HeaderRow || strings.Join(p.Columns, ",") != "B" {
		t.Errorf("Products suggestion = %+v", p)
	}
	if n := s.Sheets[1]; n.HeaderRow || strings.Join(n.Columns, ",") != "A" {
		t.Errorf("Notes suggestion = %+v", n)
	}
	if err := s.Validate(layoutWorkbook(t)); err != nil {
		t.Errorf("the suggestion does not validate: %v", err)
	}
}

func TestInspectXLSX_BilingualNeedsNoLayout(t *testing.T) {
	in, err := InspectXLSX(xlsxtest.BuildXLSX(t, []string{"en", "fr"}, [][]string{{"Hello", "Bonjour"}}))
	if err != nil || !in.Bilingual || in.Suggested != nil {
		t.Errorf("inspect = %+v, %v; want bilingual and no suggestion", in, err)
	}
}

// A workbook with language codes in row 1 is read by them; a layout would
// read its target columns as source text and overwrite the source on export.
func TestXLSXLayout_RefusedForABilingualWorkbook(t *testing.T) {
	content := xlsxtest.BuildXLSX(t, []string{"en", "fr"}, [][]string{{"Hello", "Bonjour"}})
	l := &XLSXLayout{Sheets: []XLSXSheetLayout{{Name: "Sheet1", Columns: []string{"A"}}}}
	segs, err := XLSX{Layout: l}.Parse(content)
	var le *common.XLSXLayoutError
	if !errors.As(err, &le) {
		t.Fatalf("Parse = %+v, %v; want an *XLSXLayoutError", segs, err)
	}
	if want := "This workbook has language codes in row 1, so it is read by them. Upload it without a column layout."; le.Msg != want {
		t.Errorf("message %q, want %q", le.Msg, want)
	}
}

// Some short headings are language codes too: "ID" is Indonesian, "Cat"
// Catalan. A layout is refused only when it would read a column under a code
// as source text: a chosen column there, or the whole first sheet. Other
// columns stay choosable.
func TestXLSXLayout_CodeLikeHeadingsLeaveOtherColumnsChoosable(t *testing.T) {
	catalog := xlsxtest.BuildXLSX(t, []string{"ID", "Name", "Cat"}, [][]string{{"P-1", "Blue chair", "C-1"}, {"P-2", "Red table", "C-2"}})
	l := &XLSXLayout{Sheets: []XLSXSheetLayout{{Name: "Sheet1", HeaderRow: true, Columns: []string{"B"}}}}
	segs, err := XLSX{Layout: l}.Parse(catalog)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := segmentKeys(segs), "Sheet1!B2=Blue chair|Sheet1!B3=Red table"; got != want {
		t.Errorf("segments %q, want %q", got, want)
	}

	bilingual := xlsxtest.BuildXLSX(t, []string{"en", "fr"}, [][]string{{"Hello", "Bonjour"}})
	whole := &XLSXLayout{Sheets: []XLSXSheetLayout{{Name: "Sheet1"}}}
	var le *common.XLSXLayoutError
	if _, err := (XLSX{Layout: whole}).Parse(bilingual); !errors.As(err, &le) {
		t.Errorf("whole first sheet of a bilingual workbook = %v, want an *XLSXLayoutError", err)
	}
}

// A heading counts as a language code only when it names a language: "SKU"
// does not, so a catalog headed "ID | Name | SKU" has one code and is read
// cell by cell, with its columns offered.
func TestXLSXInspect_CatalogHeadingsAreNotLanguageCodes(t *testing.T) {
	catalog := xlsxtest.BuildXLSX(t, []string{"ID", "Name", "SKU"}, [][]string{{"P-1", "Blue chair", "S-1"}, {"P-2", "Red table", "S-2"}})
	in, err := InspectXLSX(catalog)
	if err != nil {
		t.Fatalf("InspectXLSX: %v", err)
	}
	if in.Bilingual {
		t.Fatal("inspection is bilingual; want the columns offered")
	}
	if in.Suggested == nil || len(in.Suggested.Sheets) != 1 || strings.Join(in.Suggested.Sheets[0].Columns, ",") != "B" {
		t.Errorf("suggested = %+v, want column B (Name)", in.Suggested)
	}
	segs, err := XLSX{}.Parse(catalog)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(segs) == 0 || segs[0].Key != "Sheet1!A1" {
		t.Errorf("segments %q, want every text cell from Sheet1!A1", segmentKeys(segs))
	}
}
