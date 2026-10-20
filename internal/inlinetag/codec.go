package inlinetag

import (
	"sort"
	"strconv"
	"strings"
)

// Table holds the chip labels assigned to one segment's tags and the markup
// each one stands for. Encode builds it; Decode consumes it. A source and its
// translation must share one Table, or the two sides label the same tag
// differently and the model has nothing to match on.
type Table struct {
	entries []entry
	// byKey maps a tag's identity to the label it was assigned, so the same tag
	// in the translation reuses the source's label.
	byKey map[string]string
	// used is every label handed out, so a second tag cannot claim it.
	used map[string]bool
	// literal is the visible text seen so far, so a label that also occurs as
	// real content is never handed out.
	literal string
	nextNum int
}

type entry struct {
	label string
	raw   string
}

// NewTable returns an empty Table. Encode calls it for you; use it directly
// only when encoding several strings that must share one labelling.
func NewTable() *Table {
	return &Table{byKey: map[string]string{}, used: map[string]bool{}, nextNum: 1}
}

// Encode replaces every inline tag in rich with its chip label and returns the
// result plus the Table needed to decode it. Rich text with no tags comes back
// unchanged with an empty Table.
func Encode(rich string) (string, *Table) {
	t := NewTable()
	return EncodeWith(t, rich), t
}

// EncodeWith encodes rich against an existing Table, reusing the labels already
// assigned to tags it has seen. This is how a segment's source and target end
// up speaking the same chip vocabulary.
func EncodeWith(t *Table, rich string) string {
	if rich == "" {
		return ""
	}
	tags := Tags(rich)
	if len(tags) == 0 {
		return rich
	}
	t.literal += textRuns(rich)

	// Group by pair so both halves of a tag get one number. Allocation order is
	// first appearance, which keeps labels stable for a given input.
	var order []string
	byPair := map[string][]Tag{}
	for _, tg := range tags {
		k := pairIdentity(tg)
		if _, seen := byPair[k]; !seen {
			order = append(order, k)
		}
		byPair[k] = append(byPair[k], tg)
	}
	for _, k := range order {
		t.assign(byPair[k])
	}

	var b strings.Builder
	b.Grow(len(rich))
	pos := 0
	for _, tg := range tags {
		b.WriteString(rich[pos:tg.Start])
		b.WriteString(t.byKey[tagIdentity(tg)])
		pos = tg.End
	}
	b.WriteString(rich[pos:])
	return b.String()
}

// assign gives every tag of one pair its final label, picking a fresh number
// when the natural one is already taken or occurs literally in the text.
func (t *Table) assign(pair []Tag) {
	// Already labelled on an earlier Encode against this table (the source side
	// of a source/target pair). Reusing is the whole point — reallocating here
	// would label the same tag differently on the two sides.
	if t.allLabelled(pair) {
		return
	}

	// A placeholder shows its own content (`%s`). Content labels are handled
	// per tag: they carry no number to bump, and two `%s` placeholders are
	// interchangeable, so duplicates are fine and Decode resolves them by
	// occurrence order.
	if c := phContent(pair[0].Raw); c != "" && !pairedTypes[pair[0].Kind] {
		if !strings.Contains(t.literal, c) {
			for _, tg := range pair {
				t.record(tg, c)
			}
			return
		}
		// The content also appears as real text — a number is the only safe
		// label left.
	}

	body := pair[0].ID
	if body == "" {
		body = strconv.Itoa(pair[0].Num)
	}
	for !t.bodyFree(pair, body) {
		body = strconv.Itoa(t.nextNum)
		t.nextNum++
	}
	// Keep the fresh-number counter clear of numbers already in play, so a
	// later collision does not hand out one that is taken.
	if n, err := strconv.Atoi(body); err == nil && n >= t.nextNum {
		t.nextNum = n + 1
	}
	for _, tg := range pair {
		t.record(tg, labelWithBody(tg, body))
	}
}

func (t *Table) allLabelled(pair []Tag) bool {
	for _, tg := range pair {
		if t.byKey[tagIdentity(tg)] == "" {
			return false
		}
	}
	return true
}

