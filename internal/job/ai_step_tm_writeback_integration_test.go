package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Completing a QE+APE workflow step writes nothing to TM, even with a
// human-approved segment and a read_write TM: only job completion writes TM,
// so only the last step's confirmations reach it.
func TestIntegrationAIStep_StepComplete_NoTMWrite(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AIStepTMWriteCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@aisteptm.test", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, "AIStep TM Write Project", "en", []string{"de"})

	// A read_write TM so the write-back has a target.
	tm := testutil.SeedTM(t, q, tenant.ID, "Write-back TM")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tm.ID,
		Mode:              "read_write",
		Priority:          1,
	}); err != nil {
		t.Fatalf("seed TM assignment: %v", err)
	}

	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 0)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// One human-approved translation with active text. SeedTranslation
	// doesn't set text, so patch it directly.
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusApproved)
	if _, err := pool.Exec(ctx,
		`UPDATE translations SET active_text = $1, status = 'approved' WHERE id = $2`,
		"Hallo Welt", tr.ID,
	); err != nil {
		t.Fatalf("set approved active_text: %v", err)
	}

	// Active QE step (the only step → also the final step), assigned to owner.
	// The engine gets no token because there is none to give it; advancing the
	// step never runs the AI step, so its usability is not in play here.
	seedActiveAIStep(t, q, tenant.ID, project.SettingsProfileID, job.ID, &owner.ID, nil)

	// Complete (advance) the final QE step.
	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/workflow/advance", job.ID),
		nil, owner.ID, tenant.ID, "owner",
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var body struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode advance response: %v (raw: %s)", err, rec.Body.String())
	}
	if body.Data.Status != "workflow_completed" {
		t.Fatalf("want status=workflow_completed (final step), got %q (body: %s)", body.Data.Status, rec.Body.String())
	}

	var tmCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM translation_memory WHERE tm_id = $1`, tm.ID,
	).Scan(&tmCount); err != nil {
		t.Fatalf("count TM entries: %v", err)
	}
	if tmCount != 0 {
		t.Fatalf("completing the QE step must not write TM (that is job-Complete-only); got %d entries", tmCount)
	}
}

// A plain (non-AI) final step writes nothing to TM either.
func TestIntegrationAIStep_PlainStepComplete_NoTMWrite(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PlainStepNoTMCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@plainstep.test", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, "Plain Step Project", "en", []string{"de"})

	tm := testutil.SeedTM(t, q, tenant.ID, "Plain TM")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tm.ID,
		Mode:              "read_write",
		Priority:          1,
	}); err != nil {
		t.Fatalf("seed TM assignment: %v", err)
	}

	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 0)
	job := testutil.SeedJob(t, q, file.ID, "de")
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusApproved)
	if _, err := pool.Exec(ctx,
		`UPDATE translations SET active_text = $1, status = 'approved' WHERE id = $2`,
		"Hallo Welt", tr.ID,
	); err != nil {
		t.Fatalf("set approved active_text: %v", err)
	}

	// Plain human step (no ai_router_id), final step, assigned to owner.
	testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &owner.ID)

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/workflow/advance", job.ID),
		nil, owner.ID, tenant.ID, "owner",
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var tmCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM translation_memory WHERE tm_id = $1`, tm.ID,
	).Scan(&tmCount); err != nil {
		t.Fatalf("count TM entries: %v", err)
	}
	if tmCount != 0 {
		t.Fatalf("plain step completion must not write TM (that is job-Complete-only); got %d entries", tmCount)
	}
}
