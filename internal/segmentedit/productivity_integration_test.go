package segmentedit_test

import (
	"context"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// stepProductivity is the German step's segments and words edited, as the
// project's productivity report counts them.
func (f *fixture) stepProductivity(t *testing.T) (segments, words int32) {
	t.Helper()
	rows, err := f.q.GetStepProductivityByProject(context.Background(), f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.TargetLanguage == "de" {
			segments += r.SegmentsEdited
			words += r.WordsEdited
		}
	}
	return segments, words
}

// editInStep records a human edit of each translation in the German step.
func (f *fixture) editInStep(t *testing.T, ids ...int64) {
	t.Helper()
	step := testutil.SeedJobStep(t, f.q, f.project.SettingsProfileID, f.jobs["de"].ID, &f.userID)
	for _, id := range ids {
		f.exec(t, `INSERT INTO translation_history (translation_id, kind, text, created_by, job_step_assignment_id)
			VALUES ($1, 'human', 'edit', $2, $3)`, id, f.userID, step)
	}
}

// The step productivity report keeps what earlier edits counted: a split
// does not shrink an edit of the whole segment to its first piece.
func TestIntegrationProductivity_SplitKeepsPastWords(t *testing.T) {
	f := newFixture(t)
	de := f.trs["de"][0]
	f.editInStep(t, de.ID)
	segmentsBefore, wordsBefore := f.stepProductivity(t)

	f.split(t, "de", de.ID, len("Press the button"))
	if segments, words := f.stepProductivity(t); segments != segmentsBefore || words != wordsBefore {
		t.Errorf("after the split: %d segments, %d words; want %d, %d", segments, words, segmentsBefore, wordsBefore)
	}
}

// A join does not fold the edits of the rows it removes into the kept row:
// each still counts as its own segment with its own words.
func TestIntegrationProductivity_JoinKeepsPastSegments(t *testing.T) {
	f := newSentenceFixture(t)
	de := f.trs["de"]
	f.editInStep(t, de[0].ID, de[1].ID)
	segmentsBefore, wordsBefore := f.stepProductivity(t)

	if rec := f.join(t, "de", de[0].ID, de[1].ID); rec.Code != http.StatusOK {
		t.Fatalf("join: %d %s", rec.Code, rec.Body.String())
	}
	if segments, words := f.stepProductivity(t); segments != segmentsBefore || words != wordsBefore {
		t.Errorf("after the join: %d segments, %d words; want %d, %d", segments, words, segmentsBefore, wordsBefore)
	}
}

// An edit before a split and another after it, in the same step, count once,
// with the words of the earliest: the whole segment, not the first piece.
func TestIntegrationProductivity_EditsAroundASplitCountTheEarliest(t *testing.T) {
	f := newFixture(t)
	de := f.trs["de"][0]
	step := testutil.SeedJobStep(t, f.q, f.project.SettingsProfileID, f.jobs["de"].ID, &f.userID)
	edit := func() {
		f.exec(t, `INSERT INTO translation_history (translation_id, kind, text, created_by, job_step_assignment_id)
			VALUES ($1, 'human', 'edit', $2, $3)`, de.ID, f.userID, step)
	}
	edit()
	f.split(t, "de", de.ID, len("Press the button"))
	edit()

	if segments, words := f.stepProductivity(t); segments != 1 || words != 8 {
		t.Errorf("%d segments, %d words; want 1 segment with the 8 words of the edit before the split", segments, words)
	}
}
