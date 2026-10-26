package android_xml

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"openloc/internal/parsers/common"
)

// AndroidXML parses Android strings.xml files, extracting <string>, <string-array>,
// and <plurals> elements as translatable segments.
type AndroidXML struct {
	// RawText says the translations hold a string's raw content — entities,
	// backslash escapes and quotes as written in the file — which is how a file
	// stores them when AndroidStoredRaw reports so. Reconstruct decodes that
	// text first, so nothing is escaped twice.
	RawText bool
}

func (AndroidXML) Parse(content []byte) ([]common.Segment, error) {
	var segments []common.Segment
	var contentErr error
	err := walkAndroidStrings(content, func(s androidString) {
		if !s.translatable || contentErr != nil {
			return
		}
		text, err := decodeAndroidContent(content[s.start:s.end])
		if err != nil {
			contentErr = fmt.Errorf("android_xml: read %q: %w", s.key, err)
			return
		}
		// A string may hold inline HTML (<b>, <i>, <a>…), real or escaped for
		// Html.fromHtml; both are markup the app renders, so both become tags.
		plain, rich := common.DetectInlineTags(text)
		segments = append(segments, common.Segment{
			Key:      s.key,
			Text:     plain,
			RichText: rich,
			Position: len(segments),
		})
	})
	if err != nil {
		return nil, err
	}
	if contentErr != nil {
		return nil, contentErr
	}
	return segments, nil
}

// Reconstruct replaces the content of each translated string in place. Every
// other byte — the declaration, namespaces, comments, indentation and the
// strings left untranslated — stays as uploaded.
func (p AndroidXML) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	var out bytes.Buffer
	last := 0
	var contentErr error
	err := walkAndroidStrings(original, func(s androidString) {
		translated, ok := translations[s.key]
		if !s.translatable || !ok || contentErr != nil {
			return
		}
		inner := original[s.start:s.end]
		core := bytes.TrimSpace(inner)
		lead := bytes.Index(inner, core)
		if len(core) == 0 {
			lead = len(inner)
		}
		markup, err := encodeAndroidContent(translated, core, p.RawText)
		if err != nil {
			contentErr = fmt.Errorf("android_xml: write %q: %w", s.key, err)
			return
		}
		out.Write(original[last : s.start+lead])
		out.WriteString(markup)
		last = s.start + lead + len(core)
	})
	if err != nil {
		return nil, err
	}
	if contentErr != nil {
		return nil, contentErr
	}
	out.Write(original[last:])
	return out.Bytes(), nil
}

// AndroidStoredRaw reports whether stored (segment key → source text) holds
// the strings' raw content: some stored text differs from the text a parse of
// original gives.
func AndroidStoredRaw(original []byte, stored map[string]string) (bool, error) {
	segments, err := AndroidXML{}.Parse(original)
	if err != nil {
		return false, err
	}
	for _, s := range segments {
		if text, ok := stored[s.Key]; ok && text != s.Text {
			return true, nil
		}
	}
	return false, nil
}

// androidString is one translatable element — a <string>, or an <item> of a
// <string-array> or <plurals> — with the byte range of its content.
type androidString struct {
	key          string
	translatable bool
	start, end   int
}

// walkAndroidStrings calls visit for every <string> and array/plural <item>,
// in file order. Keys: `name`, `name[index]` for arrays, `name[quantity]` for
// plurals. A self-closing element has no content and is skipped.
func walkAndroidStrings(content []byte, visit func(androidString)) error {
	dec := xml.NewDecoder(bytes.NewReader(content))
	var (
		cur         *androidString
		selfClosing bool
		depth       int // elements open inside cur
		arrayName   string
		arrayIndex  int
		pluralName  string
		groupTransl bool // the open array or plurals is translatable
		inArray     bool
		inPlurals   bool
	)
	for {
		before := int(dec.InputOffset())
		// Token, not RawToken: it checks that every end tag matches, so a
		// malformed file is rejected rather than cut at the wrong place.
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("android_xml: decode: %w", err)
		}
		after := int(dec.InputOffset())

		if cur != nil {
			switch tok.(type) {
			case xml.StartElement:
				depth++
			case xml.EndElement:
				if depth > 0 {
					depth--
					continue
				}
				if !selfClosing {
					cur.end = before
					visit(*cur)
				}
				cur = nil
			}
			continue
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space != "" {
				continue
			}
			var key string
			translatable := true
			switch t.Name.Local {
			case "string":
				key = attrVal(t.Attr, "name")
				translatable = attrVal(t.Attr, "translatable") != "false"
			case "string-array":
				arrayName, arrayIndex, inArray = attrVal(t.Attr, "name"), 0, true
				groupTransl = attrVal(t.Attr, "translatable") != "false"
				continue
			case "plurals":
				pluralName, inPlurals = attrVal(t.Attr, "name"), true
				groupTransl = attrVal(t.Attr, "translatable") != "false"
				continue
			case "item":
				translatable = groupTransl
				switch {
				case inArray:
					key = fmt.Sprintf("%s[%d]", arrayName, arrayIndex)
					arrayIndex++
				case inPlurals:
					key = fmt.Sprintf("%s[%s]", pluralName, attrVal(t.Attr, "quantity"))
				default:
					continue
				}
			default:
				continue
			}
			cur = &androidString{key: key, translatable: translatable, start: after}
			selfClosing = bytes.HasSuffix(content[:after], []byte("/>"))
			depth = 0
		case xml.EndElement:
			switch t.Name.Local {
			case "string-array":
				inArray = false
			case "plurals":
				inPlurals = false
			}
		}
	}
}

// attrVal returns the value of the named attribute, or empty string.
func attrVal(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
