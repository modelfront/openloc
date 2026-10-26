package po

import (
	"crypto/sha256"
	"fmt"
	"openloc/internal/parsers/common"
	"strconv"
	"strings"
)

// PO parses PO/POT gettext files. Each msgid/msgstr pair becomes a segment.
// POT files (empty msgstr) are handled identically.
//
// A plural entry (msgid_plural with msgstr[0..N]) becomes one segment per
// msgstr[N] line the file has, keyed "<entry key>#N". Form 0 shows msgid as
// its source and every other form msgid_plural. The number of forms is the
// file's own; the Plural-Forms header is left as uploaded.
type PO struct {
	// PlainText says the file's strings are plain text, not HTML (see GetParserFor).
	PlainText bool
}

// poEntry represents a single PO entry during parsing.
type poEntry struct {
	comments    []string
	msgctxt     string
	msgid       string
	msgidPlural string
	fuzzy       bool
	plural      bool // the entry has msgstr[N] lines
	// msgstrs is the entry's msgstr block, or one block per msgstr[N].
	msgstrs []poMsgstr
}

// poMsgstr is one msgstr block and the lines it spans, for reconstruction.
type poMsgstr struct {
	index      int // N of msgstr[N]; 0 for a singular msgstr
	text       string
	start, end int // first and last line index (inclusive)
}

// poUnit is one translatable unit of an entry: the whole entry, or one form of
// a plural entry.
type poUnit struct {
	key, source, context string
	msgstr               poMsgstr
}

// units lists the entry's translatable units. The header, fuzzy entries and
// entries with no msgstr line have none.
func (e poEntry) units() []poUnit {
	if e.msgid == "" || e.fuzzy || len(e.msgstrs) == 0 {
		return nil
	}
	key := poKey(e.msgctxt, e.msgid)
	if !e.plural {
		return []poUnit{{key: key, source: e.msgid, context: e.msgctxt, msgstr: e.msgstrs[0]}}
	}
	units := make([]poUnit, 0, len(e.msgstrs))
	for _, m := range e.msgstrs {
		source := e.msgidPlural
		if m.index == 0 {
			source = e.msgid
		}
		context := fmt.Sprintf("plural form %d", m.index)
		if e.msgctxt != "" {
			context = e.msgctxt + "; " + context
		}
		units = append(units, poUnit{key: fmt.Sprintf("%s#%d", key, m.index), source: source, context: context, msgstr: m})
	}
	return units
}

func (p PO) Parse(content []byte) ([]common.Segment, error) {
	var segments []common.Segment
	for _, e := range parsePOEntries(string(content)) {
		for _, u := range e.units() {
			srcPlain, srcRich := common.ImportString(u.source, p.PlainText)
			tgtPlain, tgtRich := common.ImportString(u.msgstr.text, p.PlainText)
			segments = append(segments, common.Segment{
				Key:            u.key,
				Text:           srcPlain,
				RichText:       srcRich,
				Target:         tgtPlain,
				TargetRichText: tgtRich,
				Position:       len(segments),
				Context:        u.context,
			})
		}
	}
	return segments, nil
}

func (p PO) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	lines := strings.Split(string(original), "\n")

	// Each translated unit's msgstr block, by its first line, becomes one line.
	type replacement struct {
		end  int
		line string
	}
	replace := map[int]replacement{}
	for _, e := range parsePOEntries(string(original)) {
		for _, u := range e.units() {
			translated, ok := translations[u.key]
			if !ok {
				continue
			}
			quoted := quotePO(escapePO(common.ExportString(translated, u.source, p.PlainText)))
			line := "msgstr " + quoted
			if e.plural {
				line = fmt.Sprintf("msgstr[%d] %s", u.msgstr.index, quoted)
			}
			replace[u.msgstr.start] = replacement{end: u.msgstr.end, line: line}
		}
	}

	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if r, ok := replace[i]; ok {
			out = append(out, r.line)
			i = r.end
			continue
		}
		out = append(out, lines[i])
	}
	return []byte(strings.Join(out, "\n")), nil
}

