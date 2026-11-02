package segmentedit_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/ptr"
)

// A split changes the job it is made in and nothing else: the German row
// becomes two pieces, the French job and the shared segment stay as imported.
func TestIntegrationSplit_ChangesOnlyItsJob(t *testing.T) {
	f := newFixture(t)
	frBefore := f.index(t, "fr")
	de := f.trs["de"][0]

	res := f.split(t, "de", de.ID, len("Press the button"))

	idx := f.index(t, "de")
	if len(idx.SegmentIDs) != 3 {
		t.Fatalf("German rows = %v, want the two pieces and the second line", idx.SegmentIDs)
	}
	if !slices.Equal(idx.Positions, []int32{0, 0, 1}) || !slices.Equal(idx.SubPositions, []int32{0, 1, 0}) {
		t.Errorf("numbers: positions %v sub-positions %v, want 1, 1.1, 2", idx.Positions, idx.SubPositions)
	}
	if want := []int64{de.ID, res.TranslationIDs[1], f.trs["de"][1].ID}; !slices.Equal(idx.TranslationIDs, want) {
		t.Errorf("translations = %v, want %v: the row's translation moves to the first piece", idx.TranslationIDs, want)
	}

	first, second := f.segment(t, idx.SegmentIDs[0]), f.segment(t, idx.SegmentIDs[1])
	if first.SourceText != "Press the button" || second.SourceText != "and wait for the light" {
		t.Errorf("piece sources %q / %q", first.SourceText, second.SourceText)
	}
	if ptr.Deref(second.TargetLanguage) != "de" || ptr.Deref(second.ExportKey) != "line:1" || ptr.Deref(second.GapBefore) != " " {
		t.Errorf("second piece: language %v export key %v gap %q", second.TargetLanguage, second.ExportKey, ptr.Deref(second.GapBefore))
	}
	if first.WordCount != 3 || second.WordCount != 5 {
		t.Errorf("piece word counts %d / %d, want 3 / 5", first.WordCount, second.WordCount)
	}

	moved := f.translation(t, de.ID)
	if ptr.Deref(moved.ActiveText) != targets["de"][0] || moved.Status != db.TranslationStatusNeedsReview {
		t.Errorf("first piece: %q %s, want the whole target, needs review", ptr.Deref(moved.ActiveText), moved.Status)
	}
	if empty := f.translation(t, res.TranslationIDs[1]); empty.ActiveText != nil || empty.Status != db.TranslationStatusNeedsReview {
		t.Errorf("second piece: %v %s, want empty, needs review", empty.ActiveText, empty.Status)
	}

	if frAfter := f.index(t, "fr"); !reflect.DeepEqual(frAfter, frBefore) {
		t.Errorf("the French job changed:\nbefore %+v\nafter  %+v", frBefore, frAfter)
	}
	if shared := f.segment(t, f.segs[0].ID); shared.SourceText != sources[0] || shared.TargetLanguage != nil {
		t.Errorf("the shared segment changed: %+v", shared)
	}
	for _, lang := range []string{"de", "fr"} {
		if stored, counted := f.counts(t, lang); stored != counted {
			t.Errorf("%s stats %v, rows %v", lang, stored, counted)
		}
	}
	if kind, details := f.lastHistory(t, de.ID); kind != "split" || details["from_segment"] != "1" {
		t.Errorf("history: %s %v, want a split from segment 1", kind, details)
	}
}

