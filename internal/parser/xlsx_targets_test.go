package parser

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// multilingualWorkbook: "Catalog" has a header row (titles, not language
// codes), an ID in A, the source in B and empty French, German and Spanish
// columns in C, D and E, except an old German text in D2.
func multilingualWorkbook(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.SetSheetName("Sheet1", "Catalog"))
	for cell, v := range map[string]any{
		"A1": "ID", "B1": "Name", "C1": "French", "D1": "German", "E1": "Spanish",
		"A2": "P-1", "B2": "Blue chair", "D2": "Alter Stuhl",
		"A3": "P-2", "B3": "Red table",
	} {
		must(f.SetCellValue("Catalog", cell, v))
	}
	var buf bytes.Buffer
	must(f.Write(&buf))
	return buf.Bytes()
}

func multilingualLayout() *XLSXLayout {
	return &XLSXLayout{Sheets: []XLSXSheetLayout{{
		Name: "Catalog", HeaderRow: true, Columns: []string{"B"},
		TargetColumns: []XLSXTargetColumn{{Language: "fr", Column: "C"}, {Language: "de", Column: "D"}, {Language: "es", Column: "E"}},
	}}}
}

func cellValues(t *testing.T, content []byte, sheet string, cells ...string) string {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	for _, c := range cells {
		v, err := f.GetCellValue(sheet, c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, c+"="+v)
	}
	return strings.Join(out, "|")
}

func TestXLSXTargets_ImportReadsOnlyTheSourceColumn(t *testing.T) {
	segs, err := XLSX{Layout: multilingualLayout()}.Parse(multilingualWorkbook(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// The old German text in D2 and the column titles are not source text.
	if got, want := segmentKeys(segs), "Catalog!B2=Blue chair|Catalog!B3=Red table"; got != want {
		t.Errorf("segments %q, want %q", got, want)
	}
}

func TestXLSXTargets_ExportWritesEachLanguageIntoItsColumn(t *testing.T) {
	content := multilingualWorkbook(t)
	p := XLSX{Layout: multilingualLayout()}
	for _, tc := range []struct {
		lang         string
		translations map[string]string
		want         string
	}{
		{"fr", map[string]string{"Catalog!B2": "Chaise bleue", "Catalog!B3": "Table rouge"},
			"B2=Blue chair|B3=Red table|C2=Chaise bleue|C3=Table rouge|D2=Alter Stuhl|D3=|E2=|E3="},
		{"de", map[string]string{"Catalog!B2": "Blauer Stuhl", "Catalog!B3": "Roter Tisch"},
			"B2=Blue chair|B3=Red table|C2=|C3=|D2=Blauer Stuhl|D3=Roter Tisch|E2=|E3="},
		// A regional job finds its base language's column.
		{"es-MX", map[string]string{"Catalog!B2": "Silla azul"},
			"B2=Blue chair|B3=Red table|C2=|C3=|D2=Alter Stuhl|D3=|E2=Silla azul|E3="},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			out, err := p.ReconstructForLang(context.Background(), content, tc.translations, ExportLangs{Source: "en", Target: tc.lang})
			if err != nil {
				t.Fatalf("ReconstructForLang: %v", err)
			}
			got := cellValues(t, out, "Catalog", "B2", "B3", "C2", "C3", "D2", "D3", "E2", "E3")
			if got != tc.want {
				t.Errorf("cells %q, want %q", got, tc.want)
			}
			if title := cellValues(t, out, "Catalog", "C1"); title != "C1=French" {
				t.Errorf("header %q changed", title)
			}
		})
	}
}