// parsePOEntries splits PO file content into entries, tracking line positions.
func parsePOEntries(content string) []poEntry {
	lines := strings.Split(content, "\n")
	var entries []poEntry
	var cur poEntry
	var state string // "", "msgctxt", "msgid", "msgid_plural", "msgstr"
	inEntry := false

	flush := func() {
		if inEntry {
			entries = append(entries, cur)
		}
		cur = poEntry{}
		state = ""
		inEntry = false
	}

	for i, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)

		// Blank line: flush current entry.
		if trimmed == "" {
			flush()
			continue
		}

		// Comment line.
		if strings.HasPrefix(trimmed, "#") {
			if !inEntry {
				inEntry = true
			}
			cur.comments = append(cur.comments, trimmed)
			if strings.HasPrefix(trimmed, "#,") && strings.Contains(trimmed, "fuzzy") {
				cur.fuzzy = true
			}
			continue
		}

		// Keyword lines.
		if strings.HasPrefix(trimmed, "msgctxt ") {
			if !inEntry {
				inEntry = true
			}
			state = "msgctxt"
			cur.msgctxt = unquotePO(strings.TrimPrefix(trimmed, "msgctxt "))
			continue
		}
		if strings.HasPrefix(trimmed, "msgid ") {
			if !inEntry {
				inEntry = true
			}
			state = "msgid"
			cur.msgid = unquotePO(strings.TrimPrefix(trimmed, "msgid "))
			continue
		}
		if strings.HasPrefix(trimmed, "msgid_plural ") {
			state = "msgid_plural"
			cur.msgidPlural = unquotePO(strings.TrimPrefix(trimmed, "msgid_plural "))
			continue
		}
		if strings.HasPrefix(trimmed, "msgstr ") {
			state = "msgstr"
			cur.msgstrs = append(cur.msgstrs, poMsgstr{text: unquotePO(strings.TrimPrefix(trimmed, "msgstr ")), start: i, end: i})
			continue
		}
		if index, rest, ok := pluralMsgstr(trimmed); ok {
			state = "msgstr"
			cur.plural = true
			cur.msgstrs = append(cur.msgstrs, poMsgstr{index: index, text: unquotePO(rest), start: i, end: i})
			continue
		}

		// Continuation line (quoted string).
		if strings.HasPrefix(trimmed, "\"") {
			val := unquotePO(trimmed)
			switch state {
			case "msgctxt":
				cur.msgctxt += val
			case "msgid":
				cur.msgid += val
			case "msgid_plural":
				cur.msgidPlural += val
			case "msgstr":
				last := &cur.msgstrs[len(cur.msgstrs)-1]
				last.text += val
				last.end = i
			}
			continue
		}
	}
	flush()
	return entries
}

// POTargetLanguage returns the Language field of a PO file's header entry, or
// "" when the file has none.
func POTargetLanguage(content []byte) string {
	for _, e := range parsePOEntries(string(content)) {
		if e.msgid != "" || len(e.msgstrs) == 0 {
			continue
		}
		for _, line := range strings.Split(e.msgstrs[0].text, "\n") {
			if v, ok := strings.CutPrefix(line, "Language:"); ok {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	return ""
}

// pluralMsgstr splits a `msgstr[N] "…"` line into N and the quoted rest.
func pluralMsgstr(line string) (index int, rest string, ok bool) {
	tail, found := strings.CutPrefix(line, "msgstr[")
	if !found {
		return 0, "", false
	}
	num, rest, found := strings.Cut(tail, "]")
	if !found {
		return 0, "", false
	}
	index, err := strconv.Atoi(num)
	if err != nil || index < 0 {
		return 0, "", false
	}
	return index, strings.TrimSpace(rest), true
}

// poKey generates a key for a PO entry. Uses msgctxt|msgid if context present.
// If the msgid is longer than 80 chars, uses a hash to keep keys manageable.
func poKey(msgctxt, msgid string) string {
	raw := msgid
	if msgctxt != "" {
		raw = msgctxt + "|" + msgid
	}
	if len(raw) <= 80 {
		return raw
	}
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("sha256:%x", h[:8])
}

// unquotePO removes surrounding quotes and unescapes PO escape sequences.
func unquotePO(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return unescapePO(s)
}

// unescapePO handles PO escape sequences: \n, \t, \\, \"
func unescapePO(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
			case 't':
				b.WriteByte('\t')
				i++
			case '\\':
				b.WriteByte('\\')
				i++
			case '"':
				b.WriteByte('"')
				i++
			default:
				b.WriteByte(s[i])
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// escapePO escapes special characters for PO format.
func escapePO(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

// quotePO wraps a string in double quotes for PO output.
func quotePO(s string) string {
	return `"` + s + `"`
}
