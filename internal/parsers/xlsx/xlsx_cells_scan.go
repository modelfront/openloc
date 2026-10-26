package xlsx

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html/charset"
	"openloc/internal/parsers/common"
)

// xlsxStringCells returns, per sheet name, the cells that store a literal
// string: t="s" (shared string), t="inlineStr", or t="str" without a formula.
// It reads each worksheet part once as a stream, so the cost per cell is
// constant and no worksheet is held in memory; excelize's per-cell lookups
// would load each sheet whole and rescan its merged ranges on every call.
// Call it only on content openXLSX accepted, which bounds every part's
// unzipped size.
func xlsxStringCells(content []byte) (map[string]map[string]bool, error) {
	pkg, err := readXLSXPackage(content)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]bool, len(pkg.sheets))
	for _, s := range pkg.sheets {
		part, ok := pkg.parts[s.part]
		if !ok {
			continue
		}
		cells, err := scanStringCells(part)
		if err != nil {
			return nil, err
		}
		out[s.name] = cells
	}
	return out, nil
}

// xlsxPackage is a workbook's sheet list, read from its package parts without
// opening the workbook.
type xlsxPackage struct {
	parts  map[string]*zip.File
	sheets []xlsxSheet
}

// xlsxSheet is one sheet of the workbook: its name, the part that holds its
// worksheet, and whether Excel hides it.
type xlsxSheet struct {
	name, part string
	hidden     bool
}

// readXLSXPackage finds the sheets as excelize finds them: the package
// relationships name the workbook, the workbook's relationships name each
// sheet's part. Two sheets on one part are malformed: Excel never writes
// that, and every reader would process the part once per sheet.
func readXLSXPackage(content []byte) (*xlsxPackage, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", common.ErrXLSXMalformed, err)
	}
	pkg := &xlsxPackage{parts: make(map[string]*zip.File, len(zr.File))}
	for _, f := range zr.File {
		pkg.parts[partName(f.Name)] = f
	}

	workbook := "xl/workbook.xml"
	if rels, err := readRelationships(pkg.parts, "_rels/.rels"); err != nil {
		return nil, err
	} else if target, ok := rels.byTypeSuffix("/officeDocument"); ok {
		workbook = resolvePart("", target)
	}
	refs, err := readWorkbookSheets(pkg.parts, workbook)
	if err != nil {
		return nil, err
	}
	rels, err := readRelationships(pkg.parts, path.Join(path.Dir(workbook), "_rels", path.Base(workbook)+".rels"))
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		target, ok := rels.byID[ref.relID]
		if !ok {
			continue
		}
		part := resolvePart(path.Dir(workbook), target)
		if seen[part] {
			return nil, fmt.Errorf("%w: two sheets share the worksheet %s", common.ErrXLSXMalformed, part)
		}
		seen[part] = true
		pkg.sheets = append(pkg.sheets, xlsxSheet{name: ref.name, part: part, hidden: ref.state == "hidden" || ref.state == "veryHidden"})
	}
	return pkg, nil
}

type xlsxRels struct {
	byID   map[string]string // relationship Id → Target
	byType map[string]string // relationship Type → Target
}

func (r xlsxRels) byTypeSuffix(suffix string) (string, bool) {
	for typ, target := range r.byType {
		if strings.HasSuffix(typ, suffix) {
			return target, true
		}
	}
	return "", false
}

// resolvePart turns a relationship Target into a part name: absolute targets
// start at the package root, others are relative to dir.
func resolvePart(dir, target string) string {
	target = strings.ReplaceAll(target, "\\", "/")
	if strings.HasPrefix(target, "/") {
		return partName(path.Clean(target))
	}
	return partName(path.Clean(path.Join(dir, target)))
}

// partName is a zip entry or resolved target as excelize names it: a
// backslash is a path separator, and there is no leading slash.
func partName(name string) string {
	return strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "/")
}

