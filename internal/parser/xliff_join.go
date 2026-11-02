package parser

import (
	"slices"
	"strings"
)

// A join of a trans-unit's <mrk> sentences merges them: in <seg-source> the
// first <mrk> takes the others' content and the whitespace between them, and
// the others go, in <target> too, and in SDLXLIFF their <sdl:seg> entries;
// <source> stays as it is. A run is joined only when its <mrk> elements are
// siblings with nothing but whitespace between them.

// xliffRangeKey is the key of a run of a trans-unit's <mrk> sentences.
func xliffRangeKey(unitKey, firstMid, lastMid string) string {
	return unitKey + ":" + firstMid + "~" + lastMid
}

// SentenceBlocks lists the trans-units segmented into several <mrk>
// sentences. A run keeps the file's chip ids, so its offsets are 0.
func (p XLIFF) SentenceBlocks(original []byte) ([]SentenceBlock, error) {
	doc := string(original)
	var out []SentenceBlock
	err := walkTransUnits(original, func(u xliffUnit) {
		segs := u.tu.segments()
		if len(segs) < 2 || segs[0].mid == "" {
			return
		}
		unit := doc[u.start:u.end]
		keys := make([]string, len(segs))
		mids := make([]string, len(segs))
		for i, s := range segs {
			keys[i], mids[i] = s.key, s.mid
		}
		unitKey := u.tu.keyID
		out = append(out, SentenceBlock{
			Keys: keys,
			run: func(i, j int) (SentenceRange, error) {
				gaps, ok := segMrkGaps(unit, mids[i:j+1])
				if !ok {
					return SentenceRange{}, ErrSentenceRange
				}
				var text, rich strings.Builder
				chips := false
				for k := i; k <= j; k++ {
					if k > i {
						text.WriteString(gaps[k-i-1])
						rich.WriteString(gaps[k-i-1])
					}
					text.WriteString(segs[k].text)
					if segs[k].richText != "" {
						rich.WriteString(segs[k].richText)
						chips = chips || segs[k].richText != segs[k].text
					} else {
						rich.WriteString(segs[k].text)
					}
				}
				r := SentenceRange{Key: xliffRangeKey(unitKey, mids[i], mids[j]), Text: text.String(), Offsets: make([]int, j-i+1)}
				if chips {
					r.Rich = rich.String()
				}
				return r, nil
			},
			locate: func(key string) (int, int, bool) {
				for i := range keys {
					if key == keys[i] {
						return i, i, true
					}
					for j := i + 1; j < len(keys); j++ {
						if key == xliffRangeKey(unitKey, mids[i], mids[j]) {
							return i, j, true
						}
					}
				}
				return 0, 0, false
			},
		})
	})
	return out, err
}

// mrkElement is a <mrk mtype="seg"> element of a <seg-source> or <target>.
type mrkElement struct {
	mid string
	el  xmlElement
}

// segMrksIn lists the <mrk mtype="seg"> children of the region el of unit,
// in order, skipping <mrk> elements nested inside them.
func segMrksIn(unit string, region xmlElement) []mrkElement {
	var out []mrkElement
	for from := region.tagEnd; ; {
		m, ok := findElement(unit[:region.contentEnd], "mrk", from)
		if !ok {
			return out
		}
		tag := unit[m.start:m.tagEnd]
		from = m.contentEnd
		if !strings.Contains(tag, `mtype="seg"`) && !strings.Contains(tag, `mtype='seg'`) {
			from = m.tagEnd
			continue
		}
		out = append(out, mrkElement{mid: attrValue(tag, "mid"), el: m})
	}
}

