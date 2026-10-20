// Package inlinetag turns XLIFF rich text into the compact chip form the CAT
// editor shows a translator — `<4>` … `</4>` for paired tags, `{2}` or a
// placeholder's own content for standalone ones — and back again.
//
// It lets an LLM (MT, QE, APE) see and preserve inline tags as compact chips
// instead of receiving tag-stripped text and guessing the positions back.
//
// The chip vocabulary is the one in web/src/lib/xliff-tiptap.ts (tokenize) and
// web/src/lib/xliff-tag-label.ts (chipLabel). Those two are the reference —
// the labels here must match what the editor renders, because a reviewer reads
// the model's comment next to the editor's chips. Keep them in sync.
//
// internal/qa/checker.go has its own, different labels (`{4}` for `<g id="4">`,
// `{/g}` for `</g>`). They are deliberately NOT unified here: a QA finding's
// message is its per-segment ignore fingerprint, so
// changing them resets every stored tag-check ignore. `{/g}` is also not
// decodable — sibling `<g>` pairs all close as `{/g}`.
package inlinetag

import (
	"regexp"
	"strings"
)

// The tag grammar, mirroring the FE tokenizer's four name lists.
const (
	xliffTags     = `bpt|ept|ph|it|x|g|hi`
	htmlPaired    = `i|b|u|a|span`
	htmlSelfClose = `br|hr|img`
)

var (
	closeRe = regexp.MustCompile(`^</(` + xliffTags + `|` + htmlPaired + `)>`)
	// HTML void elements may be written without the slash (`<br>`), so openRe
	// covers them too and the caller reclassifies by name.
	selfCloseRe = regexp.MustCompile(`^<(` + xliffTags + `|` + htmlSelfClose + `)(\s[^>]*)?/>`)
	openRe      = regexp.MustCompile(`^<(` + xliffTags + `|` + htmlPaired + `|` + htmlSelfClose + `)(\s[^>]*)?>`)

	// Tags that emit an open chip and a separate close chip, with editable text
	// between them. Everything else in xliffTags is captured whole.
	pairedAsChipsRe = regexp.MustCompile(`^(g|` + htmlPaired + `)$`)

	attrRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

	selfCloseNames = strings.Split(htmlSelfClose, "|")
)

// Tag is one inline tag occurrence in rich text.
type Tag struct {
	Raw     string // the markup, verbatim — what Decode writes back
	Kind    string // element name: g, bpt, ept, ph, it, x, hi, i, b, u, a, span, br, hr, img
	ID      string // pairing attribute, "" when the markup carries none
	Num     int    // ordinal allocated per pair, the fallback when ID is empty
	Closing bool
	Start   int // byte offset of Raw in the rich text
	End     int
}

// token is one tokenizer output: either a text run or a tag.
type token struct {
	kind       string // "text", "open", "close", "pair", "selfclose"
	name       string
	attrs      string
	raw        string
	start, end int
}

