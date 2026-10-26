package xliff2

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"openloc/internal/parsers/common"
)

// segment is one <segment> of a translatable unit: its key, and where its
// <source> and, when present, its <target> sit in the document.
type segment struct {
	id     string
	key    string
	source fragment
	target *fragment
}

// fragment is the byte range of one element in the document. open is a
// <target>'s start tag, kept so a rewrite can reuse its attributes.
type fragment struct {
	start, end int
	open       string
}

// edit replaces the document's bytes from start to end with text.
type edit struct {
	start, end int
	text       string
}

// openUnit is a <unit> still being read.
type openUnit struct {
	id        string
	translate bool
	segs      []segment
}

// walkUnits decodes the document and calls visit with the segments of each
// translatable unit, in document order. Segments without a <source> are
// dropped, and units marked translate="no" are not visited.
//
// A segment's key is its unit's id, or "unit:segment" when the unit has more
// than one segment. A segment with no id is numbered from 1 by its position.
// A unit whose id an earlier <file> already has is keyed "f<N>/<id>", N being
// its <file>'s position, as the XLIFF 1.2 parser keys its units.
func walkUnits(content []byte, visit func([]segment) error) error {
	dec := newDecoder(bytes.NewReader(content))
	// translate holds the effective translate value of each open file, group and unit.
	translate := []bool{true}
	file := 0
	firstFile := map[string]int{}

	var unit *openUnit
	var seg *segment
	var srcStart int

	for {
		start := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("xml parse error: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			end := int(dec.InputOffset())
			if !isCore(t.Name) {
				// A module element (Translation Candidates, Glossary, …) has its own
				// <source> and <target>, which are not this segment's.
				if err := dec.Skip(); err != nil {
					return fmt.Errorf("xml parse error: %w", err)
				}
				continue
			}
			parent := translate[len(translate)-1]
			switch t.Name.Local {
			case "file":
				file++
				translate = append(translate, translateAttr(t, parent))
			case "group":
				translate = append(translate, translateAttr(t, parent))
			case "unit":
				unit = &openUnit{id: common.XMLAttr(t, "id"), translate: translateAttr(t, parent)}
				translate = append(translate, unit.translate)
			case "segment":
				if unit != nil {
					seg = &segment{id: common.XMLAttr(t, "id")}
				}
			case "source":
				if seg != nil {
					srcStart = start
				}
			case "target":
				if seg != nil {
					seg.target = &fragment{start: start, open: string(content[start:end])}
				}
			case "ignorable", "notes", "skeleton", "originalData":
				if err := dec.Skip(); err != nil {
					return fmt.Errorf("xml parse error: %w", err)
				}
			}
		case xml.EndElement:
			end := int(dec.InputOffset())
			if !isCore(t.Name) {
				continue
			}
			switch t.Name.Local {
			case "file", "group":
				translate = translate[:len(translate)-1]
			case "unit":
				translate = translate[:len(translate)-1]
				if err := finishUnit(unit, file, firstFile, visit); err != nil {
					return err
				}
				unit = nil
			case "segment":
				if seg != nil && seg.source.end > 0 {
					unit.segs = append(unit.segs, *seg)
				}
				seg = nil
			case "source":
				if seg != nil {
					seg.source = fragment{start: srcStart, end: end}
				}
			case "target":
				if seg != nil && seg.target != nil {
					seg.target.end = end
				}
			}
		}
	}
}

// finishUnit keys a unit's segments and visits them when the unit is
// translatable. Its id is recorded for every unit, so a later <file> that
// reuses the id is prefixed even when this unit is skipped.
func finishUnit(u *openUnit, file int, firstFile map[string]int, visit func([]segment) error) error {
	base := u.id
	if first, seen := firstFile[u.id]; !seen {
		firstFile[u.id] = file
	} else if first != file {
		base = fmt.Sprintf("f%d/%s", file, u.id)
	}
	if !u.translate || len(u.segs) == 0 {
		return nil
	}
	for i := range u.segs {
		u.segs[i].key = base
		if len(u.segs) > 1 {
			u.segs[i].key = base + ":" + segmentID(u.segs[i].id, i)
		}
	}
	return visit(u.segs)
}

// segmentID is a segment's id, or its 1-based position when it has none.
func segmentID(id string, index int) string {
	if id != "" {
		return id
	}
	return strconv.Itoa(index + 1)
}

// translateAttr reads an element's translate attribute. Without one the
// element inherits its parent's value, and a file's parent is translatable.
func translateAttr(el xml.StartElement, parent bool) bool {
	switch common.XMLAttr(el, "translate") {
	case "no":
		return false
	case "yes":
		return true
	}
	return parent
}

// targetEdit replaces the segment's <target> with content, or adds one after
// its <source> when the segment has none.
func (s segment) targetEdit(content string) edit {
	if s.target == nil {
		return edit{start: s.source.end, end: s.source.end, text: "<target>" + content + "</target>"}
	}
	return edit{start: s.target.start, end: s.target.end, text: openTag(s.target.open) + content + "</target>"}
}

// openTag turns a self-closing start tag (<target/>) into an opening one, so
// its content can be written.
func openTag(raw string) string {
	if strings.HasSuffix(raw, "/>") {
		return strings.TrimSuffix(raw, "/>") + ">"
	}
	return raw
}

// coreNamespace is the namespace of XLIFF Core elements.
const coreNamespace = "urn:oasis:names:tc:xliff:document:2.0"

// isCore reports whether an element belongs to XLIFF Core. A document with no
// namespace at all is read as Core too.
func isCore(name xml.Name) bool {
	return name.Space == "" || name.Space == coreNamespace
}

// newDecoder reads XML leniently, as the XLIFF 1.2 parser does: SDL and other
// tools write slightly off-spec documents.
func newDecoder(r io.Reader) *xml.Decoder {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	return dec
}
