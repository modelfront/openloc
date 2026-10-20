package taskedit_test

import (
	"context"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/stepround"
	"openloc/internal/testutil"
)

// Reopening a completed task by editing it opens a new round of that task, and
// the task keeps the statuses it ended with: only the edited segment changes.
func TestIntegrationReopenByEdit_OpensRoundKeepingStatuses(t *testing.T) {
	f := newFixture(t, "reopen-round", 2, "completed", "completed")
	ctx := context.Background()
	if err := stepround.Close(ctx, f.q, f.steps[1], stepround.Completed); err != nil {
		t.Fatal(err)
	}
	round := func() int32 {
		t.Helper()
		rows, err := f.q.ListJobStepAssignments(ctx, f.jobID)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.ID == f.steps[1] {
				return r.Round
			}
		}
		t.Fatal("review assignment missing")
		return 0
	}
	before := round()

	testutil.AssertStatus(t, f.draft(t, f.trs[0], "Hallo neu", f.ownerID, "owner"), http.StatusOK)

	if got := round(); got != before+1 {
		t.Errorf("review round = %d, want %d after the reopen", got, before+1)
	}
	other, err := f.q.GetTranslation(ctx, f.trs[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if other.Status != db.TranslationStatusApproved {
		t.Errorf("untouched segment status = %s, want approved: a reopened task keeps its statuses", other.Status)
	}
}
