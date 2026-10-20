package taskedit_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/projectevents"
	"openloc/internal/testutil"
)

// A re-import reopens a completed task only when it writes. One that changes
// approved segments without force saves nothing, so the task, the job and the
// project history stay as they were; the same upload with force reopens.
func TestIntegrationReopenByReimport_OnlyWhenItWrites(t *testing.T) {
	f := newFixture(t, "reopen-reimport", 1, "completed", "completed")
	ctx := context.Background()
	url := fmt.Sprintf("/api/files/%d/reimport-xliff?lang=de", f.fileID)
	before := f.activeText(t, f.trs[0])

	rec := testutil.DoFileUploadWithRole(t, f.router, url, "edited.xliff", reimportXLIFF, f.ownerID, f.tenantID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if taskReopened(t, rec) {
		t.Errorf("a re-import that needs force reported task_reopened; body=%s", rec.Body.String())
	}
	if got := f.activeText(t, f.trs[0]); got != before {
		t.Fatalf("a re-import that needs force wrote text: %q → %q", before, got)
	}
	if got := f.statuses(t); got[0] != "completed" || got[1] != "completed" {
		t.Errorf("statuses = %v, want both tasks still completed", got)
	}
	if n := len(f.events(t, projectevents.TypeStepReopened)); n != 0 {
		t.Errorf("%d %s events, want none for a re-import that saved nothing", n, projectevents.TypeStepReopened)
	}
	job, err := f.q.GetJob(ctx, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "completed" {
		t.Errorf("job status = %q, want completed", job.Status)
	}

	rec = testutil.DoFileUploadWithRole(t, f.router, url+"&force=true", "edited.xliff", reimportXLIFF, f.ownerID, f.tenantID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if !taskReopened(t, rec) {
		t.Errorf("a forced re-import must report task_reopened; body=%s", rec.Body.String())
	}
	if got := f.activeText(t, f.trs[0]); got != "Hallo reimport" {
		t.Errorf("forced re-import text = %q, want the imported target", got)
	}
	if got := f.statuses(t); got[0] != "completed" || got[1] != "in_progress" {
		t.Errorf("statuses = %v, want the last task (Review) reopened", got)
	}
	if n := len(f.events(t, projectevents.TypeStepReopened)); n != 1 {
		t.Errorf("%d %s events, want 1", n, projectevents.TypeStepReopened)
	}
}
