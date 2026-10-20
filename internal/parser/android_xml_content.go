package parser

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// An Android string's content is XML — entities, CDATA, child elements — with
// Android's backslash escapes and quoting on top. The translator sees it
// decoded, as the app shows it (`&amp;` and `\'` as `&` and `'`), with child
// elements kept as markup. Markup escaped in the file (`&lt;b&gt;`, for
// Html.fromHtml) decodes to the same `<b>`, so it gets a tag chip like a real
// element. Export writes each tag back the way the original string had it.

// decodeAndroidContent turns a string's raw content into the text the app
// shows plus markup: XML entities and CDATA first, then Android's own layer
// (see androidUnescaper).
func decodeAndroidContent(raw []byte) (string, error) {
	var u androidUnescaper
	err := walkAndroidContent(raw, func(tok xml.Token, selfClosing bool) {
		switch t := tok.(type) {
		case xml.CharData:
			u.text(string(t))
		case xml.StartElement, xml.EndElement:
			if tag := renderTag(tok, selfClosing); tag != "" {
				u.emit(tag)
			}
		}
	})
	return u.b.String(), err
}

// androidUnescaper applies the rules aapt2 applies to a string's character
// data: a backslash escape is one character (\n and \t are a line break and a
// tab, \uXXXX a code point); an unescaped " opens or closes a quoted part and
// is not shown; outside quotes a run of whitespace is one space, and none at
// the start or end.
type androidUnescaper struct {
	b            strings.Builder
	quoted       bool
	pendingSpace bool
}

func (u *androidUnescaper) emit(s string) {
	if u.pendingSpace && u.b.Len() > 0 {
		u.b.WriteByte(' ')
	}
	u.pendingSpace = false
	u.b.WriteString(s)
}

func (u *androidUnescaper) text(s string) {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			r, n := androidEscapeAt(s[i+1:])
			u.emit(r)
			i += 1 + n
		case c == '"':
			u.quoted = !u.quoted
			i++
		case !u.quoted && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			u.pendingSpace = true
			i++
		default:
			_, size := utf8.DecodeRuneInString(s[i:])
			u.emit(s[i : i+size])
			i += size
		}
	}
}

// androidEscapeAt decodes the escape whose letter starts rest; it returns the
// character and how many bytes of rest the escape used.
func androidEscapeAt(rest string) (string, int) {
	switch rest[0] {
	case 'n':
		return "\n", 1
	case 't':
		return "\t", 1
	case 'u':
		if len(rest) >= 5 {
			if cp, err := strconv.ParseUint(rest[1:5], 16, 32); err == nil {
				return string(rune(cp)), 5
			}
		}
	}
	_, size := utf8.DecodeRuneInString(rest)
	return rest[:size], size
}

// markupTagRE matches one start, end or self-closing tag, prefixed names
// (`xliff:g`) included.
var markupTagRE = regexp.MustCompile(`</?[A-Za-z][\w.-]*(?::[A-Za-z][\w.-]*)?(?:\s[^<>]*)?/?>`)

// renderTag writes a start or end tag of a string's content the one way both
// directions use, so export can match a translation's tag against the
// source's exactly. The end tag of a self-closing element is "".
func renderTag(tok xml.Token, selfClosing bool) string {
	switch t := tok.(type) {
	case xml.StartElement:
		var b strings.Builder
		b.WriteString("<" + xmlName(t.Name))
		for _, a := range t.Attr {
			b.WriteString(` ` + xmlName(a.Name) + `="` + xmlEscapeAttr(a.Value) + `"`)
		}
		if selfClosing {
			b.WriteByte('/')
		}
		b.WriteByte('>')
		return b.String()
	case xml.EndElement:
		if !selfClosing {
			return "</" + xmlName(t.Name) + ">"
		}
	}
	return ""
}

// voidTagRE matches a line break or rule written as `<br>`, `<br/>` or
// `<br />`; the editor's chips give back `<br>`.
var voidTagRE = regexp.MustCompile(`^<(br|hr)\s*/?>$`)

// encodeAndroidContent writes a translation as string content. Text is escaped
// once. A tag is written as an element only when the source string has that
// tag as an element — same name, same attributes — and the result nests.
// A string whose source carries escaped markup is read with Html.fromHtml:
// its own escaped tags come back escaped once, so they render, and any other
// tag is escaped twice, so it shows as text after Html.fromHtml too. In any
// other string a tag the source does not have is escaped once, as text.
//
// With rawText the translation holds raw string content, which is decoded
// first, as an import decodes the source.
func encodeAndroidContent(translated string, original []byte, rawText bool) (string, error) {
	tags := sourceTags{elements: map[string]bool{}, escaped: map[string]bool{}}
	err := walkAndroidContent(original, func(tok xml.Token, selfClosing bool) {
		if tag := renderTag(tok, selfClosing); tag != "" {
			tags.elements[tag] = true
		}
	})
	if err != nil {
		return "", err
	}
	decoded, err := decodeAndroidContent(original)
	if err != nil {
		return "", err
	}
	for _, tag := range markupTagRE.FindAllString(decoded, -1) {
		if canon := canonicalTag(tag); !tags.elements[canon] {
			tags.escaped[canon] = true
		}
	}

	quoted := isQuotedAndroidString(original)
	markup := XLIFFInlineToHTML(translated)
	if rawText {
		markup = decodeRawMarkup(markup)
	}
	out := writeAndroidMarkup(markup, quoted, tags)
	if !nestsAsXML(out) {
		out = writeAndroidMarkup(markup, quoted, sourceTags{escaped: tags.escaped})
	}
	if quoted {
		return `"` + out + `"`, nil
	}
	return out, nil
}