func readRelationships(parts map[string]*zip.File, name string) (xlsxRels, error) {
	rels := xlsxRels{byID: map[string]string{}, byType: map[string]string{}}
	f, ok := parts[name]
	if !ok {
		return rels, nil
	}
	err := streamXML(f, func(el xml.StartElement) {
		if el.Name.Local != "Relationship" {
			return
		}
		var id, typ, target string
		for _, a := range el.Attr {
			switch a.Name.Local {
			case "Id":
				id = a.Value
			case "Type":
				typ = a.Value
			case "Target":
				target = a.Value
			}
		}
		rels.byID[id] = target
		rels.byType[typ] = target
	})
	return rels, err
}

type xlsxSheetRef struct{ name, relID, state string }

func readWorkbookSheets(parts map[string]*zip.File, name string) ([]xlsxSheetRef, error) {
	f, ok := parts[name]
	if !ok {
		return nil, fmt.Errorf("%w: no workbook part %s", common.ErrXLSXMalformed, name)
	}
	var sheets []xlsxSheetRef
	err := streamXML(f, func(el xml.StartElement) {
		if el.Name.Local != "sheet" {
			return
		}
		var s xlsxSheetRef
		for _, a := range el.Attr {
			switch a.Name.Local {
			case "name":
				s.name = a.Value
			case "id": // r:id; sheetId is a different attribute
				s.relID = a.Value
			case "state":
				s.state = a.Value
			}
		}
		sheets = append(sheets, s)
	})
	return sheets, err
}

// scanStringCells streams one worksheet and collects its literal-string cells.
// A row or cell without an r attribute takes the position after the previous
// one, as Excel and excelize read it.
func scanStringCells(f *zip.File) (map[string]bool, error) {
	cells := map[string]bool{}
	var (
		row, col   int
		inCell     bool
		ref, typ   string
		hasFormula bool
	)
	err := streamXMLTokens(f, func(tok xml.Token) error {
		switch el := tok.(type) {
		case xml.StartElement:
			switch el.Name.Local {
			case "row":
				row++
				col = 0
				if r := common.XMLAttr(el, "r"); r != "" {
					n, err := strconv.Atoi(r)
					if err != nil {
						return fmt.Errorf("row r=%q: %v", r, err)
					}
					row = n
				}
			case "c":
				col++
				inCell, typ, hasFormula = true, common.XMLAttr(el, "t"), false
				ref = common.XMLAttr(el, "r")
				if ref != "" {
					c, r, err := excelize.CellNameToCoordinates(ref)
					if err != nil {
						return fmt.Errorf("cell r=%q: %v", ref, err)
					}
					col, row = c, r
				} else {
					name, err := excelize.CoordinatesToCellName(col, row)
					if err != nil {
						return fmt.Errorf("cell at row %d: %v", row, err)
					}
					ref = name
				}
			case "f":
				if inCell {
					hasFormula = true
				}
			}
		case xml.EndElement:
			if el.Name.Local == "c" && inCell {
				if typ == "s" || typ == "inlineStr" || (typ == "str" && !hasFormula) {
					cells[ref] = true
				}
				inCell = false
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: worksheet %s: %v", common.ErrXLSXMalformed, f.Name, err)
	}
	return cells, nil
}

// streamXML calls fn for every start element of an XML part.
func streamXML(f *zip.File, fn func(xml.StartElement)) error {
	return streamXMLTokens(f, func(tok xml.Token) error {
		if el, ok := tok.(xml.StartElement); ok {
			fn(el)
		}
		return nil
	})
}

// streamXMLTokens decodes a part token by token, with the declared charset,
// as checkWorksheetGrid does.
func streamXMLTokens(f *zip.File, fn func(xml.Token) error) error {
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
	for {
		tok, err := dec.RawToken()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("%w: part %s: %v", common.ErrXLSXMalformed, f.Name, err)
		}
		if err := fn(tok); err != nil {
			return err
		}
	}
}
