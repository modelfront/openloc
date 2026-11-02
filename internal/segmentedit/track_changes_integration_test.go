package segmentedit_test

import (
	"context"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Track changes compares a row with the text it held when its last step round
// ended. After a join, that is the kept row's own text, not the text of a row
// the join folded into it, even when that row was edited later in the round.
func TestIntegrationJoin_TrackChangesBaselineIsTheKeptRows(t *testing.T) {
	f := newSentenceFixture(t)
	de := f.trs["de"]
	step := testutil.SeedJobStep(t, f.q, f.project.SettingsProfileID, f.jobs["de"].ID, &f.userID)
	f.exec(t, `INSERT INTO translation_history (translation_id, kind, text, created_at)
		VALUES ($1, 'human', 'Eins alt.', now() - interval '3 hours'),
		       ($2, 'human', 'Zwei alt.', now() - interval '2 hours')`, de[0].ID, de[1].ID)
	f.exec(t, `INSERT INTO translation_step_statuses (tenant_id, translation_id, job_step_assignment_id, round, started_at, ended_at)
		VALUES ($1, $2, $4, 1, now() - interval '4 hours', now() - interval '1 hour'),
		       ($1, $3, $4, 1, now() - interval '4 hours', now() - interval '1 hour')`, f.tenantID, de[0].ID, de[1].ID, step)

	baseline := func() string {
		t.Helper()
		rows, err := f.q.ListEditorPreviousStepTexts(context.Background(), db.ListEditorPreviousStepTextsParams{
			TranslationIds: []int64{de[0].ID}, TenantID: f.tenantID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("baselines: %d rows, want 1", len(rows))
		}
		return rows[0].Text
	}
	if got := baseline(); got != "Eins alt." {
		t.Fatalf("baseline before the join = %q, want the row's own text", got)
	}
	if rec := f.join(t, "de", de[0].ID, de[1].ID); rec.Code != http.StatusOK {
		t.Fatalf("join: %d %s", rec.Code, rec.Body.String())
	}
	if got := baseline(); got != "Eins alt." {
		t.Errorf("baseline after the join = %q, want the kept row's own text", got)
	}
}
