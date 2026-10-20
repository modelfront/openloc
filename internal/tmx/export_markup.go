package tmx

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// tmxCodes are the TMX 1.4 inline elements that open a code; they and
// everything inside them (a <sub> sub-flow included) are copied as is.
var tmxCodes = map[string]bool{"bpt": true, "ept": true, "ph": true, "it": true, "hi": true, "ut": true}

// editorFormatting are the editor's own formatting elements: TMX has no code
// for them, so only their text is written.
var editorFormatting = map[string]bool{"strong": true, "em": true, "b": true, "i": true, "u": true, "sup": true, "sub": true, "p": true}

// chipElements maps XLIFF chip ctypes to the HTML element they stand for.
var chipElements = map[string]string{"bold": "b", "italic": "i", "underlined": "u", "lb": "br"}

// tmxSegMarkup turns stored rich text into <seg> content TMX allows: its own
// inline codes unchanged, XLIFF <g> chips as <bpt>/<ept> pairs and <x> chips
// as <ph>, both carrying the chip's native markup as escaped text, and editor
// formatting as its text alone. ok is false for anything else, and for rich
// text that is not well-formed.
func tmxSegMarkup(rich string) (out string, ok bool) {
	dec := xml.NewDecoder(strings.NewReader("<r>" + rich + "</r>"))
	dec.Entity = xml.HTMLEntity
	var b strings.Builder
	var open []chipClose // the <g> chips still open
	inCode := 0          // depth inside a TMX code element
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return b.String(), len(open) == 0 && inCode == 0
		}
		if err != nil {
			return "", false
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.WriteString(xmlEscape(string(t)))
		case xml.StartElement:
			name := t.Name.Local
			switch {
			case name == "r":
			case inCode > 0 || tmxCodes[name]:
				inCode++
				b.WriteString("<" + name)
				for _, a := range t.Attr {
					b.WriteString(" " + a.Name.Local + `="` + xmlEscape(a.Value) + `"`)
				}
				b.WriteString(">")
			case name == "g":
				native, element := chipNative(t)
				closing := ""
				if element != "" {
					closing = "</" + element + ">"
				}
				open = append(open, chipClose{id: attr(t, "id"), native: closing})
				fmt.Fprintf(&b, `<bpt i="%s">%s</bpt>`, xmlEscape(attr(t, "id")), xmlEscape(native))
			case name == "x":
				native, element := chipNative(t)
				if element != "" && native == "<"+element+">" {
					native = "<" + element + "/>"
				}
				fmt.Fprintf(&b, `<ph x="%s">%s</ph>`, xmlEscape(attr(t, "id")), xmlEscape(native))
				if err := dec.Skip(); err != nil {
					return "", false
				}
			case editorFormatting[name]:
			default:
				return "", false
			}
		case xml.EndElement:
			name := t.Name.Local
			switch {
			case name == "r":
			case inCode > 0:
				inCode--
				b.WriteString("</" + name + ">")
			case name == "g":
				c := open[len(open)-1]
				open = open[:len(open)-1]
				fmt.Fprintf(&b, `<ept i="%s">%s</ept>`, xmlEscape(c.id), xmlEscape(c.native))
			}
		}
	}
}

type chipClose struct{ id, native string }

// chipNative returns a chip's native start tag and element name: its
// original_markup when it has one, else the element its ctype names.
func chipNative(t xml.StartElement) (native, element string) {
	if m := attr(t, "original_markup"); m != "" {
		name := strings.TrimPrefix(m, "<")
		if i := strings.IndexAny(name, " \t\r\n/>"); i >= 0 {
			name = name[:i]
		}
		return m, name
	}
	ctype := attr(t, "ctype")
	element, ok := chipElements[ctype]
	if !ok {
		element = strings.TrimPrefix(ctype, "x-html-")
	}
	if element == "" {
		return "", ""
	}
	return "<" + element + ">", element
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
