package inlinetag

import (
	"reflect"
	"strings"
	"testing"
)

// The customer case that started this: a paired formatting tag whose content
// splits in the target language. The model can only get this right if it sees
// the tags, which is what Encode gives it.
func TestEncodeDecode_SplitPairedTag(t *testing.T) {
	src := `<g id="1" ctype="italic">To update the displayed content, click </g><g id="2" ctype="bold">Refresh</g>`

	chips, tbl := Encode(src)
	want := `<1>To update the displayed content, click </1><2>Refresh</2>`
	if chips != want {
		t.Fatalf("Encode:\n got %q\nwant %q", chips, want)
	}

	// The model splits the italic run around the bold one — legal, and the
	// whole point of the exercise.
	out := `<1>Um den Content zu aktualisieren, die Schaltfläche </1><2>Refresh</2><1> anklicken.</1>`
	rich, unknown := Decode(out, tbl)
	wantRich := `<g id="1" ctype="italic">Um den Content zu aktualisieren, die Schaltfläche </g>` +
		`<g id="2" ctype="bold">Refresh</g><g id="1" ctype="italic"> anklicken.</g>`
	if rich != wantRich {
		t.Errorf("Decode:\n got %q\nwant %q", rich, wantRich)
	}
	if len(unknown) != 0 {
		t.Errorf("unknown = %v, want none", unknown)
	}
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	cases := []struct {
		name, rich, chips string
	}{
		{
			name:  "bpt/ept pair",
			rich:  `Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept> now`,
			chips: `Click <1>here</1> now`,
		},
		{
			name:  "nested g",
			rich:  `<g id="1" ctype="bold">bold <g id="2" ctype="italic">and italic</g></g>`,
			chips: `<1>bold <2>and italic</2></1>`,
		},
		{
			name:  "self-closing x",
			rich:  `line one<x id="3" ctype="lb"/>line two`,
			chips: `line one{3}line two`,
		},
		{
			name:  "ph shows its content",
			rich:  `Hello <ph id="2">{name}</ph>, you have <ph id="4">%d</ph> messages`,
			chips: `Hello {name}, you have %d messages`,
		},
		{
			name:  "html paired tag",
			rich:  `Press <b>OK</b> to continue`,
			chips: `Press <1>OK</1> to continue`,
		},
		{
			name:  "plain text is untouched",
			rich:  `No tags at all.`,
			chips: `No tags at all.`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chips, tbl := Encode(c.rich)
			if chips != c.chips {
				t.Fatalf("Encode:\n got %q\nwant %q", chips, c.chips)
			}
			back, unknown := Decode(chips, tbl)
			if back != c.rich {
				t.Errorf("round trip:\n got %q\nwant %q", back, c.rich)
			}
			if len(unknown) != 0 {
				t.Errorf("unknown = %v, want none", unknown)
			}
		})
	}
}

// Ids come from separate counters per parser, so two different elements can
// carry the same id in one segment. They must not end up sharing a label.
func TestEncode_LabelCollision(t *testing.T) {
	// Both are standalone with id 4, and the ph has no content to show, so both
	// want the label `{4}`.
	rich := `<ph id="4"></ph> and <x id="4"/>`
	chips, tbl := Encode(rich)

	if strings.Count(chips, "{4}") > 1 {
		t.Fatalf("Encode = %q — two different tags share one label", chips)
	}
	back, unknown := Decode(chips, tbl)
	if back != rich {
		t.Fatalf("round trip:\n got %q\nwant %q", back, rich)
	}
	if len(unknown) != 0 {
		t.Fatalf("unknown = %v, want none", unknown)
	}

	seen := map[string]string{}
	for _, e := range tbl.entries {
		if prev, ok := seen[e.label]; ok && prev != e.raw {
			t.Errorf("label %q maps to both %q and %q", e.label, prev, e.raw)
		}
		seen[e.label] = e.raw
	}
}

// Two identical placeholders share one label on purpose — they are
// interchangeable. Decode resolves them by occurrence order.
func TestDecode_DuplicateLabelsByOccurrence(t *testing.T) {
	rich := `<ph id="1">%s</ph> of <ph id="2">%s</ph>`
	chips, tbl := Encode(rich)
	if chips != `%s of %s` {
		t.Fatalf("Encode = %q", chips)
	}
	back, _ := Decode(`%s von %s`, tbl)
	want := `<ph id="1">%s</ph> von <ph id="2">%s</ph>`
	if back != want {
		t.Errorf("Decode:\n got %q\nwant %q", back, want)
	}
}

// A label that also occurs as real content would decode the content into
// markup. The encoder must pick a different label instead.
func TestEncode_LabelClashesWithLiteralText(t *testing.T) {
	rich := `press <x id="1"/> to see {1} results`

	chips, tbl := Encode(rich)
	if strings.Count(chips, "{1}") != 1 {
		t.Fatalf("Encode = %q — the tag label must not be {1}, which is already in the text", chips)
	}
	back, unknown := Decode(chips, tbl)
	if back != rich {
		t.Errorf("round trip:\n got %q\nwant %q", back, rich)
	}
	// The literal `{1}` is content, not a mangled chip.
	if len(unknown) != 0 {
		t.Errorf("unknown = %v, want none", unknown)
	}
}