func TestXLSXTargets_ExportRefusesALanguageWithoutAColumn(t *testing.T) {
	p := XLSX{Layout: multilingualLayout()}
	_, err := p.ReconstructForLang(context.Background(), multilingualWorkbook(t),
		map[string]string{"Catalog!B2": "Blå stol"}, ExportLangs{Source: "en", Target: "sv"})
	var ee *ExportError
	if !errors.As(err, &ee) {
		t.Fatalf("err %v, want an ExportError", err)
	}
	for _, part := range []string{`no column on sheet "Catalog"`, "de, es, fr", "maps sv to a column"} {
		if !strings.Contains(ee.Msg, part) {
			t.Errorf("message %q lacks %q", ee.Msg, part)
		}
	}
}

func TestXLSXTargets_ASheetWithoutTargetsStillWritesInPlace(t *testing.T) {
	content := multilingualWorkbook(t)
	l := multilingualLayout()
	l.Sheets[0].TargetColumns = nil
	out, err := XLSX{Layout: l}.ReconstructForLang(context.Background(), content,
		map[string]string{"Catalog!B2": "Chaise bleue"}, ExportLangs{Source: "en", Target: "sv"})
	if err != nil {
		t.Fatalf("ReconstructForLang: %v", err)
	}
	if got := cellValues(t, out, "Catalog", "B2", "C2"); got != "B2=Chaise bleue|C2=" {
		t.Errorf("cells %q", got)
	}
}

func TestXLSXTargets_ImportsTextAlreadyInALanguageColumn(t *testing.T) {
	got, err := XLSX{Layout: multilingualLayout()}.ExtractTargets(multilingualWorkbook(t), []string{"de", "fr", "de-AT", "sv"})
	if err != nil {
		t.Fatalf("ExtractTargets: %v", err)
	}
	// D2 holds German for B2; C is empty; de-AT reads the de column; sv has
	// no column and is left out.
	if d := got["de"]; len(d) != 1 || d["Catalog!B2"].Text != "Alter Stuhl" {
		t.Errorf("de targets = %+v, want Catalog!B2 = Alter Stuhl", d)
	}
	if fr, ok := got["fr"]; !ok || len(fr) != 0 {
		t.Errorf("fr targets = %+v (present %v), want an empty map", fr, ok)
	}
	if at := got["de-AT"]; at["Catalog!B2"].Text != "Alter Stuhl" {
		t.Errorf("de-AT targets = %+v, want the de column", at)
	}
	if _, ok := got["sv"]; ok {
		t.Error("sv has no column but got targets")
	}

	// A layout without target columns still writes over its source cells:
	// there is nothing to import.
	l := multilingualLayout()
	l.Sheets[0].TargetColumns = nil
	if got, err := (XLSX{Layout: l}).ExtractTargets(multilingualWorkbook(t), []string{"de"}); err != nil || got != nil {
		t.Errorf("without target columns: %v, %v; want nothing", got, err)
	}
}

func TestValidateXLSXTargets(t *testing.T) {
	fr := XLSXTargetColumn{Language: "fr", Column: "C"}
	for _, tc := range []struct {
		name    string
		columns []string
		targets []XLSXTargetColumn
		want    string
	}{
		{"none", nil, nil, ""},
		{"one source", []string{"B"}, []XLSXTargetColumn{fr, {Language: "pt-br", Column: "AA"}}, ""},
		{"no source", nil, []XLSXTargetColumn{fr}, "exactly one source column"},
		{"two sources", []string{"A", "B"}, []XLSXTargetColumn{fr}, "exactly one source column"},
		{"over the source", []string{"C"}, []XLSXTargetColumn{fr}, "Column C holds the source text, so fr cannot"},
		{"unknown language", []string{"B"}, []XLSXTargetColumn{{Language: "sku", Column: "C"}}, `"sku" is not a language code`},
		{"missing language", []string{"B"}, []XLSXTargetColumn{{Column: "C"}}, `"" is not a language code`},
		{"bad column", []string{"B"}, []XLSXTargetColumn{{Language: "fr", Column: "C1"}}, `"C1" is not a column`},
		{"past the last column", []string{"B"}, []XLSXTargetColumn{{Language: "fr", Column: "XFE"}}, `"XFE" is not a column`},
		{"language twice", []string{"B"}, []XLSXTargetColumn{fr, {Language: "fr", Column: "D"}}, "fr has two target columns"},
		{"column twice", []string{"B"}, []XLSXTargetColumn{fr, {Language: "de", Column: "C"}}, "Column C is the target of both fr and de"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateXLSXTargets(tc.columns, tc.targets)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("err %v, want none", err)
				}
				return
			}
			var le *XLSXLayoutError
			if !errors.As(err, &le) || !strings.Contains(le.Msg, tc.want) {
				t.Errorf("err %v, want a layout error with %q", err, tc.want)
			}
		})
	}
}

