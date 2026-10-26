package pptxtest

// The fixed parts every deck carries.

const grpProps = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>`

const nsDecl = `xmlns:a="` + NSA + `" xmlns:r="` + NSR + `" xmlns:p="` + NSP + `"`

const clrMap = `<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>`

const slideMaster = xmlHeader + `<p:sldMaster ` + nsDecl + `><p:cSld><p:spTree>` + grpProps + `</p:spTree></p:cSld>` +
	clrMap + `<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst></p:sldMaster>`

const slideLayout = xmlHeader + `<p:sldLayout ` + nsDecl + ` type="blank" preserve="1"><p:cSld name="Blank"><p:spTree>` + grpProps +
	`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`

const notesMaster = xmlHeader + `<p:notesMaster ` + nsDecl + `><p:cSld><p:spTree>` + grpProps + `</p:spTree></p:cSld>` +
	clrMap + `</p:notesMaster>`

// notesSlide is a notes page with the usual placeholders. Only the body
// holds notes text; the header placeholder carries text too, which a parser
// must leave alone.
func notesSlide(body string) string {
	return xmlHeader + `<p:notes ` + nsDecl + `><p:cSld><p:spTree>` + grpProps +
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image Placeholder 1"/><p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr><p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr><p:spPr/></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		txBody("p", []string{body}) + `</p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="4" name="Header Placeholder 3"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="hdr" sz="quarter"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		txBody("p", []string{P(R(`lang="en-US"`, "Internal draft"))}) + `</p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="5" name="Slide Number Placeholder 4"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="sldNum" sz="quarter" idx="5"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		txBody("p", []string{P(R(`lang="en-US"`, "Page "), SlideNumber(1))}) + `</p:sp>` +
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:notes>`
}

// smartArtData is a diagram's data part; withDrawing names the drawing part
// through the slide's relationship.
func smartArtData(points string, withDrawing bool) string {
	ext := ""
	if withDrawing {
		ext = `<dgm:extLst><a:ext uri="http://schemas.microsoft.com/office/drawing/2008/diagram">` +
			`<dsp:dataModelExt xmlns:dsp="` + NSDsp + `" relId="` + SmartArtDrawingRelID + `" minVer="http://schemas.openxmlformats.org/drawingml/2006/diagram"/>` +
			`</a:ext></dgm:extLst>`
	}
	return xmlHeader + `<dgm:dataModel xmlns:dgm="` + NSDgm + `" xmlns:a="` + NSA + `" xmlns:r="` + NSR + `">` +
		`<dgm:ptLst>` + points + `</dgm:ptLst><dgm:cxnLst/><dgm:bg/><dgm:whole/>` + ext + `</dgm:dataModel>`
}

func smartArtDrawing(shapes string) string {
	return xmlHeader + `<dsp:drawing xmlns:dgm="` + NSDgm + `" xmlns:dsp="` + NSDsp + `" xmlns:a="` + NSA + `">` +
		`<dsp:spTree><dsp:nvGrpSpPr><dsp:cNvPr id="0" name=""/><dsp:cNvGrpSpPr/></dsp:nvGrpSpPr><dsp:grpSpPr/>` +
		shapes + `</dsp:spTree></dsp:drawing>`
}

const smartArtLayout = xmlHeader + `<dgm:layoutDef xmlns:dgm="` + NSDgm + `" xmlns:a="` + NSA + `" uniqueId="urn:microsoft.com/office/officeart/2005/8/layout/default">` +
	`<dgm:title val=""/><dgm:desc val=""/><dgm:layoutNode name="diagram"/></dgm:layoutDef>`

const smartArtStyle = xmlHeader + `<dgm:styleDef xmlns:dgm="` + NSDgm + `" xmlns:a="` + NSA + `" uniqueId="urn:microsoft.com/office/officeart/2005/8/quickstyle/simple1">` +
	`<dgm:title val=""/><dgm:desc val=""/></dgm:styleDef>`

const smartArtColors = xmlHeader + `<dgm:colorsDef xmlns:dgm="` + NSDgm + `" xmlns:a="` + NSA + `" uniqueId="urn:microsoft.com/office/officeart/2005/8/colors/accent1_2">` +
	`<dgm:title val=""/><dgm:desc val=""/></dgm:colorsDef>`

const theme = xmlHeader + `<a:theme xmlns:a="` + NSA + `" name="Office Theme"><a:themeElements>` +
	`<a:clrScheme name="Office">` +
	`<a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1><a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>` +
	`<a:dk2><a:srgbClr val="44546A"/></a:dk2><a:lt2><a:srgbClr val="E7E6E6"/></a:lt2>` +
	`<a:accent1><a:srgbClr val="4472C4"/></a:accent1><a:accent2><a:srgbClr val="ED7D31"/></a:accent2>` +
	`<a:accent3><a:srgbClr val="A5A5A5"/></a:accent3><a:accent4><a:srgbClr val="FFC000"/></a:accent4>` +
	`<a:accent5><a:srgbClr val="5B9BD5"/></a:accent5><a:accent6><a:srgbClr val="70AD47"/></a:accent6>` +
	`<a:hlink><a:srgbClr val="0563C1"/></a:hlink><a:folHlink><a:srgbClr val="954F72"/></a:folHlink></a:clrScheme>` +
	`<a:fontScheme name="Office"><a:majorFont><a:latin typeface="Calibri Light"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont>` +
	`<a:minorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme>` +
	`<a:fmtScheme name="Office">` +
	`<a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst>` +
	`<a:lnStyleLst><a:ln w="6350"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="12700"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="19050"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst>` +
	`<a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>` +
	`<a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst>` +
	`</a:fmtScheme></a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`
