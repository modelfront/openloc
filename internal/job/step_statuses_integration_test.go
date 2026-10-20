package job_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/job"
	"openloc/internal/stepround"
	"openloc/internal/testutil"
)

// The editor's step-status reads: the job's previous step per segment and a
// segment's full step list. Tenant-scoped, and a linguist reads only jobs
// assigned to them.
func TestIntegrationStepStatuses_Endpoints(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "StepStatusCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@stepstatus.test", "pass", "owner")
	stranger := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@stepstatus.test", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "Step Status", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 0)
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	j := testutil.SeedJob(t, q, file.ID, "de")
	steps := testutil.SeedWorkflow(t, q, project.SettingsProfileID, j.ID,
		testutil.WorkflowStepSpec{Name: "Translation", Status: "in_progress", Assignee: &owner.ID},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &owner.ID},
	)
	if err := stepround.Open(ctx, q, j.ID, steps[0], stepround.Keep); err != nil {
		t.Fatal(err)
	}
	text := "Hallo"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: tr.ID, HumanText: &text, EditedBy: &owner.ID}); err != nil {
		t.Fatal(err)
	}
	rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", j.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	jobPath := fmt.Sprintf("/api/jobs/%d/step-statuses", j.ID)
	rec = testutil.DoRequestWithRole(t, router, "GET", jobPath, nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var jobResp struct {
		Data job.JobStepStatuses `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &jobResp)
	if len(jobResp.Data.Previous) != 1 {
		t.Fatalf("previous = %+v, want one segment", jobResp.Data.Previous)
	}
	p := jobResp.Data.Previous[0]
	if p.TranslationID != tr.ID || p.StepName != "Translation" || p.Status != "approved" || !p.Edited || p.EndReason != "completed" {
		t.Errorf("previous = %+v, want Translation: approved, edited", p)
	}

	trPath := fmt.Sprintf("/api/translations/%d/step-statuses", tr.ID)
	rec = testutil.DoRequestWithRole(t, router, "GET", trPath, nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var trResp struct {
		Data []stepround.Entry `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &trResp)
	if len(trResp.Data) != 1 || trResp.Data[0].StepName != "Translation" {
		t.Errorf("segment step statuses = %+v", trResp.Data)
	}

	// A linguist the job is not assigned to reads neither.
	for _, path := range []string{jobPath, trPath} {
		rec = testutil.DoRequestWithRole(t, router, "GET", path, nil, stranger.ID, tenant.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
	}

	// Another organization reads neither.
	otherUser, otherTenant := testutil.RegisterUser(t, q, "stepstatus-other")
	for _, path := range []string{jobPath, trPath} {
		rec = testutil.DoRequestWithRole(t, router, "GET", path, nil, otherUser, otherTenant, "owner")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	}
}
