package xliff

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"openloc/internal/parsers/common"
	"strings"
)

// XLIFF parses XLIFF 1.2 and SDLXLIFF files.
// Segments are extracted from <trans-unit> elements (skipping translate="no").
// For SDLXLIFF, segments come from <seg-source> <mrk mtype="seg"> elements.
// Key format: "{trans-unit-id}" or "{trans-unit-id}:{mrk-mid}" for multi-segment units.
// Ids are unique only within one <file>, so a unit whose id an earlier unit
// already has is keyed "f{N}/{trans-unit-id}", N being its <file>'s position.
type XLIFF struct{}

func (p XLIFF) Parse(content []byte) ([]common.Segment, error) {
	var segments []common.Segment
	pos := 0

	err := walkTransUnits(content, func(u xliffUnit) {
		for _, s := range u.tu.segments() {
			if strings.TrimSpace(s.text) == "" {
				continue
			}
			// XLIFF exports often carry HTML inline markup as
			// entity-escaped text inside <source>/<target> — `<b>{name}</b>`
			// ships as `&lt;b&gt;{name}&lt;/b&gt;`.
			// The XML decoder un-escapes them, so by the time we see
			// s.text it contains literal `<b>...</b>`. Run them through
			// inline tag detection — same path as JSON / TXT / XLSX. Skip
			// when richText was already populated by the XLIFF inline-tag
			// parser (bpt/ept/ph/g/x) — that takes precedence.
			srcText, srcRich := s.text, s.richText
			if srcRich == "" {
				srcText, srcRich = common.DetectInlineTags(s.text)
			}
			tgtText, tgtRich := s.target, s.targetRich
			if tgtRich == "" && s.target != "" {
				tgtText, tgtRich = common.DetectInlineTags(s.target)
			}
			segments = append(segments, common.Segment{
				Key:            s.key,
				Text:           srcText,
				RichText:       srcRich,
				Target:         tgtText,
				TargetRichText: tgtRich,
				Position:       pos,
			})
			pos++
		}
	})
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("no translatable segments found")
	}
	return segments, nil
}

// XLIFFTargetLanguage scans the XLIFF content for the first `<file>` element's
// `target-language` attribute. Returns the empty string when the attribute is
// absent — XLIFF 1.2 spec marks it optional, so absence is not an error here.
//
// Used by the re-import flow ([internal/file/handler_reimport.go]) to
// cross-check the user-provided `?lang=` against the file metadata. Without
// this check, a user could upload an XLIFF whose `<target>` text is in the
// wrong language and have it silently overwrite the wrong translation rows
// (architecture spec §E "Open questions" — REQUIRED cross-check).
//
// Returns the raw attribute value; the caller is responsible for normalizing
// (`langcode.Normalize`) before comparing against project state.
func XLIFFTargetLanguage(content []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("xml parse error: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "file" {
			continue
		}
		for _, a := range start.Attr {
			if a.Name.Local == "target-language" {
				return a.Value, nil
			}
		}
		// First <file> seen, no target-language attr — return empty.
		return "", nil
	}
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	xml.Escape(&buf, []byte(s))
	return buf.String()
}

// xliffTargetXML returns newText as XLIFF element content: rich text with
// inline tags keeps its tags, plain text is escaped whole.
func xliffTargetXML(newText string) string {
	if hasXLIFFInlineTags(newText) {
		return xliffRichToXML(newText)
	}
	return xmlEscape(newText)
}

// hasXLIFFInlineTags returns true if the string contains XLIFF inline tag elements.
func hasXLIFFInlineTags(s string) bool {
	for _, tag := range []string{"<bpt ", "<ept ", "<ph ", "<it ", "<x ", "<g ", "<bpt>", "<ept>", "<ph>", "<x/>"} {
		if strings.Contains(s, tag) {
			return true
		}
	}
	return false
}

// --- internal trans-unit parsing ---

