// Package xliff2 parses XLIFF 2.x documents (XLIFF Core 2.0 and 2.1).
//
// Each <segment> of a translatable <unit> is a segment. Inline elements are
// read as chips in the same form the XLIFF 1.2 parser produces, so the editor,
// QA and machine translation treat both versions alike. On export a target is
// rebuilt from its source's own inline markup, so a chip can move but its
// element cannot change.
package xliff2

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"openloc/internal/parsers/common"
)

// XLIFF2 parses XLIFF 2.x documents.
type XLIFF2 struct{}

// Parse returns one segment per <segment> with non-blank source text, in
// document order. Its target, when the segment has one, is filled in too.
func (XLIFF2) Parse(content []byte) ([]common.Segment, error) {
	var segments []common.Segment
	err := walkUnits(content, func(segs []segment) error {
		for _, s := range segs {
			src, err := readInline(content[s.source.start:s.source.end], "source")
			if err != nil {
				return err
			}
			if strings.TrimSpace(src.plain) == "" {
				continue
			}
			seg := common.Segment{
				Key:      s.key,
				Text:     strings.TrimSpace(src.plain),
				RichText: src.rich,
				Position: len(segments),
			}
			if s.target != nil {
				tgt, err := readInline(content[s.target.start:s.target.end], "target")
				if err != nil {
					return err
				}
				seg.Target = strings.TrimSpace(tgt.plain)
				seg.TargetRichText = tgt.rich
			}
			segments = append(segments, seg)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("no translatable segments found")
	}
	return segments, nil
}

// Reconstruct writes each translated segment's <target>, replacing an existing
// one or adding one after the <source>. Every other byte stays as uploaded:
// the header, namespaces, skeletons, ignorable units and untranslated segments.
func (XLIFF2) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	var edits []edit
	err := walkUnits(original, func(segs []segment) error {
		for _, s := range segs {
			translated, ok := translations[s.key]
			if !ok {
				continue
			}
			src, err := readInline(original[s.source.start:s.source.end], "source")
			if err != nil {
				return err
			}
			content, err := targetContent(translated, src.elems)
			if err != nil {
				return err
			}
			edits = append(edits, s.targetEdit(content))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	last := 0
	for _, e := range edits {
		out.Write(original[last:e.start])
		out.WriteString(e.text)
		last = e.end
	}
	out.Write(original[last:])
	return out.Bytes(), nil
}

// XLIFF2TargetLanguage returns the root's trgLang attribute, the language the
// document's targets are in. It is "" when the attribute is absent.
func XLIFF2TargetLanguage(content []byte) (string, error) {
	dec := newDecoder(bytes.NewReader(content))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("xml parse error: %w", err)
		}
		if root, ok := tok.(xml.StartElement); ok {
			return common.XMLAttr(root, "trgLang"), nil
		}
	}
}
