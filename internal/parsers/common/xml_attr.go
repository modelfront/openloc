package common

import "strings"

// Attribute access on a raw start tag, for exports that edit one attribute
// and keep the rest of the tag byte for byte.

// RawAttr is one attribute of a raw start tag. full runs from the whitespace
// before the name to the closing quote; value excludes the quotes.
type RawAttr struct {
	Name  string
	full  TextRange
	value TextRange
}

// scanRawAttrs lists the attributes of a start tag such as
// `<a:rPr lang="en-US" b='1'/>`. Names keep their prefix, so "lang" and
// "xml:lang" are different attributes. Scanning stops at anything that is not
// a well-formed attribute.
func scanRawAttrs(tag string) []RawAttr {
	i := 0
	if i < len(tag) && tag[i] == '<' {
		i++
	}
	for i < len(tag) && !isTagSpace(tag[i]) && tag[i] != '>' && tag[i] != '/' {
		i++
	}

	var attrs []RawAttr
	for {
		start := i
		for i < len(tag) && isTagSpace(tag[i]) {
			i++
		}
		if i == start || i >= len(tag) || tag[i] == '>' || tag[i] == '/' {
			return attrs
		}
		nameStart := i
		for i < len(tag) && tag[i] != '=' && !isTagSpace(tag[i]) && tag[i] != '>' {
			i++
		}
		name := tag[nameStart:i]
		for i < len(tag) && isTagSpace(tag[i]) {
			i++
		}
		if i >= len(tag) || tag[i] != '=' {
			return attrs
		}
		i++
		for i < len(tag) && isTagSpace(tag[i]) {
			i++
		}
		if i >= len(tag) || (tag[i] != '"' && tag[i] != '\'') {
			return attrs
		}
		quote := tag[i]
		valueStart := i + 1
		end := strings.IndexByte(tag[valueStart:], quote)
		if end < 0 {
			return attrs
		}
		i = valueStart + end + 1
		attrs = append(attrs, RawAttr{
			Name:  name,
			full:  TextRange{Start: start, End: i},
			value: TextRange{Start: valueStart, End: valueStart + end},
		})
	}
}

func isTagSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// AttrValueRange returns the byte range of attribute name's raw (escaped)
// value inside tag, without the quotes.
func AttrValueRange(tag, name string) (TextRange, bool) {
	for _, a := range scanRawAttrs(tag) {
		if a.Name == name {
			return a.value, true
		}
	}
	return TextRange{}, false
}

// RawAttrValue returns attribute name's raw (escaped) value in tag.
func RawAttrValue(tag, name string) (string, bool) {
	r, ok := AttrValueRange(tag, name)
	if !ok {
		return "", false
	}
	return tag[r.Start:r.End], true
}

// SetRawAttrValue returns tag with attribute name's value replaced by value,
// which must already be escaped. A tag without the attribute comes back as it
// is.
func SetRawAttrValue(tag, name, value string) string {
	r, ok := AttrValueRange(tag, name)
	if !ok {
		return tag
	}
	return tag[:r.Start] + value + tag[r.End:]
}

// RemoveRawAttrs returns tag without the named attributes.
func RemoveRawAttrs(tag string, names ...string) string {
	attrs := scanRawAttrs(tag)
	var edits []SpliceEdit
	for _, a := range attrs {
		for _, n := range names {
			if a.Name == n {
				edits = append(edits, SpliceEdit{Rng: a.full})
				break
			}
		}
	}
	if len(edits) == 0 {
		return tag
	}
	return Splice(tag, edits)
}

// XMLEscapeAttrValue escapes s for an attribute value in either quote style.
// Newlines, carriage returns and tabs become character references, since a
// parser normalizes literal ones in attribute values to spaces.
func XMLEscapeAttrValue(s string) string {
	s = XMLEscapeAttr(s)
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, "\r", "&#xD;")
	s = strings.ReplaceAll(s, "\n", "&#xA;")
	s = strings.ReplaceAll(s, "\t", "&#x9;")
	return s
}