// bodyFree reports whether every label this body would produce is unclaimed and
// absent from the segment's own text.
func (t *Table) bodyFree(pair []Tag, body string) bool {
	for _, tg := range pair {
		l := labelWithBody(tg, body)
		if t.used[l] && t.byKey[tagIdentity(tg)] != l {
			return false
		}
		if strings.Contains(t.literal, l) {
			return false
		}
	}
	return true
}

func (t *Table) record(tg Tag, label string) {
	t.byKey[tagIdentity(tg)] = label
	t.used[label] = true
	t.entries = append(t.entries, entry{label: label, raw: tg.Raw})
}

// labelWithBody renders a tag's label around an explicit body, bypassing the
// id/ordinal Label would pick.
func labelWithBody(t Tag, body string) string {
	if !pairedTypes[t.Kind] {
		return "{" + body + "}"
	}
	if t.Closing {
		return "</" + body + ">"
	}
	return "<" + body + ">"
}

// tagIdentity distinguishes one tag occurrence kind from another across the
// source and its translation. Mirrors chipKey in web/src/lib/missing-tags.ts.
func tagIdentity(t Tag) string {
	id := t.ID
	if id == "" {
		id = "#" + strconv.Itoa(t.Num)
	}
	return t.Kind + "|" + id + "|" + strconv.FormatBool(t.Closing)
}

// pairIdentity groups the halves of one tag so they share a number. bpt and ept
// are folded together because they are the two halves of the same pair.
func pairIdentity(t Tag) string {
	kind := t.Kind
	if kind == "bpt" || kind == "ept" {
		kind = "bptept"
	}
	id := t.ID
	if id == "" {
		id = "#" + strconv.Itoa(t.Num)
	}
	return kind + "|" + id
}

// Empty reports whether the table holds no tags, i.e. the segment was plain
// text and encoding changed nothing.
func (t *Table) Empty() bool { return t == nil || len(t.entries) == 0 }

// Decode turns chip labels back into inline tag markup.
//
// Repeated labels resolve by occurrence order: the k-th `%s` in the model's
// output becomes the k-th `%s` in the table. A label the table does not know is
// left in the text verbatim and reported in unknown — the caller surfaces those
// to a reviewer, because nothing downstream can tell a leftover `{4}` from a
// real placeholder.
func Decode(chipText string, t *Table) (rich string, unknown []string) {
	if t.Empty() || chipText == "" {
		return chipText, nil
	}

	// Longest label first, so `{10}` is never matched as `{1}` followed by `0}`.
	labels := make([]string, 0, len(t.entries))
	seen := map[string]bool{}
	for _, e := range t.entries {
		if !seen[e.label] {
			seen[e.label] = true
			labels = append(labels, e.label)
		}
	}
	sort.Slice(labels, func(i, j int) bool { return len(labels[i]) > len(labels[j]) })

	nth := map[string]int{}
	var b strings.Builder
	b.Grow(len(chipText))
	unknownSeen := map[string]bool{}

	for i := 0; i < len(chipText); {
		matched := ""
		for _, l := range labels {
			if strings.HasPrefix(chipText[i:], l) {
				matched = l
				break
			}
		}
		if matched != "" {
			b.WriteString(t.rawFor(matched, nth[matched]))
			nth[matched]++
			i += len(matched)
			continue
		}
		// Not a known label. If it still looks like one, the model invented or
		// mangled it: keep it visible and report it. Text that was already in
		// the segment (a literal `{count}` in the content) is not a mangled
		// chip and must not be reported, or every such segment gets a comment.
		if loc := numericLabelRe.FindStringIndex(chipText[i:]); loc != nil && loc[0] == 0 &&
			!strings.Contains(t.literal, chipText[i:i+loc[1]]) {
			bad := chipText[i : i+loc[1]]
			if !unknownSeen[bad] {
				unknownSeen[bad] = true
				unknown = append(unknown, bad)
			}
			b.WriteString(bad)
			i += loc[1]
			continue
		}
		b.WriteByte(chipText[i])
		i++
	}
	return b.String(), unknown
}

// rawFor returns the markup for the n-th occurrence of a label, falling back to
// the last entry when the model used a label more often than we sent it.
func (t *Table) rawFor(label string, n int) string {
	var last string
	count := 0
	for _, e := range t.entries {
		if e.label != label {
			continue
		}
		last = e.raw
		if count == n {
			return e.raw
		}
		count++
	}
	return last
}
