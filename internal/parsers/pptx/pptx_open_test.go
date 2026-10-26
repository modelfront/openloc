package pptx

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"openloc/internal/parsers/common"
	"openloc/internal/parsers/common/cfbtest"
	"openloc/internal/parsers/common/ooxmltest"
	pt "openloc/internal/parsers/pptx/pptxtest"
)

// pptxEntryPoints are every exported function that opens a .pptx; each one
// must apply the same guards.
var pptxEntryPoints = map[string]func([]byte) error{
	"Parse": func(c []byte) error { _, err := PPTX{}.Parse(c); return err },
	"Reconstruct": func(c []byte) error {
		_, err := PPTX{}.Reconstruct(c, map[string]string{"k": "x"})
		return err
	},
	"ReconstructForLang": func(c []byte) error {
		_, err := PPTX{}.ReconstructForLang(context.Background(), c, map[string]string{"k": "x"}, common.ExportLangs{Source: "en", Target: "fr"})
		return err
	},
}

// withoutPart drops a part from a zip.
func withoutPart(t *testing.T, content []byte, name string) []byte {
	t.Helper()
	if !ooxmltest.HasPart(t, content, name) {
		t.Fatalf("no part %s", name)
	}
	return ooxmltest.RewriteZip(t, content, func(w *zip.Writer, f *zip.File) bool { return f.Name == name })
}

// withExtraPart adds a part to a zip.
func withExtraPart(t *testing.T, content []byte, name, body string) []byte {
	t.Helper()
	added := false
	return ooxmltest.RewriteZip(t, content, func(w *zip.Writer, f *zip.File) bool {
		if added {
			return false
		}
		added = true
		if err := w.Copy(f); err != nil {
			t.Fatal(err)
		}
		ooxmltest.WriteZipEntry(t, w, name, body)
		return true
	})
}

// nestedGroups is a slide whose shapes sit in depth nested groups.
func nestedGroups(depth int) []byte {
	shape := pt.TextBox(2, pt.P(pt.R("", "Deep")))
	open := strings.Repeat(`<p:grpSp><p:nvGrpSpPr><p:cNvPr id="3" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>`, depth)
	return pt.Build(slide(open + shape + strings.Repeat(`</p:grpSp>`, depth)))
}

// A crafted or broken file is refused with an error the upload can explain,
// from every entry point, without a panic or a hang.
func TestPPTXGuards_RejectBadFiles(t *testing.T) {
	valid := pt.Build(pt.JourneyDeck())
	cases := []struct {
		name    string
		content []byte
		want    error
	}{
		{"encrypted presentation", cfbtest.CFBFile("EncryptedPackage"), common.ErrPPTXEncrypted},
		{"PowerPoint 97-2003 file", cfbtest.CFBFile("PowerPoint Document"), common.ErrPPTXLegacy},
		{"slide larger than the cap", ooxmltest.WithDeclaredSizes(t, valid, map[string]uint64{"ppt/slides/slide1.xml": maxPPTXUnzipped + 1}), common.ErrPPTXTooLarge},
		{"tiny elements past the element budget", pt.Build(slide(strings.Repeat("<p:sp/>", maxPPTXElements+1))), common.ErrPPTXTooLarge},
		{"not a zip", []byte("this is not a presentation"), common.ErrPPTXMalformed},
		{"truncated zip", valid[:len(valid)/2], common.ErrPPTXMalformed},
		{"no presentation part", withoutPart(t, valid, "ppt/presentation.xml"), common.ErrPPTXMalformed},
		{"slide part missing", withoutPart(t, valid, "ppt/slides/slide2.xml"), common.ErrPPTXMalformed},
		{"two parts differing in case", withExtraPart(t, valid, "PPT/Presentation.xml", "<x/>"), common.ErrPPTXMalformed},
		{"slide is not XML", ooxmltest.RewriteZip(t, valid, func(w *zip.Writer, f *zip.File) bool {
			if f.Name != "ppt/slides/slide1.xml" {
				return false
			}
			ooxmltest.WriteZipEntry(t, w, f.Name, "<p:sld><unclosed>")
			return true
		}), common.ErrPPTXMalformed},
		{"a Word file renamed", wordDocument(t), common.ErrPPTXMalformed},
		{"groups nested past the limit", nestedGroups(maxPPTXShapeDepth + 1), common.ErrPPTXMalformed},
		{"groups nested very deep", nestedGroups(20_000), common.ErrPPTXMalformed},
	}
	for _, tc := range cases {
		for name, fn := range pptxEntryPoints {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				err := ooxmltest.CallGuarded(t, fn, tc.content)
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
				// A guard that is missing panics further in; the recovered
				// panic must not pass for the guard's own error.
				if errors.Is(err, common.ErrRecoveredPanic) {
					t.Fatalf("the guard did not refuse the file, a panic did: %v", err)
				}
				if _, ok := common.ImportMessage(err); !ok {
					t.Errorf("no upload message for %v", err)
				}
			})
		}
	}
}

