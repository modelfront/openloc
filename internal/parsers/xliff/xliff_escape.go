package xliff

import (
	"openloc/internal/parsers/common"
	"regexp"
	"strings"
)

// XLIFF rich text has two kinds of character data. Text runs (including the
// content of <g>, <hi>, <sub>) are what the translator edits and are stored
// unescaped, like every other parser's rich text. The content of bpt/ept/ph/it
// is the source format's own markup (`&lt;b&gt;`) and is stored escaped, so
// the tag stays well-formed and the chip labels decode it.

// isOpaqueInlineElement reports whether an inline element's content is native
// markup rather than translatable text.
func isOpaqueInlineElement(name string) bool {
	switch name {
	case "bpt", "ept", "ph", "it":
		return true
	}
	return false
}

func writeRichCharData(b *strings.Builder, data []byte, opaque bool) {
	if opaque {
		b.WriteString(common.XMLEscapeText(string(data)))
		return
	}
	b.Write(data)
}

var (
	xliffRichTagRE  = regexp.MustCompile(`^<(/?)(bpt|ept|ph|it|x|bx|ex|g|hi|sub|mrk)((?:\s+[\w:.-]+\s*=\s*(?:"[^"]*"|'[^']*'))*)\s*(/?)>`)
	xliffRichAttrRE = regexp.MustCompile(`([\w:.-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
)

// xliffRichToXML turns stored rich text into well-formed XLIFF element
// content. Text runs are escaped. Opaque tag content and attribute values are
// unescaped first and escaped again, so both escaped content and raw markup
// (`<ph><br/></ph>`) come out as valid XML.
func xliffRichToXML(rich string) string {
	var b strings.Builder
	b.Grow(len(rich) + len(rich)/8)
	var opaque []string // open bpt/ept/ph/it, innermost last
	for i := 0; i < len(rich); {
		if rich[i] == '<' {
			if m := xliffRichTagRE.FindStringSubmatch(rich[i:]); m != nil && richTagAllowed(m, opaque) {
				closing, name, selfClose := m[1] == "/", m[2], m[4] == "/"
				writeRichTag(&b, closing, name, m[3], selfClose)
				switch {
				case closing && len(opaque) > 0 && opaque[len(opaque)-1] == name:
					opaque = opaque[:len(opaque)-1]
				case !closing && !selfClose && isOpaqueInlineElement(name):
					opaque = append(opaque, name)
				}
				i += len(m[0])
				continue
			}
		}
		end := len(rich)
		if next := strings.IndexByte(rich[i+1:], '<'); next >= 0 {
			end = i + 1 + next
		}
		run := rich[i:end]
		if len(opaque) > 0 {
			run = common.XMLUnescapeAttr(run)
		}
		b.WriteString(common.XMLEscapeText(run))
		i = end
	}
	return b.String()
}

// richTagAllowed limits markup inside opaque content to <sub> and the closing
// tag of the element being read; anything else there is native markup.
func richTagAllowed(m []string, opaque []string) bool {
	if len(opaque) == 0 {
		return true
	}
	name := m[2]
	if name == "sub" {
		return true
	}
	return m[1] == "/" && name == opaque[len(opaque)-1]
}

func writeRichTag(b *strings.Builder, closing bool, name, attrs string, selfClose bool) {
	b.WriteByte('<')
	if closing {
		b.WriteByte('/')
	}
	b.WriteString(name)
	for _, a := range xliffRichAttrRE.FindAllStringSubmatch(attrs, -1) {
		val := a[2]
		if val == "" {
			val = a[3]
		}
		b.WriteString(" " + a[1] + `="` + common.XMLEscapeAttr(common.XMLUnescapeAttr(val)) + `"`)
	}
	if selfClose {
		b.WriteByte('/')
	}
	b.WriteByte('>')
}

// XLIFFRichTextEqual reports whether two stored XLIFF rich texts are the same
// content. Inline-tag content may be stored escaped (`&lt;b&gt;`) or as raw
// markup (`<b>`); both forms compare equal.
func XLIFFRichTextEqual(a, b string) bool {
	return a == b || xliffRichToXML(a) == xliffRichToXML(b)
}
