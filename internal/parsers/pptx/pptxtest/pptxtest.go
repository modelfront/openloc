// Package pptxtest builds small PowerPoint decks for tests: a presentation
// with a slide master, a layout and a theme, and per slide the shapes, notes,
// picture and SmartArt diagram a test asks for. Shape content is raw
// PresentationML; the helpers in shapes.go write the common pieces.
//
// Output is deterministic (fixed timestamps, fixed part order), so a deck can
// be compared with a committed fixture.
package pptxtest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"time"
)

// Relationship ids a slide's shape XML can refer to.
const (
	ImageRelID           = "rId3"
	SmartArtDataRelID    = "rId4"
	smartArtLayoutRelID  = "rId5"
	smartArtStyleRelID   = "rId6"
	smartArtColorsRelID  = "rId7"
	SmartArtDrawingRelID = "rId8"
)

// Namespaces of the parts.
const (
	NSA   = "http://schemas.openxmlformats.org/drawingml/2006/main"
	NSR   = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	NSP   = "http://schemas.openxmlformats.org/presentationml/2006/main"
	NSDgm = "http://schemas.openxmlformats.org/drawingml/2006/diagram"
	NSDsp = "http://schemas.microsoft.com/office/drawing/2008/diagram"

	relBase = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
)

// Deck is a presentation to build.
type Deck struct {
	Slides []Slide
}

// Slide is one slide, in display order.
type Slide struct {
	Hidden bool
	// Shapes is the XML inside p:spTree after the group properties.
	Shapes string
	// Notes is the XML of the notes body's paragraphs (a:p elements). Empty
	// means the slide has no notes page.
	Notes string
	// Image is a PNG the slide's shapes can show through ImageRelID.
	Image []byte
	// SmartArt is a diagram the slide's shapes can show with SmartArtFrame.
	SmartArt *SmartArt
}

// SmartArt is a diagram's data and its cached drawing.
type SmartArt struct {
	// Points is the XML inside dgm:ptLst.
	Points string
	// Drawing is the XML inside dsp:spTree.
	Drawing string
	// NoDrawing leaves the drawing part out, as some files do; PowerPoint
	// then lays the diagram out from the data part.
	NoDrawing bool
}

// stamp is every entry's modification time.
var stamp = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Build writes the deck as a .pptx.
func Build(d Deck) []byte {
	parts := d.parts()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, p := range parts {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Deflate, Modified: stamp})
		if err != nil {
			panic(err)
		}
		if _, err := fw.Write([]byte(p.body)); err != nil {
			panic(err)
		}
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

type part struct {
	name string
	body string
}

// Parts returns the deck's parts by name, for tests that compare decks by
// content rather than by zip bytes.
func Parts(d Deck) map[string]string {
	out := map[string]string{}
	for _, p := range d.parts() {
		out[p.name] = p.body
	}
	return out
}

func (d Deck) parts() []part {
	hasNotes := false
	for _, s := range d.Slides {
		hasNotes = hasNotes || s.Notes != ""
	}

	var ct, sldIDs strings.Builder
	ct.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Default Extension="png" ContentType="image/png"/>` +
		override("/ppt/presentation.xml", "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml") +
		override("/ppt/slideMasters/slideMaster1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml") +
		override("/ppt/slideLayouts/slideLayout1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml") +
		override("/ppt/theme/theme1.xml", "application/vnd.openxmlformats-officedocument.theme+xml"))

	presRelList := []string{
		rel("rId1", "slideMaster", "slideMasters/slideMaster1.xml"),
		rel("rId2", "theme", "theme/theme1.xml"),
	}
	if hasNotes {
		presRelList = append(presRelList, rel("rId3", "notesMaster", "notesMasters/notesMaster1.xml"))
		ct.WriteString(override("/ppt/notesMasters/notesMaster1.xml", "application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml"))
		ct.WriteString(override("/ppt/theme/theme2.xml", "application/vnd.openxmlformats-officedocument.theme+xml"))
	}

	var slideParts []part
	for i, s := range d.Slides {
		n := i + 1
		rid := fmt.Sprintf("rId%d", 10+i)
		presRelList = append(presRelList, rel(rid, "slide", fmt.Sprintf("slides/slide%d.xml", n)))
		fmt.Fprintf(&sldIDs, `<p:sldId id="%d" r:id="%s"/>`, 255+n, rid)
		ct.WriteString(override(fmt.Sprintf("/ppt/slides/slide%d.xml", n), "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"))
		slideParts = append(slideParts, s.slideParts(n, &ct)...)
	}
	ct.WriteString(`</Types>`)

	presentation := xmlHeader + `<p:presentation xmlns:a="` + NSA + `" xmlns:r="` + NSR + `" xmlns:p="` + NSP + `">` +
		`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>`
	if hasNotes {
		presentation += `<p:notesMasterIdLst><p:notesMasterId r:id="rId3"/></p:notesMasterIdLst>`
	}
	presentation += `<p:sldIdLst>` + sldIDs.String() + `</p:sldIdLst>` +
		`<p:sldSz cx="12192000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`

	parts := []part{
		{"[Content_Types].xml", ct.String()},
		{"_rels/.rels", rels(rel("rId1", "officeDocument", "ppt/presentation.xml"))},
		{"ppt/presentation.xml", presentation},
		{"ppt/_rels/presentation.xml.rels", rels(presRelList...)},
		{"ppt/slideMasters/slideMaster1.xml", slideMaster},
		{"ppt/slideMasters/_rels/slideMaster1.xml.rels", rels(
			rel("rId1", "slideLayout", "../slideLayouts/slideLayout1.xml"),
			rel("rId2", "theme", "../theme/theme1.xml"),
		)},
		{"ppt/slideLayouts/slideLayout1.xml", slideLayout},
		{"ppt/slideLayouts/_rels/slideLayout1.xml.rels", rels(rel("rId1", "slideMaster", "../slideMasters/slideMaster1.xml"))},
		{"ppt/theme/theme1.xml", theme},
	}
	if hasNotes {
		parts = append(parts,
			part{"ppt/notesMasters/notesMaster1.xml", notesMaster},
			part{"ppt/notesMasters/_rels/notesMaster1.xml.rels", rels(rel("rId1", "theme", "../theme/theme2.xml"))},
			part{"ppt/theme/theme2.xml", theme},
		)
	}
	return append(parts, slideParts...)
}