// wordDocument is a minimal .docx: a package whose main part is a Word
// document.
func wordDocument(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	ooxmltest.WriteZipEntry(t, w, "_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`)
	ooxmltest.WriteZipEntry(t, w, "word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body/></w:document>`)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Only the parts the parser reads count against the cap. A deck with a large
// video is fine, and the video is copied without being unzipped.
func TestPPTXGuards_MediaIsNotCounted(t *testing.T) {
	valid := pt.Build(pt.JourneyDeck())
	content := ooxmltest.WithDeclaredSizes(t, valid, map[string]uint64{"ppt/media/image2.png": 1 << 62})
	segs, err := PPTX{}.Parse(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := pptxExport(t, content, map[string]string{segs[0].Key: "Résultats"})
	before, after := zipEntries(t, content), zipEntries(t, out)
	if !bytes.Equal(before["ppt/media/image2.png"], after["ppt/media/image2.png"]) {
		t.Error("the picture was not copied raw")
	}
}

// Every upload message names what to do, in words a user understands.
func TestImportMessage(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{common.ErrPPTXLegacy, "save it as .pptx"},
		{common.ErrPPTXEncrypted, "password-protected"},
		{common.ErrPPTXTooLarge, "too large"},
		{common.ErrPPTXMalformed, "not a valid PowerPoint presentation"},
		{common.ErrPPTXEmpty, "no text to translate"},
		{common.ErrXLSXEncrypted, "password-protected"},
		{common.ErrXLSXTooLarge, "too large"},
		{common.ErrXLSXMalformed, "not a valid Excel workbook"},
		{common.ErrXLSXOutOfGrid, "not a valid Excel workbook"},
		{common.ErrXLSXEmpty, "no text to translate"},
		{common.ErrXLSXLegacy, "save it as .xlsx"},
		{fmt.Errorf("pptx: %w (panic: boom)", common.ErrPPTXMalformed), "not a valid PowerPoint presentation"},
	}
	for _, tc := range cases {
		msg, ok := common.ImportMessage(tc.err)
		if !ok || !strings.Contains(msg, tc.want) {
			t.Errorf("ImportMessage(%v) = %q, %v; want it to contain %q", tc.err, msg, ok, tc.want)
		}
	}
	if msg, ok := common.ImportMessage(errors.New("xml: decode at byte 3")); ok {
		t.Errorf("an unknown error got a message: %q", msg)
	}
}

func TestUnsupportedFormatMessage(t *testing.T) {
	if msg, ok := common.UnsupportedFormatMessage("Deck.PPT"); !ok || !strings.Contains(msg, "save it as .pptx") {
		t.Errorf(".PPT: %q, %v", msg, ok)
	}
	if msg, ok := common.UnsupportedFormatMessage("Sheet.XLS"); !ok || !strings.Contains(msg, "save it as .xlsx") {
		t.Errorf(".XLS: %q, %v", msg, ok)
	}
	for _, name := range []string{"deck.pptx", "deck.ppt.zip", "notes.txt", "sheet.xlsx"} {
		if msg, ok := common.UnsupportedFormatMessage(name); ok {
			t.Errorf("%s got a message: %q", name, msg)
		}
	}
	if common.FormatFromFilename("Deck.PPTX") != "pptx" || common.FormatFromFilename("deck.ppt") != "" {
		t.Error("only .pptx is a PowerPoint format")
	}
}

// Every entry point recovers a panic inside the parser and reports it as a
// malformed file, marked as a recovered panic.
func TestPPTXEntryPoints_RecoverPanic(t *testing.T) {
	orig := parsePPTXPart
	parsePPTXPart = func([]byte, common.XMLParseOptions) (*common.XMLDoc, error) { panic("parser blew up") }
	t.Cleanup(func() { parsePPTXPart = orig })

	content := pt.Build(pt.JourneyDeck())
	for name, fn := range pptxEntryPoints {
		t.Run(name, func(t *testing.T) {
			err := ooxmltest.CallGuarded(t, fn, content)
			if !errors.Is(err, common.ErrPPTXMalformed) || !errors.Is(err, common.ErrRecoveredPanic) {
				t.Fatalf("err = %v, want a recovered panic reported as ErrPPTXMalformed", err)
			}
		})
	}
}

// Shape geometry (spPr) can hold tens of thousands of path points in a deck
// drawn with vector shapes. The parser never reads inside it, so those
// elements cost no DOM nodes and do not count against the element budget.
func TestPPTXGuards_GeometryIsNotCounted(t *testing.T) {
	geometry := `<p:sp><p:nvSpPr><p:cNvPr id="9" name="Freeform"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr><a:custGeom><a:pathLst><a:path>` +
		strings.Repeat(`<a:lnTo><a:pt x="1" y="2"/></a:lnTo>`, maxPPTXElements) +
		`</a:path></a:pathLst></a:custGeom></p:spPr></p:sp>`
	segs := pptxSegments(t, slide(pt.TextBox(2, pt.P(pt.R("", "Drawn deck"))), geometry))
	if got := pptxTexts(segs); len(got) != 1 || got[0] != "Drawn deck" {
		t.Fatalf("texts = %q, want the text box only", got)
	}
}

// When earlier parts have used the whole node budget, the next part is
// refused before it is parsed: no budget left must never mean no limit.
func TestPPTXGuards_NodeBudgetUsedUp(t *testing.T) {
	pkg, err := openPPTX(pt.Build(pt.JourneyDeck()))
	if err != nil {
		t.Fatal(err)
	}
	pkg.nodesLeft = 0
	if _, err := pkg.doc("ppt/slides/slide1.xml"); !errors.Is(err, common.ErrPPTXTooLarge) {
		t.Fatalf("err = %v, want ErrPPTXTooLarge", err)
	}
}

// One start tag with millions of attributes is a single element, so the node
// budget cannot see it, and the XML decoder builds every attribute before the
// parser gets the tag. Start tags past maxPPTXTagBytes are refused first.
func TestPPTXGuards_HugeStartTag(t *testing.T) {
	huge := `<p:sp ` + strings.Repeat(`a="" `, maxPPTXTagBytes/5+1) + `/>`
	for name, fn := range pptxEntryPoints {
		t.Run(name, func(t *testing.T) {
			err := ooxmltest.CallGuarded(t, fn, pt.Build(slide(huge)))
			if !errors.Is(err, common.ErrPPTXTooLarge) || errors.Is(err, common.ErrRecoveredPanic) {
				t.Fatalf("err = %v, want ErrPPTXTooLarge", err)
			}
		})
	}
}

// Many tags just under the start-tag limit add up: each keeps its attributes
// on its node. The attributes the parts keep are budgeted in total, so a
// small zip of attribute-heavy tags is refused as too large.
func TestPPTXGuards_AttributeBudget(t *testing.T) {
	perTag := (maxPPTXTagBytes - 100) / 5
	tag := `<p:sp ` + strings.Repeat(`a="" `, perTag) + `/>`
	content := pt.Build(slide(strings.Repeat(tag, maxPPTXAttrs/perTag+1)))
	for name, fn := range pptxEntryPoints {
		t.Run(name, func(t *testing.T) {
			err := ooxmltest.CallGuarded(t, fn, content)
			if !errors.Is(err, common.ErrPPTXTooLarge) || errors.Is(err, common.ErrRecoveredPanic) {
				t.Fatalf("err = %v, want ErrPPTXTooLarge", err)
			}
		})
	}
}

// The attribute budget also stops a part while it is parsed, not only the
// part after it: the last part a deck reads has no part after it.
func TestPPTXGuards_AttributeBudgetStopsTheParse(t *testing.T) {
	pkg, err := openPPTX(pt.Build(slide(`<p:sp ` + strings.Repeat(`a="" `, 50) + `/>`)))
	if err != nil {
		t.Fatal(err)
	}
	pkg.attrsLeft = 20
	if _, err := pkg.doc("ppt/slides/slide1.xml"); !errors.Is(err, common.ErrPPTXTooLarge) {
		t.Fatalf("err = %v, want ErrPPTXTooLarge", err)
	}
}
