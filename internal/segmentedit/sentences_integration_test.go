package segmentedit_test

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"openloc/internal/pipeline"
	"openloc/internal/ptr"
)

// A TXT line of three sentences, stored as an upload splits it, and a line of
// one.
const threeSentences = "One one. Two two. Three three.\nNext one\n"

func newSentenceFixture(t *testing.T) *fixture {
	t.Helper()
	return newFileFixture(t, threeSentences, []fileSegment{
		{"line:1#1", "One one."}, {"line:1#2", "Two two."}, {"line:1#3", "Three three."}, {"line:2", "Next one"},
	}, map[string][]string{
		"de": {"Eins eins.", "Zwei zwei.", "Drei drei.", "Der nächste"},
		"fr": {"Un un.", "Deux deux.", "Trois trois.", "Le suivant"},
	})
}

func (f *fixture) export(t *testing.T, lang string) string {
	t.Helper()
	out, _, err := pipeline.NewService(f.q, pipeline.NewPgxTxRunner(f.pool), f.store).ExportFile(context.Background(), f.tenantID, f.file.ID, lang)
	if err != nil {
		t.Fatalf("export %s: %v", lang, err)
	}
	return string(out)
}

// Joining two sentences of one line makes one row over both, numbered with
// the first's number; export writes its translation over both sentences and
// the space between them. The other language keeps its three sentences.
func TestIntegrationJoin_SentencesOfOneBlock(t *testing.T) {
	f := newSentenceFixture(t)
	frBefore := f.index(t, "fr")
	de := f.trs["de"]

	if rec := f.join(t, "de", de[0].ID, de[1].ID); rec.Code != http.StatusOK {
		t.Fatalf("join: %d %s", rec.Code, rec.Body.String())
	}
	idx := f.index(t, "de")
	if !slices.Equal(idx.Positions, []int32{0, 2, 3}) || idx.TranslationIDs[0] != de[0].ID {
		t.Fatalf("rows: positions %v translations %v, want 1, 3, 4 with the first row kept", idx.Positions, idx.TranslationIDs)
	}
	joined := f.segment(t, idx.SegmentIDs[0])
	if joined.SourceText != "One one. Two two." || ptr.Deref(joined.ExportKey) != "line:1#1-2" {
		t.Errorf("joined row: %q export key %v", joined.SourceText, joined.ExportKey)
	}
	if got := ptr.Deref(f.translation(t, de[0].ID).ActiveText); got != "Eins eins. Zwei zwei." {
		t.Errorf("joined target %q", got)
	}
	if out := f.export(t, "de"); out != "Eins eins. Zwei zwei. Drei drei.\nDer nächste\n" {
		t.Errorf("de export = %q", out)
	}
	if out := f.export(t, "fr"); out != "Un un. Deux deux. Trois trois.\nLe suivant\n" {
		t.Errorf("fr export = %q", out)
	}
	if after := f.index(t, "fr"); !reflect.DeepEqual(after, frBefore) {
		t.Errorf("the French job changed")
	}
	if stored, counted := f.counts(t, "de"); stored != counted {
		t.Errorf("stats %v, rows %v", stored, counted)
	}

	// The joined row joins on with the next sentence, into a run of all three.
	if rec := f.join(t, "de", de[0].ID, de[2].ID); rec.Code != http.StatusOK {
		t.Fatalf("second join: %d %s", rec.Code, rec.Body.String())
	}
	if got := f.segment(t, f.index(t, "de").SegmentIDs[0]); got.SourceText != threeSentences[:30] || ptr.Deref(got.ExportKey) != "line:1#1-3" {
		t.Errorf("run of three: %q %v", got.SourceText, got.ExportKey)
	}
	if out := f.export(t, "de"); out != "Eins eins. Zwei zwei. Drei drei.\nDer nächste\n" {
		t.Errorf("de export after the second join = %q", out)
	}
}

// A sentence split in this job joins with the next sentence only with all of
// its pieces; sentences of two blocks never join.
func TestIntegrationJoin_SentencesRefusals(t *testing.T) {
	f := newSentenceFixture(t)
	de := f.trs["de"]
	res := f.split(t, "de", de[1].ID, len("Two"))

	rec := f.join(t, "de", res.TranslationIDs[1], de[2].ID)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "join_partial") {
		t.Errorf("join of a piece with the next sentence: %d %s, want 400 join_partial", rec.Code, rec.Body.String())
	}
	rec = f.join(t, "de", de[2].ID, de[3].ID)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "join_other_unit") {
		t.Errorf("join across lines: %d %s, want 400 join_other_unit", rec.Code, rec.Body.String())
	}

	if rec := f.join(t, "de", res.TranslationIDs[0], res.TranslationIDs[1], de[2].ID); rec.Code != http.StatusOK {
		t.Fatalf("join of both pieces with the next sentence: %d %s", rec.Code, rec.Body.String())
	}
	idx := f.index(t, "de")
	if got := f.segment(t, idx.SegmentIDs[1]); got.SourceText != "Two two. Three three." || ptr.Deref(got.ExportKey) != "line:1#2-3" {
		t.Errorf("joined row %q %v", got.SourceText, got.ExportKey)
	}
	if out := f.export(t, "de"); out != "Eins eins. Zwei zwei. Drei drei.\nDer nächste\n" {
		t.Errorf("de export = %q", out)
	}
}

// A run of joined sentences splits like any row, and joining its pieces
// gives the run back.
func TestIntegrationJoin_SplitAJoinedRun(t *testing.T) {
	f := newSentenceFixture(t)
	de := f.trs["de"]
	if rec := f.join(t, "de", de[0].ID, de[1].ID); rec.Code != http.StatusOK {
		t.Fatalf("join: %d %s", rec.Code, rec.Body.String())
	}
	res := f.split(t, "de", de[0].ID, len("One one."))
	idx := f.index(t, "de")
	if !slices.Equal(idx.SubPositions[:2], []int32{0, 1}) {
		t.Fatalf("pieces of the run: sub-positions %v", idx.SubPositions)
	}
	f.exec(t, `UPDATE translations SET active_text = 'Eins.' WHERE id = $1`, res.TranslationIDs[0])
	f.exec(t, `UPDATE translations SET active_text = 'Zwei.' WHERE id = $1`, res.TranslationIDs[1])
	if out := f.export(t, "de"); out != "Eins. Zwei. Drei drei.\nDer nächste\n" {
		t.Errorf("export of the split run = %q", out)
	}
	if rec := f.join(t, "de", res.TranslationIDs...); rec.Code != http.StatusOK {
		t.Fatalf("join back: %d %s", rec.Code, rec.Body.String())
	}
	if got := f.segment(t, f.index(t, "de").SegmentIDs[0]); got.SourceText != "One one. Two two." || ptr.Deref(got.ExportKey) != "line:1#1-2" {
		t.Errorf("run after the join back: %q %v", got.SourceText, got.ExportKey)
	}
}
