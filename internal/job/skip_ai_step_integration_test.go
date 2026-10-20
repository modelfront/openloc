package job_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

const aiStepFailure = "provider 404: deployment not found"

func (f rerunFixture) skipAIStep(t *testing.T, userID, tenantID int64, role string) int {
	t.Helper()
	router := testutil.NewTestRouter(f.pool)
	rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/skip-ai-step", f.jobID), nil, userID, tenantID, role)
	return rec.Code
}

// saveDraft writes the fixture's segment as the assigned linguist, the person
// who works the hybrid step after the AI.
func (f rerunFixture) saveDraft(t *testing.T, text string) int {
	t.Helper()
	router := testutil.NewTestRouter(f.pool)
	rec := testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", f.fileID, f.translationID),
		map[string]any{"human_text": text}, f.linguist, f.tenantID, "linguist")
	return rec.Code
}

// A failed AI run on a hybrid step locks the task the person works on. A
// manager skipping the AI step clears the error and leaves the task in
// progress, so the person can edit and confirm without the AI pass.
func TestIntegrationSkipAIStep_UnlocksHybridStep(t *testing.T) {
	f := newRerunFixture(t, "skip-ai", "hybrid")
	ctx := context.Background()
	f.fail(t, "AI step (QE+APE): 3 / 9", aiStepFailure)

	if code := f.saveDraft(t, "Hallo"); code != http.StatusConflict {
		t.Fatalf("edit while the AI step is errored: status %d, want 409", code)
	}
	if code := f.skipAIStep(t, f.linguist, f.tenantID, "linguist"); code != http.StatusForbidden {
		t.Fatalf("linguist skipping the AI step: status %d, want 403", code)
	}
	if code := f.skipAIStep(t, f.ownerID, f.tenantID, "owner"); code != http.StatusNoContent {
		t.Fatalf("manager skip: status %d, want 204", code)
	}

	job, err := f.q.GetJob(ctx, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status == "failed" || job.ErrorMessage != nil || pipeline.IsPipelineRunning(job.Status, job.CurrentStep) {
		t.Errorf("after skip: status=%q error=%v step=%q; want not failed, error cleared, not running", job.Status, job.ErrorMessage, job.CurrentStep)
	}
	active, err := f.q.GetActiveStepForJob(ctx, f.jobID)
	if err != nil {
		t.Fatalf("the hybrid task must stay in progress: %v", err)
	}
	if active.StepType != "hybrid" {
		t.Errorf("active step = %q, want the hybrid step", active.StepType)
	}

	if code := f.saveDraft(t, "Hallo"); code != http.StatusOK {
		t.Errorf("edit after skip: status %d, want 200", code)
	}

	events := testutil.ProjectEvents(t, f.q, f.projectID, f.tenantID, "job.ai_step_skipped")
	if len(events) != 1 {
		t.Fatalf("job.ai_step_skipped events = %d, want 1", len(events))
	}
	if details := events[0].Data; details["error_dismissed"] != aiStepFailure {
		t.Errorf("skip details = %v", details)
	}

	// Nothing is failed any more: a second click has nothing to skip.
	if code := f.skipAIStep(t, f.ownerID, f.tenantID, "owner"); code != http.StatusConflict {
		t.Errorf("second skip: status %d, want 409", code)
	}
}

func TestIntegrationSkipAIStep_Refusals(t *testing.T) {
	t.Run("not failed", func(t *testing.T) {
		f := newRerunFixture(t, "skip-ok", "hybrid")
		if code := f.skipAIStep(t, f.ownerID, f.tenantID, "owner"); code != http.StatusConflict {
			t.Errorf("status %d, want 409", code)
		}
	})

	t.Run("pre-translation failure", func(t *testing.T) {
		f := newRerunFixture(t, "skip-pre", "hybrid")
		f.fail(t, "MT: 5 / 10 segments", "MT provider 500")
		if code := f.skipAIStep(t, f.ownerID, f.tenantID, "owner"); code != http.StatusConflict {
			t.Errorf("status %d, want 409", code)
		}
		assertStillFailed(t, f)
	})

	// On an `ai` step nobody works after the AI: Complete step is the way on.
	t.Run("AI-only step", func(t *testing.T) {
		f := newRerunFixture(t, "skip-aionly", "ai")
		f.fail(t, "AI step (QE+APE): 3 / 9", aiStepFailure)
		if code := f.skipAIStep(t, f.ownerID, f.tenantID, "owner"); code != http.StatusConflict {
			t.Errorf("status %d, want 409", code)
		}
		assertStillFailed(t, f)
	})

	t.Run("another tenant", func(t *testing.T) {
		f := newRerunFixture(t, "skip-tenant", "hybrid")
		f.fail(t, "AI step (QE+APE): 3 / 9", aiStepFailure)
		otherID, otherTenant := testutil.RegisterUser(t, f.q, "skip-other")
		if code := f.skipAIStep(t, otherID, otherTenant, "owner"); code != http.StatusNotFound {
			t.Errorf("status %d, want 404", code)
		}
		assertStillFailed(t, f)
	})
}

func assertStillFailed(t *testing.T, f rerunFixture) {
	t.Helper()
	job, err := f.q.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "failed" || job.ErrorMessage == nil {
		t.Errorf("a refused skip must leave the task errored; status=%q error=%v", job.Status, job.ErrorMessage)
	}
}