// sourceTags are a source string's tags in canonicalTag form: the ones written
// as elements, and the ones written as escaped markup.
type sourceTags struct {
	elements map[string]bool
	escaped  map[string]bool
}

// canonicalTag renders tag the way renderTag does, so a tag written another
// way — single quotes, a space before `>` — compares equal. A tag that does
// not parse is returned as it is.
func canonicalTag(tag string) string {
	if v := voidTagRE.FindStringSubmatch(tag); v != nil {
		return "<" + v[1] + "/>"
	}
	tok, err := xml.NewDecoder(strings.NewReader(tag)).RawToken()
	if err != nil {
		return tag
	}
	if rendered := renderTag(tok, strings.HasSuffix(tag, "/>")); rendered != "" {
		return rendered
	}
	return tag
}

// decodeRawMarkup decodes the text between the tags of raw string content —
// entities, then Android's escapes and quotes — and keeps the tags.
func decodeRawMarkup(markup string) string {
	var u androidUnescaper
	last := 0
	for _, m := range markupTagRE.FindAllStringIndex(markup, -1) {
		u.text(html.UnescapeString(markup[last:m[0]]))
		u.emit(markup[m[0]:m[1]])
		last = m[1]
	}
	u.text(html.UnescapeString(markup[last:]))
	return u.b.String()
}

// writeAndroidMarkup escapes markup's text and writes each tag per the rules
// on encodeAndroidContent.
func writeAndroidMarkup(markup string, quoted bool, tags sourceTags) string {
	escape := func(s string, atStart bool) string {
		return xmlEscapeText(androidEscapeText(s, quoted, atStart))
	}
	fromHTML := len(tags.escaped) > 0
	var b strings.Builder
	last := 0
	for _, m := range markupTagRE.FindAllStringIndex(markup, -1) {
		b.WriteString(escape(markup[last:m[0]], last == 0))
		tag := markup[m[0]:m[1]]
		canon := canonicalTag(tag)
		switch {
		case tags.elements[canon]:
			b.WriteString(canon)
		case fromHTML && !tags.escaped[canon]:
			b.WriteString(escape(xmlEscapeText(tag), false))
		default:
			b.WriteString(escape(tag, false))
		}
		last = m[1]
	}
	b.WriteString(escape(markup[last:], last == 0))
	return b.String()
}

// nestsAsXML reports whether content parses as element content with every tag
// matched.
func nestsAsXML(content string) bool {
	dec := xml.NewDecoder(strings.NewReader("<r>" + content + "</r>"))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
	}
}

// androidEscapeText escapes text for a string's character data: \\, \", line
// breaks and tabs always; ' only outside quotes; a leading @ or ? so the
// value is not read as a resource or attribute reference.
func androidEscapeText(s string, quoted, atStart bool) string {
	var b strings.Builder
	for i, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\'':
			if quoted {
				b.WriteRune(r)
			} else {
				b.WriteString(`\'`)
			}
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '@', '?':
			if atStart && i == 0 {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isQuotedAndroidString reports whether the developer wrapped the whole string
// in quotes ("It's"): exactly two unescaped quotes, first and last.
func isQuotedAndroidString(content []byte) bool {
	s := string(bytes.TrimSpace(content))
	var quotes []int
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			quotes = append(quotes, i)
		}
	}
	return len(quotes) == 2 && quotes[0] == 0 && quotes[1] == len(s)-1
}

// walkAndroidContent calls visit for each character run and element inside a
// string's content, without the string's own tags. Comments and processing
// instructions are skipped.
func walkAndroidContent(raw []byte, visit func(tok xml.Token, selfClosing bool)) error {
	wrapped := append(append([]byte("<r>"), raw...), "</r>"...)
	dec := xml.NewDecoder(bytes.NewReader(wrapped))
	var open []bool // self-closing flag of each open element
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			selfClosing := bytes.HasSuffix(wrapped[:dec.InputOffset()], []byte("/>"))
			if len(open) > 0 {
				visit(t, selfClosing)
			}
			open = append(open, selfClosing)
		case xml.EndElement:
			if len(open) == 0 {
				return fmt.Errorf("unexpected </%s>", xmlName(t.Name))
			}
			selfClosing := open[len(open)-1]
			open = open[:len(open)-1]
			if len(open) > 0 {
				visit(t, selfClosing)
			}
		case xml.CharData:
			visit(t, false)
		}
	}
}

// xmlName renders a raw token's name with its prefix, as written in the file.
func xmlName(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}
