package pipeline

import (
	"slices"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
)

func TestJoinSplitPieces_FoldsPiecesIntoTheirOriginalKey(t *testing.T) {
	rows := []db.ListApprovedForExportRow{
		{SegmentKey: ptr.To("line:1"), SourceText: "Before.", ActiveText: ptr.To("Davor.")},
		{SegmentKey: ptr.To("line:2@7"), ExportKey: ptr.To("line:2"), SourceText: "Click here now.",
			SourceRichText: ptr.To(`Click <g id="1">here</g> now.`),
			ActiveText:     ptr.To("Jetzt hier klicken."), ActiveRichText: ptr.To(`Jetzt <g id="1">hier</g> klicken.`)},
		{SegmentKey: ptr.To("line:2@8"), ExportKey: ptr.To("line:2"), SourceText: "And there.",
			SourceRichText: ptr.To(`And <g id="1">there</g>.`), ChipBase: 1, GapBefore: ptr.To(" ")},
		{SegmentKey: ptr.To("line:3@9"), ExportKey: ptr.To("line:3"), SourceText: "Untouched", ActiveText: ptr.To("")},
		{SegmentKey: ptr.To("line:3@10"), ExportKey: ptr.To("line:3"), SourceText: "pieces.", GapBefore: ptr.To(" ")},
		{SegmentKey: ptr.To("line:4"), SourceText: "After."},
	}
	got := joinSplitPieces(rows)

	keys := make([]string, len(got))
	for i, r := range got {
		keys[i] = ptr.Deref(r.SegmentKey)
	}
	if want := []string{"line:1", "line:2", "line:3", "line:4"}; !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}

	split := got[1]
	if ptr.Deref(split.ActiveText) != "Jetzt hier klicken. And there." {
		t.Errorf("text = %q: a translated piece keeps its text, an untranslated one its source", ptr.Deref(split.ActiveText))
	}
	if want := `Jetzt <g id="1">hier</g> klicken. And <g id="2">there</g>.`; ptr.Deref(split.ActiveRichText) != want {
		t.Errorf("rich = %q, want %q", ptr.Deref(split.ActiveRichText), want)
	}
	if split.SourceText != "Click here now. And there." || ptr.Deref(split.SourceRichText) != `Click <g id="1">here</g> now. And <g id="2">there</g>.` {
		t.Errorf("source = %q / %q, want the original segment's", split.SourceText, ptr.Deref(split.SourceRichText))
	}
	if split.ExportKey != nil || split.GapBefore != nil || split.ChipBase != 0 {
		t.Errorf("folded row still carries piece fields: %+v", split)
	}

	if untouched := got[2]; untouched.ActiveText != nil || untouched.SourceText != "Untouched pieces." {
		t.Errorf("a key with no translated piece: text %v source %q, want no translation", untouched.ActiveText, untouched.SourceText)
	}

	if plain := []db.ListApprovedForExportRow{{SegmentKey: ptr.To("a")}}; len(joinSplitPieces(plain)) != 1 {
		t.Error("rows without pieces changed")
	}
}
