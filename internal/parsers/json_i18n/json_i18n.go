package json_i18n

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"openloc/internal/parsers/common"
	"strings"
)

// JSONi18n parses flat or nested JSON i18n files (e.g. {"key": "value"} or {"ns": {"key": "value"}}).
// Every non-empty string value in an object is a segment, keyed by its dotted
// path, in file order. Strings inside lists are not translated.
type JSONi18n struct {
	// PlainText says the file's strings are plain text, not HTML (see GetParserFor).
	PlainText bool
}

func (p JSONi18n) Parse(content []byte) ([]common.Segment, error) {
	var segments []common.Segment
	seen := map[string]bool{}
	err := walkJSONStrings(content, func(s jsonString) {
		// A repeated key is one segment; export writes its translation to every copy.
		if strings.TrimSpace(s.value) == "" || seen[s.key] {
			return
		}
		seen[s.key] = true
		plain, rich := common.ImportString(s.value, p.PlainText)
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
	return segments, nil
}

// Reconstruct rewrites only the translated string values in place. Everything
// else — key order, whitespace, escapes, numbers, lists — keeps the original
// bytes, so the exported file differs from the upload only where text changed.
func (p JSONi18n) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	var out bytes.Buffer
	last := 0
	var encErr error
	err := walkJSONStrings(original, func(s jsonString) {
		translated, ok := translations[s.key]
		if !ok || encErr != nil {
			return
		}
		enc, err := encodeJSONString(common.ExportString(translated, s.value, p.PlainText))
		if err != nil {
			encErr = err
			return
		}
		out.Write(original[last:s.start])
		out.Write(enc)
		last = s.end
	})
	if err != nil {
		return nil, err
	}
	if encErr != nil {
		return nil, fmt.Errorf("encode translation: %w", encErr)
	}
	out.Write(original[last:])
	return out.Bytes(), nil
}

// jsonString is one string value in an object: its dotted key path, its
// decoded text, and the byte range of its quoted form in the file.
type jsonString struct {
	key        string
	value      string
	start, end int
}

// jsonFrame is one open object or list while walking the tokens.
type jsonFrame struct {
	object    bool
	inList    bool // this container is a list or sits inside one
	prefix    string
	expectKey bool
	key       string
}

// walkJSONStrings calls visit for every string value in an object outside any
// list, in file order. The root must be an object.
func walkJSONStrings(content []byte, visit func(jsonString)) error {
	if !json.Valid(content) {
		var v any
		return fmt.Errorf("invalid JSON: %w", json.Unmarshal(content, &v))
	}
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()

	var stack []*jsonFrame
	for {
		before := int(dec.InputOffset())
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}

		if len(stack) == 0 {
			if tok != json.Delim('{') {
				return fmt.Errorf("invalid JSON: the root must be an object")
			}
			stack = append(stack, &jsonFrame{object: true, expectKey: true})
			continue
		}
		top := stack[len(stack)-1]

		if d, ok := tok.(json.Delim); ok && (d == '}' || d == ']') {
			stack = stack[:len(stack)-1]
			continue
		}
		if top.object && top.expectKey {
			top.key, _ = tok.(string)
			top.expectKey = false
			continue
		}

		path := top.prefix
		if top.object {
			path = joinJSONKey(top.prefix, top.key)
			top.expectKey = true
		}
		switch v := tok.(type) {
		case json.Delim:
			stack = append(stack, &jsonFrame{
				object:    v == '{',
				inList:    top.inList || v == '[' || !top.object,
				prefix:    path,
				expectKey: v == '{',
			})
		case string:
			if top.object && !top.inList {
				visit(jsonString{key: path, value: v, start: before + bytes.IndexByte(content[before:], '"'), end: int(dec.InputOffset())})
			}
		}
	}
}

func joinJSONKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// encodeJSONString quotes s as a JSON string without escaping <, > and &, so
// HTML in a translation reads the way it does in the source file.
func encodeJSONString(s string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
