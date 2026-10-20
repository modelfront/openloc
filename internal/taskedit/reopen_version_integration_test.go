package taskedit_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/stepround"
	"openloc/internal/testutil"
)

// The edit that reopens a completed task carries the version the editor read
// (expected_updated_at). The reopen restores the task's statuses; it must not
// move updated_at on segments it leaves unchanged, or the reopening edit — and
// every other open editor's next save — fails as a conflict nobody caused.

func completedWithRound(t *testing.T, prefix string) fixture {
	t.Helper()
	f := newFixture(t, prefix, 2, "completed", "completed")
	if err := stepround.Close(context.Background(), f.q, f.steps[1], stepround.Completed); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) version(t *testing.T, tr db.Translation) string {
	t.Helper()
	got, err := f.q.GetTranslation(context.Background(), tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got.UpdatedAt.Time.Format(time.RFC3339Nano)
}

func (f fixture) put(t *testing.T, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, "PUT", path, body, f.ownerID, f.tenantID, "owner")
}

func TestIntegrationReopenByEdit_EditorVersionStillMatches(t *testing.T) {
	t.Run("draft autosave", func(t *testing.T) {
		f := completedWithRound(t, "reopen-ver-draft")
		otherBefore := f.version(t, f.trs[1])

		rec := f.put(t, fmt.Sprintf("/api/files/%d/translations/%d/draft", f.fileID, f.trs[0].ID), map[string]any{
			"human_text": "Hallo neu", "editing_time_ms": 0, "expected_updated_at": f.version(t, f.trs[0]),
		})

		testutil.AssertStatus(t, rec, http.StatusOK)
		if !taskReopened(t, rec) {
			t.Error("the edit should report that it reopened the task")
		}
		if got := f.activeText(t, f.trs[0]); got != "Hallo neu" {
			t.Errorf("text = %q, want the draft", got)
		}
		if got := f.version(t, f.trs[1]); got != otherBefore {
			t.Errorf("an untouched segment's updated_at moved on reopen (%s → %s): another open editor would get a false conflict", otherBefore, got)
		}
	})

	t.Run("confirm", func(t *testing.T) {
		f := completedWithRound(t, "reopen-ver-confirm")

		rec := f.put(t, fmt.Sprintf("/api/files/%d/translations/%d", f.fileID, f.trs[0].ID), map[string]any{
			"human_text": "Hallo bestätigt", "editing_time_ms": 0, "expected_updated_at": f.version(t, f.trs[0]),
		})

		testutil.AssertStatus(t, rec, http.StatusOK)
		if got := f.activeText(t, f.trs[0]); got != "Hallo bestätigt" {
			t.Errorf("text = %q, want the confirmed text", got)
		}
	})
}
