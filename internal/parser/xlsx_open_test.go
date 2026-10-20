package parser

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

const sheetPart = "xl/worksheets/sheet1.xml"

// xlsxEntryPoints are every exported function that opens an XLSX with
// excelize; each one must apply the same guards.
var xlsxEntryPoints = map[string]func([]byte) error{
	"Parse": func(c []byte) error { _, err := XLSX{}.Parse(c); return err },
	"Reconstruct": func(c []byte) error {
		_, err := XLSX{}.Reconstruct(c, map[string]string{"row_2": "x"})
		return err
	},
	"ReconstructForLang": func(c []byte) error {
		_, err := XLSX{}.ReconstructForLang(context.Background(), c, map[string]string{"row_2": "x"}, ExportLangs{Target: "fr"})
		return err
	},
	"ExtractXLSXTargets": func(c []byte) error { _, err := ExtractXLSXTargets(c, []string{"fr"}); return err },
	"Parse with layout":  func(c []byte) error { _, err := XLSX{Layout: guardLayout}.Parse(c); return err },
	"ReconstructForLang with layout": func(c []byte) error {
		_, err := XLSX{Layout: guardLayout}.ReconstructForLang(context.Background(), c, map[string]string{"Sheet1!A2": "x"}, ExportLangs{Target: "fr"})
		return err
	},
	"InspectXLSX": func(c []byte) error { _, err := InspectXLSX(c); return err },
}

// xlsxCheckedEntryPoints run checkXLSX without opening the workbook: the
// crafted-workbook guards apply to them, an excelize panic cannot reach them.
var xlsxCheckedEntryPoints = map[string]func([]byte) error{
	"Validate layout": func(c []byte) error { return guardLayout.Validate(c) },
}

// allXLSXEntryPoints is every entry point the crafted-workbook guards cover.
func allXLSXEntryPoints() map[string]func([]byte) error {
	all := make(map[string]func([]byte) error, len(xlsxEntryPoints)+len(xlsxCheckedEntryPoints))
	for k, v := range xlsxEntryPoints {
		all[k] = v
	}
	for k, v := range xlsxCheckedEntryPoints {
		all[k] = v
	}
	return all
}

// guardLayout reads the guards' one-sheet workbooks by layout.
var guardLayout = &XLSXLayout{Sheets: []XLSXSheetLayout{{Name: "Sheet1", Columns: []string{"A"}}}}

// withSheetXML rewrites the first worksheet's XML.
func withSheetXML(t *testing.T, content []byte, edit func(string) string) []byte {
	t.Helper()
	return rewriteZip(t, content, func(w *zip.Writer, f *zip.File) bool {
		if f.Name != sheetPart {
			return false
		}
		in := readPart(t, f)
		out := edit(in)
		if out == in {
			t.Fatalf("sheet edit changed nothing")
		}
		writePart(t, w, f.Name, out)
		return true
	})
}

// withSheetMovedTo moves the first worksheet to part `name` (editing its XML
// with edit) and points the workbook relationship and content type at it.
func withSheetMovedTo(t *testing.T, content []byte, name string, edit func(string) string) []byte {
	t.Helper()
	replaceOnce := func(s, old, new string) string {
		if !strings.Contains(s, old) {
			t.Fatalf("%q not found", old)
		}
		return strings.Replace(s, old, new, 1)
	}
	return rewriteZip(t, content, func(w *zip.Writer, f *zip.File) bool {
		switch f.Name {
		case sheetPart:
			writePart(t, w, name, edit(readPart(t, f)))
		case "xl/_rels/workbook.xml.rels":
			writePart(t, w, f.Name, replaceOnce(readPart(t, f), `Target="worksheets/sheet1.xml"`, `Target="`+strings.TrimPrefix(name, "xl/")+`"`))
		case "[Content_Types].xml":
			writePart(t, w, f.Name, replaceOnce(readPart(t, f), `PartName="/xl/worksheets/sheet1.xml"`, `PartName="/`+name+`"`))
		default:
			return false
		}
		return true
	})
}