// Joining every piece of a split gives back the imported segment, with the
// pieces' targets joined; the removed row's comments and history move to the
// kept row with its number.
func TestIntegrationJoin_UndoesASplit(t *testing.T) {
	f := newFixture(t)
	de := f.trs["de"][0]
	res := f.split(t, "de", de.ID, len("Press the button"))
	second := res.TranslationIDs[1]
	f.exec(t, `UPDATE translations SET active_text = 'Drücken Sie die Taste' WHERE id = $1`, de.ID)
	f.exec(t, `UPDATE translations SET active_text = 'und warten Sie auf das Licht' WHERE id = $1`, second)
	f.exec(t, `INSERT INTO segment_comments (translation_id, user_id, content) VALUES ($1, $2, 'check the term')`, second, f.userID)
	f.exec(t, `INSERT INTO translation_history (translation_id, kind, text) VALUES ($1, 'human', 'und warten Sie auf das Licht')`, second)

	if rec := f.join(t, "de", de.ID, second); rec.Code != http.StatusOK {
		t.Fatalf("join: %d %s", rec.Code, rec.Body.String())
	}

	idx := f.index(t, "de")
	if want := []int64{f.segs[0].ID, f.segs[1].ID}; !slices.Equal(idx.SegmentIDs, want) {
		t.Fatalf("German rows = %v, want the imported segments %v back", idx.SegmentIDs, want)
	}
	if idx.TranslationIDs[0] != de.ID {
		t.Errorf("the row carries translation %d, want the kept %d", idx.TranslationIDs[0], de.ID)
	}
	kept := f.translation(t, de.ID)
	if want := "Drücken Sie die Taste und warten Sie auf das Licht"; ptr.Deref(kept.ActiveText) != want || kept.Status != db.TranslationStatusNeedsReview {
		t.Errorf("joined target %q %s, want %q, needs review", ptr.Deref(kept.ActiveText), kept.Status, want)
	}
	if _, err := f.q.GetTranslation(context.Background(), second); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("the removed row's translation is still there: %v", err)
	}
	var languageRows int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM segments WHERE file_id = $1 AND target_language IS NOT NULL`, f.file.ID).Scan(&languageRows); err != nil {
		t.Fatal(err)
	}
	if languageRows != 0 {
		t.Errorf("%d language rows left after the join gave the segment back", languageRows)
	}

	var commentOn int64
	var from *string
	if err := f.pool.QueryRow(context.Background(), `SELECT translation_id, from_segment FROM segment_comments WHERE content = 'check the term'`).Scan(&commentOn, &from); err != nil {
		t.Fatal(err)
	}
	if commentOn != de.ID || ptr.Deref(from) != "1.1" {
		t.Errorf("comment on %d from %v, want on %d from segment 1.1", commentOn, from, de.ID)
	}
	var movedFrom string
	if err := f.pool.QueryRow(context.Background(), `SELECT details->>'from_segment' FROM translation_history
		WHERE translation_id = $1 AND kind = 'human'`, de.ID).Scan(&movedFrom); err != nil {
		t.Fatalf("the removed row's history did not move: %v", err)
	}
	if movedFrom != "1.1" {
		t.Errorf("moved history from %q, want 1.1", movedFrom)
	}
	if kind, details := f.lastHistory(t, de.ID); kind != "joined" || !reflect.DeepEqual(details["joined_segments"], []any{"1", "1.1"}) {
		t.Errorf("last history %s %v, want the join of 1 and 1.1", kind, details)
	}
	baseline, err := f.q.GetLatestHistoryTextForTranslation(context.Background(), de.ID)
	if err != nil || ptr.Deref(baseline) != ptr.Deref(kept.ActiveText) {
		t.Errorf("edit baseline %q (%v), want the joined target", ptr.Deref(baseline), err)
	}
	if stored, counted := f.counts(t, "de"); stored != counted {
		t.Errorf("stats %v, rows %v", stored, counted)
	}
}

// Joining two of three pieces keeps a language row, and the numbers close up.
func TestIntegrationJoin_PartOfASplit(t *testing.T) {
	f := newFixture(t)
	de := f.trs["de"][0]
	res := f.split(t, "de", de.ID, len("Press the button"))
	res2 := f.split(t, "de", res.TranslationIDs[1], len("and wait"))
	if idx := f.index(t, "de"); !slices.Equal(idx.SubPositions[:3], []int32{0, 1, 2}) {
		t.Fatalf("after two splits: sub-positions %v, want 1, 1.1, 1.2", idx.SubPositions)
	}

	if rec := f.join(t, "de", de.ID, res2.TranslationIDs[1]); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "join_not_adjacent") {
		t.Errorf("join of 1 and 1.2: %d %s, want 400 join_not_adjacent", rec.Code, rec.Body.String())
	}
	if rec := f.join(t, "de", res2.TranslationIDs...); rec.Code != http.StatusOK {
		t.Fatalf("join of 1.1 and 1.2: %d %s", rec.Code, rec.Body.String())
	}
	idx := f.index(t, "de")
	if !slices.Equal(idx.Positions, []int32{0, 0, 1}) || !slices.Equal(idx.SubPositions, []int32{0, 1, 0}) {
		t.Errorf("numbers %v / %v, want 1, 1.1, 2", idx.Positions, idx.SubPositions)
	}
	if got := f.segment(t, idx.SegmentIDs[1]); got.SourceText != "and wait for the light" || got.TargetLanguage == nil {
		t.Errorf("joined piece %q (language %v), want a language row with both sources", got.SourceText, got.TargetLanguage)
	}
}

// Export folds the pieces back into the line they came from; the other
// language exports as before.
func TestIntegrationSplit_ExportRebuildsTheFile(t *testing.T) {
	f := newFixture(t)
	res := f.split(t, "de", f.trs["de"][0].ID, len("Press the button"))
	f.exec(t, `UPDATE translations SET active_text = 'Drücken Sie die Taste' WHERE id = $1`, res.TranslationIDs[0])
	svc := pipeline.NewService(f.q, pipeline.NewPgxTxRunner(f.pool), storage{[]byte(original)})

	de, _, err := svc.ExportFile(context.Background(), f.tenantID, f.file.ID, "de")
	if err != nil {
		t.Fatalf("export de: %v", err)
	}
	if want := "Drücken Sie die Taste and wait for the light\nDer nächste\n"; string(de) != want {
		t.Errorf("de export = %q, want %q (an untranslated piece keeps its source)", de, want)
	}
	f.exec(t, `UPDATE translations SET active_text = 'und warten Sie auf das Licht' WHERE id = $1`, res.TranslationIDs[1])
	if de, _, err = svc.ExportFile(context.Background(), f.tenantID, f.file.ID, "de"); err != nil || string(de) != "Drücken Sie die Taste und warten Sie auf das Licht\nDer nächste\n" {
		t.Errorf("de export = %q (%v)", de, err)
	}
	fr, _, err := svc.ExportFile(context.Background(), f.tenantID, f.file.ID, "fr")
	if err != nil || string(fr) != targets["fr"][0]+"\n"+targets["fr"][1]+"\n" {
		t.Errorf("fr export = %q (%v), want the French targets", fr, err)
	}

	xliff, _, err := svc.ExportBilingualXLIFF(context.Background(), f.tenantID, f.file.ID, "en", "de")
	if err != nil {
		t.Fatalf("bilingual export: %v", err)
	}
	for _, want := range []string{`id="line:1@`, "Press the button", "and wait for the light"} {
		if !strings.Contains(string(xliff), want) {
			t.Errorf("bilingual XLIFF lacks %q:\n%s", want, xliff)
		}
	}
	if strings.Contains(string(xliff), `id="line:1"`) {
		t.Errorf("bilingual XLIFF still holds the replaced row line:1:\n%s", xliff)
	}
}

func (f *fixture) lastHistory(t *testing.T, translationID int64) (string, map[string]any) {
	t.Helper()
	var kind string
	var raw []byte
	if err := f.pool.QueryRow(context.Background(), `SELECT kind, details FROM translation_history
		WHERE translation_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, translationID).Scan(&kind, &raw); err != nil {
		t.Fatalf("last history of %d: %v", translationID, err)
	}
	details := map[string]any{}
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatal(err)
	}
	return kind, details
}