type transUnit struct {
	id         string
	keyID      string // id, made unique across <file> elements (see XLIFF)
	translate  string // "yes" (default) or "no"
	source     string // plain <source> text
	sourceRich string // source with inline XLIFF tags (empty if no tags)
	segSource  []segMrk
	target     string
	targetRich string
	segTarget  []segMrk
	hasTarget  bool
}

type segMrk struct {
	mid      string
	text     string
	richText string // text with inline XLIFF tags (empty if no tags)
}

type parsedSeg struct {
	key        string
	mid        string
	text       string
	richText   string // source with inline tags
	target     string
	targetRich string
}

func (tu *transUnit) segments() []parsedSeg {
	// Prefer seg-source mrk segments (SDLXLIFF style)
	if len(tu.segSource) > 0 {
		targetByMid := make(map[string]string, len(tu.segTarget))
		targetRichByMid := make(map[string]string, len(tu.segTarget))
		for _, t := range tu.segTarget {
			targetByMid[t.mid] = t.text
			targetRichByMid[t.mid] = t.richText
		}

		multiSeg := len(tu.segSource) > 1
		var out []parsedSeg
		for _, m := range tu.segSource {
			key := tu.keyID
			mid := m.mid
			if multiSeg {
				key = tu.keyID + ":" + mid
			}
			out = append(out, parsedSeg{
				key: key, mid: mid,
				text: m.text, richText: m.richText,
				target: targetByMid[mid], targetRich: targetRichByMid[mid],
			})
		}
		return out
	}

	if tu.source != "" {
		return []parsedSeg{{
			key: tu.keyID, text: tu.source, richText: tu.sourceRich,
			target: tu.target, targetRich: tu.targetRich,
		}}
	}
	return nil
}

func parseTransUnit(decoder *xml.Decoder, start xml.StartElement) transUnit {
	tu := transUnit{translate: "yes"}
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "id":
			tu.id = a.Value
		case "translate":
			tu.translate = a.Value
		}
	}

	depth := 1
	var inSegSource, inTarget bool
	// Track inline XLIFF tags inside <target> for non-SDLXLIFF files.
	// SDLXLIFF uses <mrk> segments (handled separately), but standard XLIFF
	// has inline tags (bpt/ept/ph/etc.) directly inside <target>.
	var targetPlain, targetRich strings.Builder
	targetHasTags := false
	targetHiddenDepth := 0
	targetOpaqueDepth := 0

	for depth > 0 {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "alt-trans":
				// A TM/MT candidate with its own <source> and <target>; it
				// must not overwrite the trans-unit's own pair.
				if err := decoder.Skip(); err != nil {
					return tu
				}
				depth--
			case "source":
				if !inSegSource {
					plain, rich := collectTextWithTags(decoder)
					depth-- // collectTextWithTags consumed the closing </source>
					tu.source = strings.TrimSpace(plain)
					tu.sourceRich = rich
				}
			case "seg-source":
				inSegSource = true
			case "target":
				if !inSegSource {
					inTarget = true
					tu.hasTarget = true
					targetPlain.Reset()
					targetRich.Reset()
					targetHasTags = false
					targetHiddenDepth = 0
					targetOpaqueDepth = 0
				}
			case "mrk":
				mid := common.XMLAttr(t, "mid")
				mtype := common.XMLAttr(t, "mtype")
				if mtype == "seg" && mid != "" {
					plain, rich := collectTextWithTags(decoder)
					depth-- // collectTextWithTags consumed the closing </mrk>
					if inSegSource || !inTarget {
						tu.segSource = append(tu.segSource, segMrk{mid: mid, text: plain, richText: rich})
					} else if inTarget {
						tu.segTarget = append(tu.segTarget, segMrk{mid: mid, text: plain, richText: rich})
					}
				}
			default:
				if inTarget && isXLIFFInlineElement(t.Name.Local) {
					targetHasTags = true
					targetRich.WriteString(serializeStartElement(t))
					if t.Name.Local == "bpt" || t.Name.Local == "ept" || t.Name.Local == "it" {
						targetHiddenDepth++
					}
					if isOpaqueInlineElement(t.Name.Local) {
						targetOpaqueDepth++
					}
				}
			}
		case xml.EndElement:
			depth--
			switch t.Name.Local {
			case "seg-source":
				inSegSource = false
			case "target":
				if inTarget {
					inTarget = false
					tu.target = strings.TrimSpace(targetPlain.String())
					if targetHasTags {
						tu.targetRich = targetRich.String()
					}
				}
			default:
				if inTarget && isXLIFFInlineElement(t.Name.Local) {
					targetRich.WriteString("</" + t.Name.Local + ">")
					if (t.Name.Local == "bpt" || t.Name.Local == "ept" || t.Name.Local == "it") && targetHiddenDepth > 0 {
						targetHiddenDepth--
					}
					if isOpaqueInlineElement(t.Name.Local) && targetOpaqueDepth > 0 {
						targetOpaqueDepth--
					}
				}
			}
		case xml.CharData:
			if inTarget {
				writeRichCharData(&targetRich, t, targetOpaqueDepth > 0)
				if targetHiddenDepth == 0 {
					targetPlain.Write(t)
				}
			}
		}
	}

	return tu
}

