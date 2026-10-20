package pptxtest

import (
	"fmt"
	"strings"
)

// Esc escapes text for element content.
func Esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

// P is a paragraph with the given runs, breaks and fields.
func P(content ...string) string {
	return `<a:p>` + strings.Join(content, "") + `</a:p>`
}

// R is a text run. attrs are a:rPr attributes such as `lang="en-US" b="1"`;
// empty attrs give a run without properties.
func R(attrs, text string) string {
	if attrs == "" {
		return `<a:r><a:t>` + Esc(text) + `</a:t></a:r>`
	}
	return `<a:r><a:rPr ` + attrs + `/><a:t>` + Esc(text) + `</a:t></a:r>`
}

// Br is a line break.
func Br() string { return `<a:br><a:rPr lang="en-US"/></a:br>` }

// SlideNumber is a slide number field showing n.
func SlideNumber(n int) string {
	return fmt.Sprintf(`<a:fld id="{B6F15528-21DE-4FAA-801E-634DDDAF4B2B}" type="slidenum"><a:rPr lang="en-US"/><a:t>%d</a:t></a:fld>`, n)
}

// TextBox is a text box shape.
func TextBox(id int, paras ...string) string {
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="TextBox %d"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`, id, id) +
		`<p:spPr/>` + txBody("p", paras) + `</p:sp>`
}

// Placeholder is a placeholder shape of the given type ("title", "body",
// "subTitle", "sldNum").
func Placeholder(id int, typ string, paras ...string) string {
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s %d"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="%s"/></p:nvPr></p:nvSpPr>`, id, typ, id, typ) +
		`<p:spPr/>` + txBody("p", paras) + `</p:sp>`
}

// WithAlt adds alt text to the first cNvPr of a shape.
func WithAlt(shape, descr string) string {
	i := strings.Index(shape, "<p:cNvPr ")
	if i < 0 {
		panic("pptxtest: shape has no cNvPr")
	}
	at := i + len("<p:cNvPr ")
	return shape[:at] + `descr="` + EscAttr(descr) + `" ` + shape[at:]
}

// EscAttr escapes text for an attribute value.
func EscAttr(s string) string {
	return strings.ReplaceAll(Esc(s), `"`, "&quot;")
}

// Group is a group of shapes.
func Group(id int, shapes ...string) string {
	return fmt.Sprintf(`<p:grpSp><p:nvGrpSpPr><p:cNvPr id="%d" name="Group %d"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>`, id, id) +
		`<p:grpSpPr/>` + strings.Join(shapes, "") + `</p:grpSp>`
}

// Table is a table whose cells each hold the given paragraphs.
func Table(id int, rows ...[]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="Table %d"/><p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr><p:nvPr/></p:nvGraphicFramePr>`, id, id)
	b.WriteString(`<p:xfrm><a:off x="0" y="0"/><a:ext cx="6096000" cy="740000"/></p:xfrm>`)
	b.WriteString(`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table"><a:tbl><a:tblPr firstRow="1" bandRow="1"/><a:tblGrid>`)
	if len(rows) > 0 {
		for range rows[0] {
			b.WriteString(`<a:gridCol w="3048000"/>`)
		}
	}
	b.WriteString(`</a:tblGrid>`)
	for _, row := range rows {
		b.WriteString(`<a:tr h="370840">`)
		for _, cell := range row {
			b.WriteString(`<a:tc>` + txBody("a", []string{cell}) + `<a:tcPr/></a:tc>`)
		}
		b.WriteString(`</a:tr>`)
	}
	b.WriteString(`</a:tbl></a:graphicData></a:graphic></p:graphicFrame>`)
	return b.String()
}

// Picture shows the slide's Image.
func Picture(id int, descr string) string {
	return fmt.Sprintf(`<p:pic><p:nvPicPr><p:cNvPr id="%d" name="Picture %d" descr="%s"/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr>`, id, id, EscAttr(descr)) +
		`<p:blipFill><a:blip r:embed="` + ImageRelID + `"/><a:stretch><a:fillRect/></a:stretch></p:blipFill>` +
		`<p:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="914400" cy="914400"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`
}

// SmartArtFrame shows the slide's SmartArt diagram.
func SmartArtFrame(id int) string {
	return fmt.Sprintf(`<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="Diagram %d"/><p:cNvGraphicFramePr/><p:nvPr/></p:nvGraphicFramePr>`, id, id) +
		`<p:xfrm><a:off x="0" y="0"/><a:ext cx="6096000" cy="2000000"/></p:xfrm>` +
		`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/diagram">` +
		`<dgm:relIds xmlns:dgm="` + NSDgm + `" r:dm="` + SmartArtDataRelID + `" r:lo="` + smartArtLayoutRelID + `" r:qs="` + smartArtStyleRelID + `" r:cs="` + smartArtColorsRelID + `"/>` +
		`</a:graphicData></a:graphic></p:graphicFrame>`
}

// DocPoint is a SmartArt diagram's root point.
func DocPoint(modelID string) string {
	return `<dgm:pt modelId="` + modelID + `" type="doc"><dgm:prSet/><dgm:spPr/><dgm:t><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></dgm:t></dgm:pt>`
}

// NodePoint is a SmartArt point holding text.
func NodePoint(modelID string, paras ...string) string {
	return `<dgm:pt modelId="` + modelID + `"><dgm:prSet/><dgm:spPr/><dgm:t><a:bodyPr/><a:lstStyle/>` +
		strings.Join(paras, "") + `</dgm:t></dgm:pt>`
}

// PresPoint is a SmartArt presentation point drawn for the point assocID.
func PresPoint(modelID, assocID string) string {
	return `<dgm:pt modelId="` + modelID + `" type="pres"><dgm:prSet presAssocID="` + assocID + `" presName="node" presStyleLbl="node1"/><dgm:spPr/></dgm:pt>`
}

// DrawingShape is a shape of a SmartArt drawing, showing the point modelID.
func DrawingShape(modelID string, paras ...string) string {
	return `<dsp:sp modelId="` + modelID + `"><dsp:nvSpPr><dsp:cNvPr id="0" name=""/><dsp:cNvSpPr/></dsp:nvSpPr><dsp:spPr/>` +
		txBody("dsp", paras) + `</dsp:sp>`
}

// txBody wraps paragraphs in a text body of the given prefix.
func txBody(prefix string, paras []string) string {
	return `<` + prefix + `:txBody><a:bodyPr/><a:lstStyle/>` + strings.Join(paras, "") + `</` + prefix + `:txBody>`
}
