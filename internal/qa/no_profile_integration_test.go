package qa_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegration_QA_ProjectWithoutProfile pins how a job behaves when its
// project has no QA profile: the report says so (the editor turns Run QA off
// from it), and a run answers 409 — an expected state, not a client bug — until
// a profile is assigned.
func TestIntegration_QA_ProjectWithoutProfile(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	user, tenant := testutil.RegisterUser(t, db.New(pool), "qanoprof")

	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "No QA Profile",
		"source_language":  "en",
		"target_languages": []string{"es"},
		"workflow_id":      testutil.LibraryWorkflow(t, q, tenant),
	}, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projectID := projResp.Data.ID
	file := testutil.SeedFile(t, q, projectID, "noprof.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")

	profileAssigned := func(t *testing.T) bool {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/qa-report", job.ID), nil, user, tenant)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				ProfileAssigned *bool `json:"profile_assigned"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if resp.Data.ProfileAssigned == nil {
			t.Fatalf("qa-report has no profile_assigned field: %s", rec.Body.String())
		}
		return *resp.Data.ProfileAssigned
	}

	if profileAssigned(t) {
		t.Fatal("profile_assigned = true for a project with no QA profile")
	}

	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID), nil, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "no QA profile") {
		t.Fatalf("run-qa body = %s, want the no-profile message", rec.Body.String())
	}

	rec = testutil.DoRequest(t, router, "POST", "/api/qa-profiles", map[string]string{"name": "Assigned later"}, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var profResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &profResp)
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", projectID),
		map[string]any{"qa_profile_id": profResp.Data.ID}, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusOK)

	if !profileAssigned(t) {
		t.Fatal("profile_assigned = false after a QA profile was assigned")
	}
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID), nil, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusOK)
}
