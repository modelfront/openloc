package segmentedit_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"openloc/internal/metrics"
)

// wordsProcessed runs the words_processed measure over today for the fixture's
// tenant: the source words and segments a human confirmed or edited.
func (f *fixture) wordsProcessed(t *testing.T) (words, segments int64) {
	t.Helper()
	res, err := metrics.Run(context.Background(), f.pool, f.tenantID, metrics.Request{
		Measure: "words_processed", From: time.Now().AddDate(0, 0, -1), To: time.Now().AddDate(0, 0, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("words_processed rows = %+v, want one", res.Rows)
	}
	num := func(v any) int64 {
		switch n := v.(type) {
		case int64:
			return n
		case float64:
			return int64(n)
		case int32:
			return int64(n)
		}
		t.Fatalf("unexpected number %T", v)
		return 0
	}
	return num(res.Rows[0]["words"]), num(res.Rows[0]["segments"])
}

// A split keeps the words an earlier edit counted: the edit was of the whole
// segment, not of the first piece the translation moved to.
func TestIntegrationMetrics_SplitKeepsPastWords(t *testing.T) {
	f := newFixture(t)
	de := f.trs["de"][0]
	f.exec(t, `INSERT INTO translation_history (translation_id, kind, text) VALUES ($1, 'human', 'Drücken')`, de.ID)
	wordsBefore, segmentsBefore := f.wordsProcessed(t)

	f.split(t, "de", de.ID, len("Press the button"))
	if words, segments := f.wordsProcessed(t); words != wordsBefore || segments != segmentsBefore {
		t.Errorf("after the split: %d words in %d segments, want %d in %d", words, segments, wordsBefore, segmentsBefore)
	}
}

// A join keeps what earlier edits of its rows counted: each row's words, and
// each row as its own segment.
func TestIntegrationMetrics_JoinKeepsPastWordsAndSegments(t *testing.T) {
	f := newSentenceFixture(t)
	de := f.trs["de"]
	f.exec(t, `INSERT INTO translation_history (translation_id, kind, text) VALUES ($1, 'human', 'Eins.'), ($2, 'human', 'Zwei.')`, de[0].ID, de[1].ID)
	wordsBefore, segmentsBefore := f.wordsProcessed(t)

	if rec := f.join(t, "de", de[0].ID, de[1].ID); rec.Code != http.StatusOK {
		t.Fatalf("join: %d %s", rec.Code, rec.Body.String())
	}
	if words, segments := f.wordsProcessed(t); words != wordsBefore || segments != segmentsBefore {
		t.Errorf("after the join: %d words in %d segments, want %d in %d", words, segments, wordsBefore, segmentsBefore)
	}
}

// A translation edited both before and after a split counts once in the
// period: one segment, with the words of one of its edits.
func TestIntegrationMetrics_EditsAroundASplitCountOnce(t *testing.T) {
	f := newFixture(t)
	de := f.trs["de"][0]
	f.exec(t, `INSERT INTO translation_history (translation_id, kind, text) VALUES ($1, 'human', 'Drücken')`, de.ID)
	f.split(t, "de", de.ID, len("Press the button"))
	f.exec(t, `INSERT INTO translation_history (translation_id, kind, text) VALUES ($1, 'human', 'Drücken Sie')`, de.ID)

	words, segments := f.wordsProcessed(t)
	if segments != 1 {
		t.Errorf("segments = %d, want 1: one translation edited twice", segments)
	}
	if words != 8 && words != 3 {
		t.Errorf("words = %d, want the words of one edit (8 before the split or 3 after), not their sum", words)
	}
}

// A split joined back keeps what an edit before the split counted: the
// values the split stored stay through the join's move.
func TestIntegrationMetrics_SplitThenJoinBackKeepsPastWords(t *testing.T) {
	f := newFixture(t)
	de := f.trs["de"][0]
	f.exec(t, `INSERT INTO translation_history (translation_id, kind, text) VALUES ($1, 'human', 'Drücken')`, de.ID)
	wordsBefore, segmentsBefore := f.wordsProcessed(t)

	res := f.split(t, "de", de.ID, len("Press the button"))
	if rec := f.join(t, "de", res.TranslationIDs...); rec.Code != http.StatusOK {
		t.Fatalf("join back: %d %s", rec.Code, rec.Body.String())
	}
	if words, segments := f.wordsProcessed(t); words != wordsBefore || segments != segmentsBefore {
		t.Errorf("after split and join back: %d words in %d segments, want %d in %d", words, segments, wordsBefore, segmentsBefore)
	}
}