// segMrkGaps returns the text between each two of the run's <mrk> elements
// in <seg-source>, which follow each other there with only whitespace
// between them; ok is false when they do not.
func segMrkGaps(unit string, mids []string) (gaps []string, ok bool) {
	seg, found := findElement(unit, "seg-source", 0)
	if !found || seg.selfClosing {
		return nil, false
	}
	mrks := segMrksIn(unit, seg)
	start := -1
	for i, m := range mrks {
		if m.mid == mids[0] {
			start = i
			break
		}
	}
	if start < 0 || start+len(mids) > len(mrks) {
		return nil, false
	}
	for k, mid := range mids {
		m := mrks[start+k]
		if m.mid != mid || m.el.selfClosing {
			return nil, false
		}
		if k > 0 {
			prev := mrks[start+k-1].el
			gap := unit[prev.contentEnd+len("</mrk>") : m.el.start]
			if strings.TrimSpace(gap) != "" {
				return nil, false
			}
			gaps = append(gaps, gap)
		}
	}
	return gaps, true
}

// mergeSegMrks merges the run's <mrk> elements in <seg-source> into the
// first, drops the others from <target> and their <sdl:seg> entries; ok is
// false, and unit unchanged, when the run is not one segMrkGaps accepts.
func mergeSegMrks(unit string, mids []string) (string, bool) {
	if _, ok := segMrkGaps(unit, mids); !ok {
		return unit, false
	}
	seg, _ := findElement(unit, "seg-source", 0)
	mrks := segMrksIn(unit, seg)
	start := 0
	for mrks[start].mid != mids[0] {
		start++
	}
	first, last := mrks[start].el, mrks[start+len(mids)-1].el
	var merged strings.Builder
	merged.WriteString(unit[first.tagEnd:first.contentEnd])
	for k := 1; k < len(mids); k++ {
		prev, m := mrks[start+k-1].el, mrks[start+k].el
		merged.WriteString(unit[prev.contentEnd+len("</mrk>") : m.start])
		merged.WriteString(unit[m.tagEnd:m.contentEnd])
	}
	unit = unit[:first.tagEnd] + merged.String() + unit[last.contentEnd:]

	if t, ok := findElement(unit, "target", 0); ok && !t.selfClosing {
		targetMrks := segMrksIn(unit, t)
		for i := len(targetMrks) - 1; i >= 0; i-- {
			m := targetMrks[i]
			if !slices.Contains(mids[1:], m.mid) {
				continue
			}
			end := m.el.contentEnd
			if !m.el.selfClosing {
				end += len("</mrk>")
			}
			unit = unit[:trimSpaceBefore(unit, m.el.start)] + unit[end:]
		}
	}
	for _, mid := range mids[1:] {
		unit = dropSDLSeg(unit, mid)
	}
	return unit, true
}

// dropSDLSeg removes the <sdl:seg> entry with this id from <sdl:seg-defs>.
func dropSDLSeg(unit, id string) string {
	for from := 0; ; {
		el, ok := findElement(unit, "sdl:seg", from)
		if !ok {
			return unit
		}
		if attrValue(unit[el.start:el.tagEnd], "id") != id {
			from = el.tagEnd
			continue
		}
		end := el.tagEnd
		if !el.selfClosing {
			end = el.contentEnd + len("</sdl:seg>")
		}
		return unit[:trimSpaceBefore(unit, el.start)] + unit[end:]
	}
}

// attrValue is the value of attribute name in a start tag, quoted either
// way, unescaped; "" when the tag has none.
func attrValue(tag, name string) string {
	for _, q := range []string{`"`, `'`} {
		marker := " " + name + "=" + q
		i := strings.Index(tag, marker)
		if i < 0 {
			continue
		}
		rest := tag[i+len(marker):]
		if j := strings.Index(rest, q); j >= 0 {
			return xmlUnescapeAttr(rest[:j])
		}
	}
	return ""
}

// trimSpaceBefore moves i back over the whitespace before it.
func trimSpaceBefore(s string, i int) int {
	for i > 0 && strings.IndexByte(" \t\r\n", s[i-1]) >= 0 {
		i--
	}
	return i
}
