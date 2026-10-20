package project_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The refresh is scoped through job -> file -> project -> tenant, not by the
// step id alone, so another tenant's id pair can never re-date this tenant's
// work — neither through the query nor through the PUT that calls it.
//
// Anti-tautology: drop `p.tenant_id = @tenant_id` from
// RefreshJobStepAssignmentDeadlines and the query half flips to the step date.
func TestIntegrationWorkflowSteps_DeadlineRefreshIsTenantScoped(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "RefreshScopeA")
	tenantB := testutil.SeedTenant(t, q, "RefreshScopeB")
	adminB := testutil.SeedUser(t, q, tenantB.ID, "admin@refreshb.test", "pass")
	project := testutil.SeedProject(t, q, tenantA.ID, "RefreshScope", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	stepDL := time.Date(2027, 6, 1, 17, 0, 0, 0, time.UTC)
	jobDL := time.Date(2027, 3, 3, 10, 0, 0, 0, time.UTC)
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "translate", StepOrder: 0,
		RoleRequired: "linguist", Deadline: pgtype.Timestamptz{Time: stepDL, Valid: true},
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	if _, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: step.ID, Status: "in_progress",
		Deadline: pgtype.Timestamptz{Time: jobDL, Valid: true},
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	if err := q.RefreshJobStepAssignmentDeadlines(ctx, db.RefreshJobStepAssignmentDeadlinesParams{
		WorkflowStepID: step.ID, TenantID: tenantB.ID,
	}); err != nil {
		t.Fatalf("refresh as tenant B: %v", err)
	}
	if got := deadlineOf(t, q, job.ID); got == nil || !got.Equal(jobDL) {
		t.Errorf("after tenant B's refresh, job deadline = %v, want it untouched at %v", got, jobDL)
	}

	moved := time.Date(2027, 9, 9, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID),
		stepsBody(t, q, tenantB.ID, moved, nil), adminB.ID, tenantB.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	if got := deadlineOf(t, q, job.ID); got == nil || !got.Equal(jobDL) {
		t.Errorf("after tenant B's PUT, job deadline = %v, want it untouched at %v", got, jobDL)
	}

	// The owning tenant's refresh does reach the job, so the untouched
	// assertions above are not a query that never matches anything.
	if err := q.RefreshJobStepAssignmentDeadlines(ctx, db.RefreshJobStepAssignmentDeadlinesParams{
		WorkflowStepID: step.ID, TenantID: tenantA.ID,
	}); err != nil {
		t.Fatalf("refresh as tenant A: %v", err)
	}
	if got := deadlineOf(t, q, job.ID); got == nil || !got.Equal(stepDL) {
		t.Errorf("after tenant A's refresh, job deadline = %v, want the step's %v", got, stepDL)
	}
}

// Every per-language rule resolveLanguageDeadlines enforces is a 400 at the
// PUT, and a refused save writes nothing: the stored override is exactly what
// it was before, neither replaced nor wiped.
func TestIntegrationWorkflowSteps_RejectsBadLanguageDeadlines(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "BadLangDLCo")
	admin := testutil.SeedUser(t, q, tenant.ID, "admin@badlangdl.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "BadLangDL", "en", []string{"de"})

	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "APE", Purpose: "qe_ape"})
	if err != nil {
		t.Fatal(err)
	}
	aiRouterID := testutil.SeedRouterForEngine(t, q, tenant.ID, engine.ID)
	aiDef := testutil.SeedStepDefinition(t, q, tenant.ID, "Auto APE", "ai", "", &aiRouterID)

	baseDL := time.Date(2027, 5, 20, 9, 0, 0, 0, time.UTC)
	dl := time.Date(2027, 6, 1, 17, 0, 0, 0, time.UTC).Format(time.RFC3339)
	put := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		return testutil.DoRequest(t, router, "PUT",
			fmt.Sprintf("/api/projects/%d/workflow-steps", project.ID), body, admin.ID, tenant.ID)
	}
	testutil.AssertStatus(t, put(stepsBody(t, q, tenant.ID, "", map[string]string{"de": baseDL.Format(time.RFC3339)})), http.StatusOK)

	lang := func(l, d string) map[string]any { return map[string]any{"target_language": l, "deadline": d} }
	// want is part of the error text, so a 400 for some other reason fails.
	cases := []struct {
		name string
		want string
		body map[string]any
	}{
		{"ai step", "cannot have a deadline", map[string]any{"steps": []map[string]any{{
			"step_definition_id": aiDef.ID,
			"language_deadlines": []map[string]any{lang("de", dl)},
		}}}},
		{"duplicate after normalize", "two deadlines", stepBodyWith(t, q, tenant.ID, map[string]any{
			"language_deadlines": []map[string]any{lang("DE", dl), lang("de", dl)},
		})},
		{"empty language", "needs a target language", stepBodyWith(t, q, tenant.ID, map[string]any{
			"language_deadlines": []map[string]any{lang("", dl)},
		})},
		{"bad date", "invalid deadline format", stepBodyWith(t, q, tenant.ID, map[string]any{
			"language_deadlines": []map[string]any{lang("de", "next tuesday")},
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := put(tc.body)
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("error = %s, want it to mention %q", rec.Body.String(), tc.want)
			}
			rows, err := q.ListWorkflowStepAssignmentsForProfile(ctx, project.SettingsProfileID)
			if err != nil {
				t.Fatalf("ListWorkflowStepAssignmentsForProfile: %v", err)
			}
			if len(rows) != 1 || rows[0].TargetLanguage != "de" || !rows[0].Deadline.Time.Equal(baseDL) {
				t.Errorf("stored overrides = %+v, want only the baseline de=%v", rows, baseDL)
			}
		})
	}
}