// readPart returns a zip entry's unzipped bytes.
func readPart(t *testing.T, f *zip.File) string {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// writePart adds an entry named name holding body.
func writePart(t *testing.T, w *zip.Writer, name, body string) {
	t.Helper()
	fw, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
}

func withDeclaredSheetSize(t *testing.T, content []byte, size uint64) []byte {
	t.Helper()
	return withDeclaredSizes(t, content, map[string]uint64{sheetPart: size})
}

func appendRows(rows string) func(string) string {
	return func(s string) string { return strings.Replace(s, "</sheetData>", rows+"</sheetData>", 1) }
}

func TestXLSXGuards_RejectCraftedWorkbooks(t *testing.T) {
	valid := buildXLSX(t, []string{"en", "fr"}, [][]string{{"Hello", "Bonjour"}})
	pastLastRow := `<row r="1048577">` + `<c r="A1048577" t="inlineStr"><is><t>x</t></is></c></row>`
	cells := func(ref string) string {
		return fmt.Sprintf(`<c r="%s" t="inlineStr"><is><t>x</t></is></c>`, ref)
	}

	cases := []struct {
		name    string
		content []byte
		want    error
	}{
		{
			name:    "encrypted (OLE/CFB container)",
			content: append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 4096)...),
			want:    ErrXLSXEncrypted,
		},
		{
			name:    "Excel 97-2003 workbook (Workbook stream)",
			content: cfbFile("Workbook"),
			want:    ErrXLSXLegacy,
		},
		{
			name:    "Excel 5 workbook (Book stream)",
			content: cfbFile("Book"),
			want:    ErrXLSXLegacy,
		},
		{
			name:    "zip64 size that overflows int64",
			content: withDeclaredSheetSize(t, valid, 1<<63),
			want:    ErrXLSXTooLarge,
		},
		{
			name:    "declared size just over the unzip limit",
			content: withDeclaredSheetSize(t, valid, maxOOXMLUnzipped+1),
			want:    ErrXLSXTooLarge,
		},
		{
			name: "two parts each under the unzip limit, together over it",
			content: withDeclaredSizes(t, valid, map[string]uint64{
				sheetPart:       maxOOXMLUnzipped/2 + 1,
				"xl/styles.xml": maxOOXMLUnzipped/2 + 1,
			}),
			want: ErrXLSXTooLarge,
		},
		{
			name:    "row number past Excel's last row",
			content: withSheetXML(t, valid, appendRows(`<row r="1048577">`+cells("A1048577")+`</row>`)),
			want:    ErrXLSXOutOfGrid,
		},
		{
			name:    "cell column past XFD",
			content: withSheetXML(t, valid, appendRows(`<row r="3">`+cells("XFE3")+`</row>`)),
			want:    ErrXLSXOutOfGrid,
		},
		{
			name:    "cell reference with an overlong column name",
			content: withSheetXML(t, valid, appendRows(`<row r="3">`+cells("AAAAAAAAAAAAAAA3")+`</row>`)),
			want:    ErrXLSXOutOfGrid,
		},
		{
			name: "col max past XFD",
			content: withSheetXML(t, valid, func(s string) string {
				return strings.Replace(s, "<sheetData>", `<cols><col min="1" max="16385" width="9"/></cols><sheetData>`, 1)
			}),
			want: ErrXLSXOutOfGrid,
		},
		{
			name: "non-UTF-8 encoding declaration hiding a row past the grid",
			content: withSheetXML(t, valid, func(s string) string {
				return appendRows(pastLastRow)(strings.Replace(s, `encoding="UTF-8"`, `encoding="ISO-8859-1"`, 1))
			}),
			want: ErrXLSXOutOfGrid,
		},
		{
			name: "unknown encoding label",
			content: withSheetXML(t, valid, func(s string) string {
				return strings.Replace(s, `encoding="UTF-8"`, `encoding="x-no-such-charset"`, 1)
			}),
			want: ErrXLSXMalformed,
		},
		{
			name:    "repeated row r, the last one past the grid",
			content: withSheetXML(t, valid, appendRows(`<row r="3" r="1048577">`+cells("A3")+`</row>`)),
			want:    ErrXLSXOutOfGrid,
		},
		{
			name:    "repeated cell r, the last one past XFD",
			content: withSheetXML(t, valid, appendRows(`<row r="3"><c r="A3" r="XFE3" t="inlineStr"><is><t>x</t></is></c></row>`)),
			want:    ErrXLSXOutOfGrid,
		},
		{
			name: "repeated col max, the last one past XFD",
			content: withSheetXML(t, valid, func(s string) string {
				return strings.Replace(s, "<sheetData>", `<cols><col min="1" max="1" max="16385" width="9"/></cols><sheetData>`, 1)
			}),
			want: ErrXLSXOutOfGrid,
		},
		{
			name:    "worksheet stored under a non-standard part name",
			content: withSheetMovedTo(t, valid, "xl/sheets/data.xml", appendRows(pastLastRow)),
			want:    ErrXLSXOutOfGrid,
		},
		{
			name: "worksheet elements with a namespace prefix",
			content: withSheetXML(t, valid, func(string) string {
				return `<?xml version="1.0" encoding="UTF-8"?>` +
					`<x:worksheet xmlns:x="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><x:sheetData>` +
					`<x:row r="1"><x:c r="A1" t="inlineStr"><x:is><x:t>en</x:t></x:is></x:c></x:row>` +
					`<x:row r="1048577"><x:c r="A1048577" t="inlineStr"><x:is><x:t>x</x:t></x:is></x:c></x:row>` +
					`</x:sheetData></x:worksheet>`
			}),
			want: ErrXLSXOutOfGrid,
		},
	}

	for _, tc := range cases {
		for entry, fn := range allXLSXEntryPoints() {
			t.Run(tc.name+"/"+entry, func(t *testing.T) {
				err := callGuarded(t, fn, tc.content)
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
			})
		}
	}
}

