package translation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// A single write refused because another user holds the segment changes
// nothing, the task included: a manager's draft, confirm or clear on a
// completed task is refused before the task guard would reopen it.
func TestIntegrationHold_RefusedSingleWriteKeepsCompletedTaskClosed(t *testing.T) {
	writes := []struct {
		name, method, suffix string
		body                 map[string]any
	}{
		{"draft", http.MethodPut, "/draft", map[string]any{"human_text": "Sichern"}},
		{"confirm", http.MethodPut, "", map[string]any{"human_text": "Sichern"}},
		{"clear", http.MethodPost, "/clear", nil},
	}
	for _, w := range writes {
		t.Run(w.name, func(t *testing.T) {
			f := newCompletedHoldFixture(t)
			wantHeld(t, f.do(t, f.other, w.method, f.trPath(f.held, w.suffix), w.body), f.holder)
			f.wantTaskClosed(t, w.name)
			f.assertUnchanged(t, f.held)
		})
	}
}

func TestIntegrationHold_RefusedBatchDraftKeepsCompletedTaskClosed(t *testing.T) {
	f := newCompletedHoldFixture(t)
	rec := f.do(t, f.other, http.MethodPut, fmt.Sprintf("/api/files/%d/translations/batch-draft", f.fileID), map[string]any{
		"fixes": []map[string]any{{"translation_id": f.held.ID, "human_text": "Sichern"}},
	})
	testutil.AssertStatus(t, rec, http.StatusOK)
	var body struct {
		Data struct {
			Held []int64 `json:"held"`
		} `json:"data"`
		TaskReopened bool `json:"task_reopened"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(body.Data.Held) != 1 || body.Data.Held[0] != f.held.ID || body.TaskReopened {
		t.Errorf("batch draft = %s, want the row held and no reopen", rec.Body.String())
	}
	f.wantTaskClosed(t, "batch draft")
	f.assertUnchanged(t, f.held)
}

// newCompletedHoldFixture: the job's only task is completed, and the holder
// has f.held open.
func newCompletedHoldFixture(t *testing.T) *holdFixture {
	t.Helper()
	f := newHoldFixture(t)
	testutil.SeedWorkflow(t, f.q, f.settingsProfile, f.job.ID,
		testutil.WorkflowStepSpec{Name: "Translate", Assignee: &f.holder.ID, Status: "completed"})
	f.hold(t, f.held, f.holder)
	return f
}

func (f *holdFixture) wantTaskClosed(t *testing.T, after string) {
	t.Helper()
	if _, err := f.q.GetActiveStepForJob(context.Background(), f.job.ID); err == nil {
		t.Errorf("after the refused %s the completed task is in progress again", after)
	}
}