// tokenize splits rich text into text runs and tag tokens. Unrecognized markup
// stays text, so a bare `<` in body content cannot derail the scan.
func tokenize(input string) []token {
	var out []token
	i := 0
	for i < len(input) {
		if input[i] != '<' {
			end := len(input)
			if next := strings.IndexByte(input[i:], '<'); next > 0 {
				end = i + next
			}
			out = append(out, token{kind: "text", raw: input[i:end], start: i, end: end})
			i = end
			continue
		}

		rest := input[i:]
		if m := closeRe.FindStringSubmatch(rest); m != nil {
			out = append(out, token{kind: "close", name: m[1], raw: m[0], start: i, end: i + len(m[0])})
			i += len(m[0])
			continue
		}
		if m := selfCloseRe.FindStringSubmatch(rest); m != nil {
			out = append(out, token{kind: "selfclose", name: m[1], attrs: m[2], raw: m[0], start: i, end: i + len(m[0])})
			i += len(m[0])
			continue
		}
		if m := openRe.FindStringSubmatch(rest); m != nil {
			name, raw := m[1], m[0]
			switch {
			case contains(selfCloseNames, name):
				out = append(out, token{kind: "selfclose", name: name, attrs: m[2], raw: raw, start: i, end: i + len(raw)})
				i += len(raw)
			case pairedAsChipsRe.MatchString(name):
				out = append(out, token{kind: "open", name: name, attrs: m[2], raw: raw, start: i, end: i + len(raw)})
				i += len(raw)
			default:
				// bpt / ept / ph / it / hi — captured whole, including content.
				closeTag := "</" + name + ">"
				rel := strings.Index(input[i+len(raw):], closeTag)
				if rel < 0 {
					// Unclosed: treat the opener as text rather than swallowing
					// the rest of the segment into one chip.
					out = append(out, token{kind: "text", raw: raw, start: i, end: i + len(raw)})
					i += len(raw)
					break
				}
				end := i + len(raw) + rel + len(closeTag)
				out = append(out, token{kind: "pair", name: name, attrs: m[2], raw: input[i:end], start: i, end: end})
				i = end
			}
			continue
		}

		// Not markup we know — emit the `<` and the run up to the next one.
		next := strings.IndexByte(input[i+1:], '<')
		end := len(input)
		if next >= 0 {
			end = i + 1 + next
		}
		out = append(out, token{kind: "text", raw: input[i:end], start: i, end: end})
		i = end
	}
	return out
}

// Tags returns every inline tag in rich text, in document order.
//
// Numbering is allocated PER PAIR: both halves of a tag carry the same Num, so
// a `<g id="4">…</g>` renders `<4>` … `</4>`. A closing tag has no attributes
// of its own, so it inherits identity from the open tag it closes.
func Tags(rich string) []Tag {
	var tags []Tag
	var stack []Tag // still-open paired tags

	nextNum := 1
	numByKey := map[string]int{}
	numberFor := func(key string) int {
		// No pairing attribute: nothing links the halves, so each gets its own
		// number and the open/close stack does the pairing instead.
		if key == "" {
			n := nextNum
			nextNum++
			return n
		}
		if n, ok := numByKey[key]; ok {
			return n
		}
		n := nextNum
		nextNum++
		numByKey[key] = n
		return n
	}

	for _, t := range tokenize(rich) {
		switch t.kind {
		case "text":
			continue
		case "open":
			key := pairKey(t.attrs)
			tag := Tag{Raw: t.raw, Kind: t.name, ID: key, Num: numberFor(key), Start: t.start, End: t.end}
			stack = append(stack, tag)
			tags = append(tags, tag)
		case "close":
			tag := Tag{Raw: t.raw, Kind: t.name, Closing: true, Start: t.start, End: t.end}
			if n := len(stack); n > 0 {
				open := stack[n-1]
				stack = stack[:n-1]
				tag.ID, tag.Num = open.ID, open.Num
			} else {
				tag.Num = numberFor("")
			}
			tags = append(tags, tag)
		case "selfclose", "pair":
			key := pairKey(t.attrs)
			tags = append(tags, Tag{
				Raw: t.raw, Kind: t.name, ID: key, Num: numberFor(key),
				// ept is the closing half of a bpt/ept pair.
				Closing: t.name == "ept",
				Start:   t.start, End: t.end,
			})
		}
	}
	return tags
}

// pairKey reads the attribute that identifies an inline tag PAIR. It is not
// always `id`: XLIFF 1.2 pairs with `rid` and gives each half a distinct `id`,
// TMX 1.4 pairs with `i`, and an SDLX export carries no `id` at all.
func pairKey(attrs string) string {
	parsed := parseAttrs(attrs)
	for _, k := range []string{"rid", "i", "id"} {
		if v := parsed[k]; v != "" {
			return v
		}
	}
	return ""
}

func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(s, -1) {
		out[m[1]] = m[2]
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// textRuns returns the visible text of rich, with every tag removed. Encode
// uses it to spot a label that also occurs literally in the content.
func textRuns(rich string) string {
	var b strings.Builder
	for _, t := range tokenize(rich) {
		if t.kind == "text" {
			b.WriteString(t.raw)
		}
	}
	return b.String()
}
