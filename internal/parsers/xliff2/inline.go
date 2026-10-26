package xliff2

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"openloc/internal/inlinetag"
	"openloc/internal/parsers/common"
)

// inlineEl is one inline element of a source, kept as the source wrote it. An
// export writes these bytes around the translator's text, so an element's
// attributes survive the round trip.
type inlineEl struct {
	kind  string // pc, mrk, sc or sm for a span; ph for a standalone code
	open  string // a span's start tag, or a standalone code's whole element
	close string // a span's end tag, or the ec or em closing an sc or sm
}

// inlineText is one <source> or <target> read into text and chips.
type inlineText struct {
	plain  string              // the text runs, without the inline elements
	rich   string              // the text with chips; "" when there are no inline elements
	tagged bool                // whether the element had any inline element
	elems  map[string]inlineEl // the inline elements by id
}

// readInline reads one <source> or <target>, given as the element's own bytes.
//
// Spans are chips in the form the XLIFF 1.2 parser uses: a <pc>, <mrk>, <sc>
// or <sm> opens <g id="N">, and its end (</pc>, </mrk>, <ec> or <em>) closes
// it with </g>. A standalone <ph> is <x id="N"/>. Other elements are not part
// of the text and are skipped.
func readInline(frag []byte, wrapper string) (inlineText, error) {
	dec := newDecoder(bytes.NewReader(frag))
	if _, err := dec.Token(); err != nil {
		return inlineText{}, fmt.Errorf("xml parse error: %w", err)
	}
	out := inlineText{elems: map[string]inlineEl{}}
	var plain, rich strings.Builder
	for {
		off := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			return inlineText{}, fmt.Errorf("xml parse error: <%s> is not closed", wrapper)
		}
		if err != nil {
			return inlineText{}, fmt.Errorf("xml parse error: %w", err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			plain.Write(t)
			rich.Write(t)
		case xml.EndElement:
			if !isCore(t.Name) {
				continue
			}
			switch t.Name.Local {
			case wrapper:
				out.plain = plain.String()
				if out.tagged {
					out.rich = rich.String()
				}
				return out, nil
			case "pc", "mrk":
				rich.WriteString("</g>")
			}
		case xml.StartElement:
			if !isCore(t.Name) {
				// A module element's text stays in the segment; its markup is not a chip.
				continue
			}
			id := common.XMLAttr(t, "id")
			name := t.Name.Local
			switch name {
			case "pc", "mrk":
				out.tagged = true
				out.elems[id] = inlineEl{kind: name, open: string(frag[off:int(dec.InputOffset())]), close: "</" + name + ">"}
				rich.WriteString(chip("g", id, false))
			case "sc", "sm", "ph":
				raw, err := skipRaw(dec, frag, off)
				if err != nil {
					return inlineText{}, err
				}
				out.tagged = true
				if name == "ph" {
					out.elems[id] = inlineEl{kind: name, open: raw}
					rich.WriteString(chip("x", id, true))
				} else {
					out.elems[id] = inlineEl{kind: name, open: raw}
					rich.WriteString(chip("g", id, false))
				}
			case "ec", "em":
				raw, err := skipRaw(dec, frag, off)
				if err != nil {
					return inlineText{}, err
				}
				// The end names its start in startRef; an isolated end carries the start's id instead.
				startID := common.XMLAttr(t, "startRef")
				if startID == "" {
					startID = id
				}
				if el, ok := out.elems[startID]; ok && el.close == "" && (el.kind == "sc" || el.kind == "sm") {
					el.close = raw
					out.elems[startID] = el
				}
				rich.WriteString("</g>")
			}
		}
	}
}

// skipRaw consumes the element just started and returns its bytes, from off
// through its end.
func skipRaw(dec *xml.Decoder, frag []byte, off int) (string, error) {
	if err := dec.Skip(); err != nil {
		return "", fmt.Errorf("xml parse error: %w", err)
	}
	return string(frag[off:int(dec.InputOffset())]), nil
}

// chip is the chip form of an element: a span's opener, or a standalone code
// when standalone is set.
func chip(name, id string, standalone bool) string {
	attr := ""
	if id != "" {
		attr = ` id="` + common.XMLEscapeAttr(id) + `"`
	}
	if standalone {
		return "<" + name + attr + "/>"
	}
	return "<" + name + attr + ">"
}

// targetContent is the markup of a translation inside <target>. Its chips are
// written as the source's own elements and its text is escaped. A translation
// with no chips is plain text.
func targetContent(translation string, elems map[string]inlineEl) (string, error) {
	if !hasChips(translation) {
		return common.XMLEscapeText(translation), nil
	}
	return exportRich(translation, elems)
}

// hasChips reports whether rich text carries a span or standalone chip.
func hasChips(rich string) bool {
	for _, t := range inlinetag.Tags(rich) {
		if t.Kind == "g" || t.Kind == "x" {
			return true
		}
	}
	return false
}

// exportRich rebuilds a target's markup from its chip form. Each chip is
// written as the source element with the same id, so only the translator's
// text and the chips' places change.
//
// It refuses a chip the source does not have, a chip written twice, a closing
// tag with no opening tag, and a span left open that the source closes. Markup
// that is not a chip is escaped and written as text.
func exportRich(rich string, elems map[string]inlineEl) (string, error) {
	var out strings.Builder
	written := map[string]bool{}
	var open []string // ids of spans still open, innermost last
	cursor := 0
	for _, t := range inlinetag.Tags(rich) {
		out.WriteString(common.XMLEscapeText(rich[cursor:t.Start]))
		cursor = t.End
		switch {
		case t.Kind == "g" && !t.Closing:
			el, known := elems[t.ID]
			if !known || el.kind == "ph" {
				return "", tagError(t.ID, "is not in the source")
			}
			if written[t.ID] {
				return "", tagError(t.ID, "appears more than once")
			}
			written[t.ID] = true
			open = append(open, t.ID)
			out.WriteString(el.open)
		case t.Kind == "g" && t.Closing:
			if len(open) == 0 {
				return "", &common.ExportError{Msg: "a closing inline tag has no opening tag in the translation"}
			}
			id := open[len(open)-1]
			open = open[:len(open)-1]
			out.WriteString(elems[id].close)
		case t.Kind == "x":
			el, known := elems[t.ID]
			if !known || el.kind != "ph" {
				return "", tagError(t.ID, "is not in the source")
			}
			if written[t.ID] {
				return "", tagError(t.ID, "appears more than once")
			}
			written[t.ID] = true
			out.WriteString(el.open)
		default:
			out.WriteString(common.XMLEscapeText(rich[t.Start:t.End]))
		}
	}
	out.WriteString(common.XMLEscapeText(rich[cursor:]))
	for _, id := range open {
		if elems[id].close != "" {
			return "", tagError(id, "has no closing tag in the translation")
		}
	}
	return out.String(), nil
}

// tagError is the export refusal for one inline tag, named by its id.
func tagError(id, problem string) error {
	return &common.ExportError{Msg: fmt.Sprintf("inline tag %q %s", id, problem)}
}