// slideParts returns slide n's part, its relationships and the notes,
// picture and diagram parts it uses, and adds their content types.
func (s Slide) slideParts(n int, ct *strings.Builder) []part {
	show := ""
	if s.Hidden {
		show = ` show="0"`
	}
	slide := xmlHeader + `<p:sld xmlns:a="` + NSA + `" xmlns:r="` + NSR + `" xmlns:p="` + NSP + `"` + show + `>` +
		`<p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
		`<p:grpSpPr/>` + s.Shapes + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`

	slideRels := []string{rel("rId1", "slideLayout", "../slideLayouts/slideLayout1.xml")}
	var parts []part
	if s.Notes != "" {
		slideRels = append(slideRels, rel("rId2", "notesSlide", fmt.Sprintf("../notesSlides/notesSlide%d.xml", n)))
		ct.WriteString(override(fmt.Sprintf("/ppt/notesSlides/notesSlide%d.xml", n), "application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"))
		parts = append(parts,
			part{fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", n), notesSlide(s.Notes)},
			part{fmt.Sprintf("ppt/notesSlides/_rels/notesSlide%d.xml.rels", n), rels(
				rel("rId1", "notesMaster", "../notesMasters/notesMaster1.xml"),
				rel("rId2", "slide", fmt.Sprintf("../slides/slide%d.xml", n)),
			)},
		)
	}
	if s.Image != nil {
		slideRels = append(slideRels, rel(ImageRelID, "image", fmt.Sprintf("../media/image%d.png", n)))
		parts = append(parts, part{fmt.Sprintf("ppt/media/image%d.png", n), string(s.Image)})
	}
	if s.SmartArt != nil {
		slideRels = append(slideRels,
			rel(SmartArtDataRelID, "diagramData", fmt.Sprintf("../diagrams/data%d.xml", n)),
			rel(smartArtLayoutRelID, "diagramLayout", fmt.Sprintf("../diagrams/layout%d.xml", n)),
			rel(smartArtStyleRelID, "diagramQuickStyle", fmt.Sprintf("../diagrams/quickStyle%d.xml", n)),
			rel(smartArtColorsRelID, "diagramColors", fmt.Sprintf("../diagrams/colors%d.xml", n)),
		)
		types := []struct{ name, typ string }{
			{"data", "application/vnd.openxmlformats-officedocument.drawingml.diagramData+xml"},
			{"layout", "application/vnd.openxmlformats-officedocument.drawingml.diagramLayout+xml"},
			{"quickStyle", "application/vnd.openxmlformats-officedocument.drawingml.diagramStyle+xml"},
			{"colors", "application/vnd.openxmlformats-officedocument.drawingml.diagramColors+xml"},
		}
		parts = append(parts,
			part{fmt.Sprintf("ppt/diagrams/data%d.xml", n), smartArtData(s.SmartArt.Points, !s.SmartArt.NoDrawing)},
			part{fmt.Sprintf("ppt/diagrams/layout%d.xml", n), smartArtLayout},
			part{fmt.Sprintf("ppt/diagrams/quickStyle%d.xml", n), smartArtStyle},
			part{fmt.Sprintf("ppt/diagrams/colors%d.xml", n), smartArtColors},
		)
		if !s.SmartArt.NoDrawing {
			slideRels = append(slideRels, `<Relationship Id="`+SmartArtDrawingRelID+`" Type="http://schemas.microsoft.com/office/2007/relationships/diagramDrawing" Target="`+fmt.Sprintf("../diagrams/drawing%d.xml", n)+`"/>`)
			types = append(types, struct{ name, typ string }{"drawing", "application/vnd.ms-office.drawingml.diagramDrawing+xml"})
			parts = append(parts, part{fmt.Sprintf("ppt/diagrams/drawing%d.xml", n), smartArtDrawing(s.SmartArt.Drawing)})
		}
		for _, d := range types {
			ct.WriteString(override(fmt.Sprintf("/ppt/diagrams/%s%d.xml", d.name, n), d.typ))
		}
	}
	return append([]part{
		{fmt.Sprintf("ppt/slides/slide%d.xml", n), slide},
		{fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", n), rels(slideRels...)},
	}, parts...)
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

func override(name, typ string) string {
	return `<Override PartName="` + name + `" ContentType="` + typ + `"/>`
}

func rel(id, typ, target string) string {
	return `<Relationship Id="` + id + `" Type="` + relBase + typ + `" Target="` + target + `"/>`
}

func rels(items ...string) string {
	return xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		strings.Join(items, "") + `</Relationships>`
}
