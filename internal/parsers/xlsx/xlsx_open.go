package xlsx

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html/charset"
	"openloc/internal/parsers/common"
)

// Excel's grid. A worksheet that references anything outside it is malformed,
// and several excelize bugs are reached only through such references.
const (
	maxXLSXRows = 1_048_576
	maxXLSXCols = 16_384
)

// xlsxErrors are the XLSX sentinels for the shared OOXML checks.
var xlsxErrors = common.OOXMLErrors{
	Encrypted:   common.ErrXLSXEncrypted,
	TooLarge:    common.ErrXLSXTooLarge,
	Malformed:   common.ErrXLSXMalformed,
	PanicAction: "xlsx-panic",
}

// openExcelize is excelize.OpenReader. Tests replace it with an opener that
// panics, to prove every entry point recovers.
var openExcelize = excelize.OpenReader

// openXLSX checks content with checkXLSX and opens it with excelize. Every
// caller also defers recoverXLSX, since excelize can still panic on input the
// check lets through.
func openXLSX(content []byte) (*excelize.File, error) {
	if _, err := checkXLSX(content); err != nil {
		return nil, err
	}
	return openExcelize(bytes.NewReader(content), excelize.Options{UnzipSizeLimit: common.MaxOOXMLUnzipped})
}

// recoverXLSX turns a panic inside excelize into an error wrapping
// ErrXLSXMalformed and reports it with ctx's scope. Use it as
// `defer recoverXLSX(ctx, op, &err)` in a function with a named error result.
func recoverXLSX(ctx context.Context, op string, err *error) {
	if rec := recover(); rec != nil {
		common.OOXMLPanicError(ctx, rec, op, xlsxErrors, err)
	}
}

// checkXLSX rejects workbooks that would reach known excelize failure modes,
// without handing them to excelize: encrypted containers, entries whose
// declared unzipped size is out of range, worksheets with rows or columns
// outside Excel's grid, and sheets that share a worksheet part. It returns the
// workbook's sheet list. It reads every worksheet once as a stream; Go's zip
// reader never returns more bytes than an entry declares, so the declared-size
// check bounds that read too.
func checkXLSX(content []byte) (*xlsxPackage, error) {
	// A legacy .xls and an encrypted .xlsx are both OLE compound files; a
	// workbook stream ("Workbook", or "Book" in Excel 5) means the former.
	if bytes.HasPrefix(content, common.CFBSignature) && (common.CFBHasStream(content, "Workbook") || common.CFBHasStream(content, "Book")) {
		return nil, common.ErrXLSXLegacy
	}
	zr, err := common.OpenOOXMLZip(content, xlsxErrors)
	if err != nil {
		return nil, err
	}
	budget := common.NewUnzipBudget(common.MaxOOXMLUnzipped, common.ErrXLSXTooLarge)
	for _, f := range zr.File {
		if err := budget.Reserve(f); err != nil {
			return nil, err
		}
	}
	for _, f := range zr.File {
		if err := checkWorksheetGrid(f); err != nil {
			return nil, err
		}
	}
	return readXLSXPackage(content)
}

// checkWorksheetGrid validates the grid references of f if it is a worksheet.
// The root element decides that, not the path, because excelize locates
// sheets through the workbook's relationships. Like excelize, it decodes any
// declared charset and matches row, c and col by local name, wherever they
// appear. An XML part it cannot read up to its root element is refused, since
// excelize may still read it.
func checkWorksheetGrid(f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("%w: %v", common.ErrXLSXMalformed, err)
	}
	defer rc.Close()

	br := bufio.NewReader(rc)
	if !looksLikeXML(br) {
		return nil
	}
	dec := xml.NewDecoder(br)
	dec.CharsetReader = charset.NewReaderLabel
	root := true
	for {
		tok, err := dec.RawToken()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("%w: part %s: %v", common.ErrXLSXMalformed, f.Name, err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if root {
			if el.Name.Local != "worksheet" {
				return nil
			}
			root = false
			continue
		}
		if err := checkGridElement(el); err != nil {
			return fmt.Errorf("%w: worksheet %s: %v", common.ErrXLSXOutOfGrid, f.Name, err)
		}
	}
}

// looksLikeXML reports whether the first non-blank byte, after an optional
// UTF-8 BOM, is '<'.
func looksLikeXML(br *bufio.Reader) bool {
	head, _ := br.Peek(512)
	head = bytes.TrimPrefix(head, []byte("\xEF\xBB\xBF"))
	head = bytes.TrimLeft(head, " \t\r\n")
	return len(head) > 0 && head[0] == '<'
}

// checkGridElement checks every row r, c r and col min/max attribute of el.
// Every occurrence counts: XML allows a repeated attribute through, and
// excelize keeps the last one.
func checkGridElement(el xml.StartElement) error {
	for _, a := range el.Attr {
		var err error
		switch {
		case el.Name.Local == "row" && a.Name.Local == "r":
			err = checkGridNumber(a.Value, maxXLSXRows)
		case el.Name.Local == "c" && a.Name.Local == "r":
			err = checkCellRef(a.Value)
		case el.Name.Local == "col" && (a.Name.Local == "min" || a.Name.Local == "max"):
			err = checkGridNumber(a.Value, maxXLSXCols)
		}
		if err != nil {
			return fmt.Errorf("%s %s=%q: %v", el.Name.Local, a.Name.Local, a.Value, err)
		}
	}
	return nil
}

// checkGridNumber checks that s is a 1-based row or column number no larger
// than max.
func checkGridNumber(s string, max int) error {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > max {
		return fmt.Errorf("want 1..%d", max)
	}
	return nil
}

// checkCellRef validates an A1-style reference: 1–3 column letters up to XFD,
// then a row number up to Excel's last row.
func checkCellRef(ref string) error {
	i, col := 0, 0
	for ; i < len(ref); i++ {
		ch := ref[i] | 0x20 // ASCII lower-case
		if ch < 'a' || ch > 'z' {
			break
		}
		if i == 3 {
			return fmt.Errorf("column has more than 3 letters")
		}
		col = col*26 + int(ch-'a'+1)
	}
	if i == 0 || col > maxXLSXCols {
		return fmt.Errorf("column outside A..XFD")
	}
	return checkGridNumber(ref[i:], maxXLSXRows)
}