func TestValidateXLSXTargets_Cap(t *testing.T) {
	targets := make([]XLSXTargetColumn, MaxXLSXTargetColumns+1)
	if err := ValidateXLSXTargets([]string{"A"}, targets); err == nil || !strings.Contains(err.Error(), "At most 100") {
		t.Errorf("err %v, want the cap", err)
	}
}

func TestXLSXLayout_NormalizesAndValidatesTargets(t *testing.T) {
	l, err := ParseXLSXLayout([]byte(`{"sheets":[{"name":"Catalog","header_row":true,"columns":["b"],"target_columns":[{"language":" FR ","column":"c"},{"language":"pt_BR","column":" d "},{"language":"","column":""}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := l.Sheets[0].TargetColumns
	want := []XLSXTargetColumn{{Language: "fr", Column: "C"}, {Language: "pt-br", Column: "D"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("targets %v, want %v", got, want)
	}
	if err := l.Validate(multilingualWorkbook(t)); err != nil {
		t.Errorf("Validate: %v", err)
	}
	l.Sheets[0].TargetColumns = append(l.Sheets[0].TargetColumns, XLSXTargetColumn{Language: "de", Column: "B"})
	err = l.Validate(multilingualWorkbook(t))
	var le *XLSXLayoutError
	if !errors.As(err, &le) || le.Msg != `Sheet "Catalog": Column B holds the source text, so de cannot be written into it.` {
		t.Errorf("Validate err %v", err)
	}
}

func TestInspectXLSX_LayoutForCarriesTargets(t *testing.T) {
	in, err := InspectXLSX(multilingualWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	l := in.LayoutFor(nil, true, []string{"b"}, []XLSXTargetColumn{{Language: "FR", Column: "c"}})
	if l == nil || len(l.Sheets) != 1 {
		t.Fatalf("layout %+v", l)
	}
	s := l.Sheets[0]
	if s.Name != "Catalog" || !s.HeaderRow || len(s.Columns) != 1 || s.Columns[0] != "B" ||
		len(s.TargetColumns) != 1 || s.TargetColumns[0] != (XLSXTargetColumn{Language: "fr", Column: "C"}) {
		t.Errorf("sheet %+v", s)
	}
}

func TestXLSXTargets_CheckSeparateColumns(t *testing.T) {
	l := multilingualLayout()
	if err := l.CheckSeparateColumns([]string{"fr", "de", "es"}); err != nil {
		t.Errorf("fr, de, es: %v", err)
	}
	// fr-CA has no column and writes into fr's.
	err := l.CheckSeparateColumns([]string{"fr", "fr-CA"})
	var ee *ExportError
	if !errors.As(err, &ee) || !strings.Contains(ee.Msg, `fr and fr-ca both go into column C on sheet "Catalog"`) {
		t.Errorf("fr, fr-CA: err %v, want the shared-column message", err)
	}
	// A language with no column at all is the no-column message.
	if err := l.CheckSeparateColumns([]string{"fr", "sv"}); !errors.As(err, &ee) || !strings.Contains(ee.Msg, "gives sv no column") {
		t.Errorf("fr, sv: err %v, want the no-column message", err)
	}
}
