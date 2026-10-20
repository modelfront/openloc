package job_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// A Raw MT job whose language the router did not cover reads "No MT" with its
// Pre-translation step open. Apply MT while the router still lacks the language
// is refused and changes nothing; once the router covers it, Apply MT
// translates, clears the reason and completes the step — and with it the job.
func TestIntegrationApplyMT_ClearsNoMTAndCompletesPretranslation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "ApplyNoMTCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@applynomt.test", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, "Apply No MT", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "a.json", db.FileFormatJsonI18n)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	job := testutil.SeedJob(t, q, file.ID, "de")

	engine := testutil.SeedMTEngine(t, q, tenant.ID, "Mock", "mock")
	mtRouter := testutil.SeedMTRouter(t, q, tenant.ID, "es only", nil, nil)
	testutil.SeedMTRouterRule(t, q, mtRouter, "es", &engine.ID, nil)
	testutil.SetProfileMTRouter(t, q, project.SettingsProfileID, &mtRouter)

	// Raw MT: a single Pre-translation step, left open by the upload's run.
	ws, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "Pre-translation", StepOrder: 0,
		RoleRequired: "linguist", StepType: "pretranslate",
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: job.ID, WorkflowStepID: ws.ID, Status: "in_progress"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &a.ID}); err != nil {
		t.Fatal(err)
	}
	reason := "no_route"
	if err := q.SetJobMTSkipReason(ctx, db.SetJobMTSkipReasonParams{ID: job.ID, Reason: &reason}); err != nil {
		t.Fatal(err)
	}

	applyMT := func() (int, string) {
		rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/mt-apply", job.ID), nil, owner.ID, tenant.ID, "owner")
		return rec.Code, rec.Body.String()
	}
	stepStatus := func() string {
		rows, err := q.ListJobStepAssignments(ctx, job.ID)
		if err != nil || len(rows) != 1 {
			t.Fatalf("ListJobStepAssignments: %v (%d rows)", err, len(rows))
		}
		return rows[0].Status
	}

	code, body := applyMT()
	if code != http.StatusBadRequest {
		t.Fatalf("Apply MT with no route for de: status %d, want 400 (body %s)", code, body)
	}
	if !strings.Contains(strings.ToLower(body), "router") {
		t.Errorf("refusal should name the router to fix, got %s", body)
	}
	got, err := q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MtSkipReason == nil || *got.MtSkipReason != reason {
		t.Errorf("refused Apply MT changed mt_skip_reason to %v", got.MtSkipReason)
	}
	if s := stepStatus(); s != "in_progress" {
		t.Errorf("refused Apply MT: step = %q, want in_progress", s)
	}

	testutil.SeedMTRouterRule(t, q, mtRouter, "de", &engine.ID, nil)
	if code, body := applyMT(); code != http.StatusOK {
		t.Fatalf("Apply MT after adding the route: status %d (body %s)", code, body)
	}
	got, err = q.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MtSkipReason != nil {
		t.Errorf("mt_skip_reason after a successful Apply MT = %q, want NULL", *got.MtSkipReason)
	}
	if s := stepStatus(); s != "completed" {
		t.Errorf("Pre-translation step after Apply MT = %q, want completed", s)
	}
	if got.Status != "completed" {
		t.Errorf("job status = %q, want completed (Pre-translation was the last step)", got.Status)
	}
	trGot, err := q.GetTranslation(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if trGot.ActiveText == nil || *trGot.ActiveText != "[DE] Hello" {
		t.Errorf("translation = %v, want the mock MT text", trGot.ActiveText)
	}
}