// The QA tag rule cannot see a leftover chip label — it renders identically to
// a real placeholder. Decode reporting it is the only signal there is.
func TestDecode_UnknownLabelReported(t *testing.T) {
	_, tbl := Encode(`<g id="1" ctype="bold">bold</g>`)

	rich, unknown := Decode(`<1>fett</1> plus {9} and <7>stray</7>`, tbl)

	if !strings.Contains(rich, `{9}`) || !strings.Contains(rich, `<7>`) {
		t.Errorf("unknown labels must stay visible in the text, got %q", rich)
	}
	want := []string{"{9}", "<7>", "</7>"}
	if !reflect.DeepEqual(unknown, want) {
		t.Errorf("unknown = %v, want %v", unknown, want)
	}
}

func TestDecode_DroppedAndReorderedChips(t *testing.T) {
	rich := `<g id="1" ctype="bold">one</g> <g id="2" ctype="italic">two</g>`
	_, tbl := Encode(rich)

	// Dropped: the tag simply is not in the output. No error, no invention.
	back, unknown := Decode(`eins zwei`, tbl)
	if back != `eins zwei` {
		t.Errorf("dropped chips: got %q", back)
	}
	if len(unknown) != 0 {
		t.Errorf("dropped chips: unknown = %v, want none", unknown)
	}

	// Reordered: each label still resolves to its own markup.
	back, unknown = Decode(`<2>zwei</2> <1>eins</1>`, tbl)
	want := `<g id="2" ctype="italic">zwei</g> <g id="1" ctype="bold">eins</g>`
	if back != want {
		t.Errorf("reordered:\n got %q\nwant %q", back, want)
	}
	if len(unknown) != 0 {
		t.Errorf("reordered: unknown = %v, want none", unknown)
	}
}

// A segment that is nothing but a tag still encodes to a non-blank string, so
// it reaches the model rather than being skipped as empty.
func TestEncode_TagOnlySegmentIsNotBlank(t *testing.T) {
	chips, _ := Encode(`<x id="1" ctype="lb"/>`)
	if strings.TrimSpace(chips) == "" {
		t.Fatalf("tag-only segment encoded to blank %q", chips)
	}
}

// Source and target must share one table, or the two sides label the same tag
// differently and the model has nothing to match on.
func TestEncodeWith_SharedLabelsAcrossSourceAndTarget(t *testing.T) {
	src := `<g id="1" ctype="bold">Save</g> the <g id="2" ctype="italic">file</g>`
	tgt := `<g id="2" ctype="italic">Datei</g> <g id="1" ctype="bold">speichern</g>`

	tbl := NewTable()
	srcChips := EncodeWith(tbl, src)
	tgtChips := EncodeWith(tbl, tgt)

	if srcChips != `<1>Save</1> the <2>file</2>` {
		t.Fatalf("source = %q", srcChips)
	}
	if tgtChips != `<2>Datei</2> <1>speichern</1>` {
		t.Fatalf("target = %q — labels must match the source's", tgtChips)
	}
}

func TestDecode_NilAndEmptyTable(t *testing.T) {
	if got, _ := Decode("unchanged", nil); got != "unchanged" {
		t.Errorf("nil table: got %q", got)
	}
	_, tbl := Encode("no tags here")
	if got, _ := Decode("unchanged", tbl); got != "unchanged" {
		t.Errorf("empty table: got %q", got)
	}
}

// The demo's two shapes, pinned because they are the feature's whole point: the
// wrong one (a pair widened to swallow another) and the right one (that pair
// split around it) must BOTH decode faithfully. If the wrong shape failed to
// decode there would be nothing for post-editing to correct; if the right one
// failed, the correction could never be stored.
func TestDecode_WidenedAndSplitPairBothRoundTrip(t *testing.T) {
	src := `<g id="1" ctype="italic">To update the displayed content, click </g><g id="2" ctype="bold">Refresh</g>`
	chips, tbl := Encode(src)
	if chips != `<1>To update the displayed content, click </1><2>Refresh</2>` {
		t.Fatalf("Encode = %q", chips)
	}

	// What a model produces when it refuses to split: tag 2 nested inside tag 1.
	wrong, unknown := Decode(`<1>Um den Content zu aktualisieren, die Schaltfläche <2>Refresh</2> anklicken.</1>`, tbl)
	wantWrong := `<g id="1" ctype="italic">Um den Content zu aktualisieren, die Schaltfläche ` +
		`<g id="2" ctype="bold">Refresh</g> anklicken.</g>`
	if wrong != wantWrong {
		t.Errorf("widened pair:\n got %q\nwant %q", wrong, wantWrong)
	}
	if len(unknown) != 0 {
		t.Errorf("widened pair: unknown = %v, want none", unknown)
	}

	// The correction: tag 1 split around tag 2, so its label appears twice.
	right, unknown := Decode(`<1>Um den Content zu aktualisieren, die Schaltfläche </1><2>Refresh</2><1> anklicken.</1>`, tbl)
	wantRight := `<g id="1" ctype="italic">Um den Content zu aktualisieren, die Schaltfläche </g>` +
		`<g id="2" ctype="bold">Refresh</g><g id="1" ctype="italic"> anklicken.</g>`
	if right != wantRight {
		t.Errorf("split pair:\n got %q\nwant %q", right, wantRight)
	}
	if len(unknown) != 0 {
		t.Errorf("split pair: unknown = %v, want none", unknown)
	}

	// Both carry the same words, which is why the apply check has to compare the
	// rich forms rather than the stripped ones.
	if Strip := textRuns(wrong); Strip != textRuns(right) {
		t.Fatalf("the two shapes should differ only in markup, got %q vs %q", Strip, textRuns(right))
	}
}