// collectText reads all text until the closing tag, stripping inline elements.
func collectText(decoder *xml.Decoder) string {
	plain, _ := collectTextWithTags(decoder)
	return plain
}

// collectTextWithTags reads text AND inline XLIFF elements, returning both
// plain text (for display/search) and rich text (with tags, for round-trip).
// If no inline tags are found, rich == "". In rich text, the content of
// bpt/ept/ph/it and every attribute value stay XML-escaped; text runs are
// stored unescaped, as the editor and the other parsers store them.
func collectTextWithTags(decoder *xml.Decoder) (plain, rich string) {
	var plainBuf, richBuf strings.Builder
	hasTags := false
	depth := 1
	hiddenDepth := 0 // >0 when inside bpt/ept/it (formatting markup hidden from plain text)
	opaqueDepth := 0 // >0 when inside an element whose content is native markup
	for depth > 0 {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if isXLIFFInlineElement(t.Name.Local) {
				hasTags = true
				richBuf.WriteString(serializeStartElement(t))
				// bpt/ept/it content is formatting markup (e.g. <b>, </b>) — hide from plain text
				// ph/x/g/hi/sub content is visible placeholders — show in plain text
				if t.Name.Local == "bpt" || t.Name.Local == "ept" || t.Name.Local == "it" {
					hiddenDepth++
				}
				if isOpaqueInlineElement(t.Name.Local) {
					opaqueDepth++
				}
			}
		case xml.EndElement:
			depth--
			if isXLIFFInlineElement(t.Name.Local) {
				richBuf.WriteString("</" + t.Name.Local + ">")
				if (t.Name.Local == "bpt" || t.Name.Local == "ept" || t.Name.Local == "it") && hiddenDepth > 0 {
					hiddenDepth--
				}
				if isOpaqueInlineElement(t.Name.Local) && opaqueDepth > 0 {
					opaqueDepth--
				}
			}
		case xml.CharData:
			writeRichCharData(&richBuf, t, opaqueDepth > 0)
			if hiddenDepth == 0 {
				plainBuf.Write(t)
			}
		}
	}
	if !hasTags {
		return plainBuf.String(), ""
	}
	return plainBuf.String(), richBuf.String()
}

// isXLIFFInlineElement returns true for XLIFF 1.2 inline elements.
func isXLIFFInlineElement(name string) bool {
	switch name {
	case "bpt", "ept", "ph", "it", "x", "g", "hi", "sub":
		return true
	}
	return false
}

// serializeStartElement re-serializes an xml.StartElement to its XML string.
func serializeStartElement(el xml.StartElement) string {
	var buf strings.Builder
	buf.WriteString("<")
	buf.WriteString(el.Name.Local)
	for _, a := range el.Attr {
		buf.WriteString(" ")
		buf.WriteString(a.Name.Local)
		buf.WriteString(`="`)
		buf.WriteString(common.XMLEscapeAttr(a.Value))
		buf.WriteString(`"`)
	}
	buf.WriteString(">")
	return buf.String()
}
