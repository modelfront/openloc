package parser

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// xliffUnit is one translatable trans-unit and the bytes it spans in the
// document, from "<trans-unit" through "</trans-unit>".
type xliffUnit struct {
	tu         transUnit
	start, end int
}

// walkTransUnits decodes every trans-unit in document order and calls visit
// for each one that is translatable. Each unit gets a keyID: its id, or
// "f<N>/<id>" when a unit of an earlier <file> already has the id (N is the
// position of its <file>, from 1). Ids repeated inside one <file> are invalid
// XLIFF and keep colliding, so the callers' duplicate-key checks refuse them.
func walkTransUnits(content []byte, visit func(xliffUnit)) error {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	// SDLXLIFF can reference non-standard entities; be lenient
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose

	firstFile := map[string]int{} // id → the <file> it first appears in
	file := 0
	for {
		start := int(decoder.InputOffset())
		tok, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("xml parse error: %w", err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if el.Name.Local == "file" {
			file++
			continue
		}
		if el.Name.Local != "trans-unit" {
			continue
		}

		tu := parseTransUnit(decoder, el)
		end := int(decoder.InputOffset())
		tu.keyID = tu.id
		if first, seen := firstFile[tu.id]; !seen {
			firstFile[tu.id] = file
		} else if first != file {
			tu.keyID = fmt.Sprintf("f%d/%s", file, tu.id)
		}
		if tu.translate == "no" {
			continue
		}
		visit(xliffUnit{tu: tu, start: start, end: end})
	}
}

func (p XLIFF) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	// Each unit's own bytes are edited in place, rather than the document
	// re-serialized, so SDL metadata, namespaces and formatting survive.
	doc := string(original)
	var out strings.Builder
	last := 0
	err := walkTransUnits(original, func(u xliffUnit) {
		unit := doc[u.start:u.end]
		changed := false
		for _, s := range u.tu.segments() {
			translated, ok := translations[s.key]
			if !ok {
				continue
			}
			if s.mid != "" {
				unit = setMrkTarget(unit, s.mid, translated)
			} else {
				unit = setPlainTarget(unit, translated)
			}
			changed = true
		}
		if changed {
			out.WriteString(doc[last:u.start])
			out.WriteString(unit)
			last = u.end
		}
	})
	if err != nil {
		return nil, err
	}
	out.WriteString(doc[last:])
	return []byte(out.String()), nil
}

// xmlElement locates one element in a fragment: its start tag spans
// [start, tagEnd), and when it is not self-closing its content spans
// [tagEnd, contentEnd).
type xmlElement struct {
	start, tagEnd, contentEnd int
	selfClosing               bool
}

// findElement finds the first <name …> element in s at or after from, skipping
// any inside <alt-trans> (a TM candidate carries its own <source>/<target>).
// Nested elements of the same name are matched to their own end tags.
func findElement(s, name string, from int) (xmlElement, bool) {
	for i := from; ; {
		pos := indexStartTag(s, name, i)
		if pos < 0 {
			return xmlElement{}, false
		}
		if end := altTransEnd(s, pos); end > pos {
			i = end
			continue
		}
		closeIdx := strings.IndexByte(s[pos:], '>')
		if closeIdx < 0 {
			return xmlElement{}, false
		}
		el := xmlElement{start: pos, tagEnd: pos + closeIdx + 1}
		if s[el.tagEnd-2] == '/' {
			el.selfClosing = true
			el.contentEnd = el.tagEnd
			return el, true
		}
		el.contentEnd = matchingEnd(s, name, el.tagEnd)
		if el.contentEnd < 0 {
			return xmlElement{}, false
		}
		return el, true
	}
}

// indexStartTag returns the index of the first "<name" start tag at or after
// from (not "<name-other"), or -1.
func indexStartTag(s, name string, from int) int {
	open := "<" + name
	for i := from; ; {
		idx := strings.Index(s[i:], open)
		if idx < 0 {
			return -1
		}
		pos := i + idx
		next := pos + len(open)
		if next < len(s) && strings.IndexByte(" \t\r\n/>", s[next]) >= 0 {
			return pos
		}
		i = next
	}
}

