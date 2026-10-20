package file_test

// Emission tests for the file-package project_events sites.
// Anti-tautology: removing the EmitInTx call in
// HandleUpload (or the Emit calls in rename/delete) fails these on the
// "expected N events" assertions.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type fileEventsPage struct {
	Data []struct {
		EventType string         `json:"event_type"`
		FileID    *int64         `json:"file_id"`
		Details   map[string]any `json:"details"`
	} `json:"data"`
}

func TestIntegrationFileEventEmission(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FileEmitCorp")
	// Rename/delete are owner/admin-gated; SeedUser's default role covers
	// upload, the role-specific helpers cover the rest.
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "admin@fileemit.test", "pass", "admin")
	project := testutil.SeedProject(t, q, tenant.ID, "FileAudit", "en", []string{"fr", "de"})

	fileContent := `{"greeting": "Hello world", "farewell": "Goodbye"}`
	rec := testutil.DoFileUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"audit.json", "application/json", fileContent, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var upResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &upResp)
	fileID := upResp.Data.ID

	fetch := func() fileEventsPage {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/projects/%d/events", project.ID), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var page fileEventsPage
		testutil.DecodeJSON(t, rec, &page)
		return page
	}
	ofType := func(page fileEventsPage, typ string) []map[string]any {
		var out []map[string]any
		for _, e := range page.Data {
			if e.EventType == typ {
				out = append(out, e.Details)
			}
		}
		return out
	}

	// --- file.uploaded, committed with the upload tx ---
	page := fetch()
	uploaded := ofType(page, "file.uploaded")
	if len(uploaded) != 1 {
		t.Fatalf("expected 1 file.uploaded event, got %d", len(uploaded))
	}
	if uploaded[0]["file_name"] != "audit.json" {
		t.Errorf("expected file_name snapshot, got %v", uploaded[0]["file_name"])
	}
	if jobs, ok := uploaded[0]["jobs"].([]any); !ok || len(jobs) != 2 {
		t.Errorf("expected 2 jobs in details (fr+de), got %v", uploaded[0]["jobs"])
	}

	// --- file.renamed with old→new diff ---
	rec = testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/projects/%d/files/%d", project.ID, fileID),
		map[string]any{"name": "renamed.json"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	page = fetch()
	renamed := ofType(page, "file.renamed")
	if len(renamed) != 1 {
		t.Fatalf("expected 1 file.renamed event, got %d", len(renamed))
	}
	// Pin the wire shape of the diff: the FE renderer reads changes.name.from/
	// .to (lowercase json tags on projectevents.Change) — a tag regression
	// would pass a count-only assertion but break the diff column.
	changes, _ := renamed[0]["changes"].(map[string]any)
	nameCh, _ := changes["name"].(map[string]any)
	if nameCh["from"] != "audit.json" || nameCh["to"] != "renamed.json" {
		t.Errorf("expected changes.name audit.json→renamed.json, got %v", renamed[0]["changes"])
	}

	// --- file.deleted survives with name + id in details ---
	rec = testutil.DoRequest(t, router, "DELETE",
		fmt.Sprintf("/api/projects/%d/files/%d", project.ID, fileID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	page = fetch()
	deleted := ofType(page, "file.deleted")
	if len(deleted) != 1 {
		t.Fatalf("expected 1 file.deleted event, got %d", len(deleted))
	}
	if deleted[0]["file_name"] != "renamed.json" {
		t.Errorf("expected deleted event to keep the file name, got %v", deleted[0]["file_name"])
	}
	// The upload event must still render after the file row is gone (no FK) —
	// same page fetch, taken after the DELETE.
	if got := len(ofType(page, "file.uploaded")); got != 1 {
		t.Errorf("file.uploaded event must survive file deletion, got %d", got)
	}
}

// Upload-time default assignments (assignee + deadline from the workflow
// blueprint) must land in History as workflow.step_assigned events —
// one per job per step that got an assignee or a deadline, so "Translation
// step (de) assigned to John — deadline X" is answerable from the History tab.
// RED without the createdAssignments emission loop in HandleUpload.
func TestIntegrationUploadEmitsStepAssignments(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "UploadAssignCorp")
	uploader := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@upload-assign.test", "pass", "admin")
	linguist := testutil.SeedUser(t, q, tenant.ID, "john@upload-assign.test", "pass")
	testutil.SetUserName(t, q, linguist.ID, "John Reviewer")
	project := testutil.SeedProject(t, q, tenant.ID, "UploadAssign", "en", []string{"fr", "de"})

	// First step: no default assignee, no deadline. The upload assigns it to
	// the uploader as a FALLBACK — that bookkeeping must NOT produce an event
	// (every bare project has a default first step; logging "assigned to
	// yourself" per upload would be noise, and it broke the project-history
	// E2E's row count when it did).
	if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID,
		Name:              "Translation",
		StepOrder:         0,
		RoleRequired:      "linguist",
	}); err != nil {
		t.Fatalf("create step 1: %v", err)
	}
	// Second step: explicit default assignee + blueprint deadline → one
	// audit event per job.
	deadline := time.Date(2026, 8, 30, 17, 0, 0, 0, time.UTC)
	if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID,
		Name:              "Review",
		StepOrder:         1,
		RoleRequired:      "pm",
		DefaultAssignee:   &linguist.ID,
		Deadline:          pgtype.Timestamptz{Time: deadline, Valid: true},
	}); err != nil {
		t.Fatalf("create step 2: %v", err)
	}

	rec := testutil.DoFileUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"assign.json", "application/json", `{"greeting": "Hello"}`, uploader.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	rec = testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/events", project.ID), nil, uploader.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var page fileEventsPage
	testutil.DecodeJSON(t, rec, &page)

	var assigned []map[string]any
	for _, e := range page.Data {
		if e.EventType == "workflow.step_assigned" {
			assigned = append(assigned, e.Details)
		}
	}
	// One per target language for the explicitly-configured "Review" step;
	// the fallback-assigned "Translation" step emits none.
	if len(assigned) != 2 {
		t.Fatalf("expected exactly 2 workflow.step_assigned events (fr + de), got %d", len(assigned))
	}
	langs := map[any]bool{}
	for _, d := range assigned {
		langs[d["target_language"]] = true
		if d["step"] != "Review" {
			t.Errorf("expected step Review (Translation is fallback-assigned → no event), got %v", d["step"])
		}
		if d["assignee_name"] != "John Reviewer" {
			t.Errorf("expected the default assignee's name, got %v", d["assignee_name"])
		}
		if d["deadline"] != "2026-08-30T17:00:00Z" {
			t.Errorf("expected the blueprint deadline in RFC3339, got %v", d["deadline"])
		}
		if d["auto"] != true {
			t.Errorf("upload defaults must be marked auto, got %v", d["auto"])
		}
		if d["file_name"] != "assign.json" {
			t.Errorf("expected file snapshot, got %v", d["file_name"])
		}
	}
	if !langs["fr"] || !langs["de"] {
		t.Errorf("expected one assignment event per target language, got %v", langs)
	}
}