// Excel's own maximums are valid: the grid check must not reject a real file
// that uses the last row or the last column.
func TestCheckXLSX_AcceptsExcelGridEdges(t *testing.T) {
	valid := buildXLSX(t, []string{"en", "fr"}, [][]string{{"Hello", "Bonjour"}})
	edges := withSheetXML(t, valid, func(s string) string {
		s = strings.Replace(s, "<sheetData>", `<cols><col min="1" max="16384" width="9"/></cols><sheetData>`, 1)
		return appendRows(`<row r="1048576"><c r="XFD1048576" t="inlineStr"><is><t>x</t></is></c></row>`)(s)
	})
	if _, err := checkXLSX(edges); err != nil {
		t.Fatalf("checkXLSX rejected a sheet inside Excel's grid: %v", err)
	}
	if _, err := checkXLSX(valid); err != nil {
		t.Fatalf("checkXLSX rejected a plain workbook: %v", err)
	}
	latin1 := withSheetXML(t, valid, func(s string) string {
		return strings.Replace(s, `encoding="UTF-8"`, `encoding="ISO-8859-1"`, 1)
	})
	if _, err := checkXLSX(latin1); err != nil {
		t.Fatalf("checkXLSX rejected a workbook whose sheet declares ISO-8859-1: %v", err)
	}
}

// excelize 2.11.0 panics (makeslice) on a zip64 size that overflows int64.
// checkXLSX stops that file first, so this opens it with excelize directly to
// prove recoverXLSX turns a real excelize panic into ErrXLSXMalformed.
func TestRecoverXLSX_TurnsExcelizePanicIntoError(t *testing.T) {
	valid := buildXLSX(t, []string{"en", "fr"}, [][]string{{"Hello", "Bonjour"}})
	crafted := withDeclaredSheetSize(t, valid, 1<<63)
	open := func(c []byte) (err error) {
		defer recoverXLSX(context.Background(), "xlsx", &err)
		f, err := excelize.OpenReader(bytes.NewReader(c))
		if err != nil {
			return err
		}
		return f.Close()
	}
	err := callGuarded(t, open, crafted)
	if !errors.Is(err, ErrXLSXMalformed) {
		t.Fatalf("err = %v, want ErrXLSXMalformed", err)
	}
}

// Every entry point defers recoverXLSX: an excelize panic on a workbook the
// pre-check accepts comes back as ErrXLSXMalformed from each of them.
func TestXLSXEntryPoints_RecoverExcelizePanic(t *testing.T) {
	valid := buildXLSX(t, []string{"en", "fr"}, [][]string{{"Hello", "Bonjour"}})
	prev := openExcelize
	openExcelize = func(io.Reader, ...excelize.Options) (*excelize.File, error) { panic("excelize blew up") }
	t.Cleanup(func() { openExcelize = prev })

	for entry, fn := range xlsxEntryPoints {
		t.Run(entry, func(t *testing.T) {
			err := callGuarded(t, fn, valid)
			if !errors.Is(err, ErrXLSXMalformed) {
				t.Fatalf("err = %v, want ErrXLSXMalformed", err)
			}
		})
	}
}