// altTransEnd returns the end of the <alt-trans> element that contains pos,
// or -1 when pos is outside every one.
func altTransEnd(s string, pos int) int {
	for i := 0; ; {
		start := indexStartTag(s, "alt-trans", i)
		if start < 0 || start > pos {
			return -1
		}
		tagEnd := strings.IndexByte(s[start:], '>')
		if tagEnd < 0 {
			return -1
		}
		tagEnd += start + 1
		if s[tagEnd-2] == '/' {
			i = tagEnd
			continue
		}
		contentEnd := matchingEnd(s, "alt-trans", tagEnd)
		if contentEnd < 0 {
			return -1
		}
		end := contentEnd + len("</alt-trans>")
		if pos < end {
			return end
		}
		i = end
	}
}

// matchingEnd returns the index of the </name> that closes an element whose
// content starts at from, counting nested <name> elements, or -1.
func matchingEnd(s, name string, from int) int {
	closeTag := "</" + name + ">"
	depth := 0
	for i := from; i < len(s); {
		nextClose := strings.Index(s[i:], closeTag)
		if nextClose < 0 {
			return -1
		}
		if pos := indexStartTag(s, name, i); pos >= 0 && pos < i+nextClose {
			end := strings.IndexByte(s[pos:], '>')
			if end < 0 {
				return -1
			}
			if s[pos+end-1] != '/' {
				depth++
			}
			i = pos + end + 1
			continue
		}
		if depth == 0 {
			return i + nextClose
		}
		depth--
		i += nextClose + len(closeTag)
	}
	return -1
}

// openTag turns a self-closing start tag ("<target/>") into an opening one.
func openTag(selfClosing string) string {
	return strings.TrimRight(strings.TrimSuffix(selfClosing, "/>"), " \t\r\n") + ">"
}

// setPlainTarget writes newText as the unit's <target>, creating the element
// after </source> when the unit has none.
func setPlainTarget(unit, newText string) string {
	content := xliffTargetXML(newText)
	if t, ok := findElement(unit, "target", 0); ok {
		if t.selfClosing {
			return unit[:t.start] + openTag(unit[t.start:t.tagEnd]) + content + "</target>" + unit[t.tagEnd:]
		}
		return unit[:t.tagEnd] + content + unit[t.contentEnd:]
	}
	src, ok := findElement(unit, "source", 0)
	if !ok || src.selfClosing {
		return unit
	}
	at := src.contentEnd + len("</source>")
	return unit[:at] + "<target>" + content + "</target>" + unit[at:]
}

// setMrkTarget writes newText into the target <mrk mtype="seg"> with this mid.
// A unit with no <target>, or an empty <target/>, first gets one copied from
// its <seg-source>, so untranslated segments keep their source text. A
// self-closing target mrk opens; a missing one is added at the end.
func setMrkTarget(unit, mid, newText string) string {
	t, ok := findElement(unit, "target", 0)
	if !ok || t.selfClosing {
		seg, found := findElement(unit, "seg-source", 0)
		if !found || seg.selfClosing {
			return unit
		}
		target := "<target>" + unit[seg.tagEnd:seg.contentEnd] + "</target>"
		if ok {
			unit = unit[:t.start] + target + unit[t.tagEnd:]
		} else {
			at := seg.contentEnd + len("</seg-source>")
			unit = unit[:at] + target + unit[at:]
		}
		if t, ok = findElement(unit, "target", 0); !ok {
			return unit
		}
	}

	content := xliffTargetXML(newText)
	for from := t.tagEnd; ; {
		m, found := findElement(unit[:t.contentEnd], "mrk", from)
		if !found {
			break
		}
		tag := unit[m.start:m.tagEnd]
		if !strings.Contains(tag, `mid="`+xmlEscapeAttr(mid)+`"`) && !strings.Contains(tag, `mid='`+xmlEscapeAttr(mid)+`'`) {
			from = m.tagEnd
			continue
		}
		if m.selfClosing {
			return unit[:m.start] + openTag(tag) + content + "</mrk>" + unit[m.tagEnd:]
		}
		return unit[:m.tagEnd] + content + unit[m.contentEnd:]
	}
	mrk := `<mrk mtype="seg" mid="` + xmlEscapeAttr(mid) + `">` + content + "</mrk>"
	return unit[:t.contentEnd] + mrk + unit[t.contentEnd:]
}
