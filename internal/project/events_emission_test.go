package project_test

// Emission tests for the project-package project_events sites.
// Anti-tautology: each test asserts on rows the HTTP
// flow must have inserted into project_events — removing the corresponding
// projectevents.Emit call makes the test fail with "expected event, got 0".

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

type emissionPage struct {
	Data []struct {
		ID        int64  `json:"id"`
		EventType string `json:"event_type"`
		Actor     *struct {
			ID int64 `json:"id"`
		} `json:"actor"`
		Details map[string]any `json:"details"`
	} `json:"data"`
}

// listEvents fetches the project's event feed newest-first.
func listEvents(t *testing.T, router http.Handler, projectID, userID, tenantID int64) emissionPage {
	t.Helper()
	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/events", projectID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var page emissionPage
	testutil.DecodeJSON(t, rec, &page)
	return page
}

// eventsOfType filters the feed by event_type.
func eventsOfType(page emissionPage, typ string) []map[string]any {
	var out []map[string]any
	for _, e := range page.Data {
		if e.EventType == typ {
			out = append(out, e.Details)
		}
	}
	return out
}

// changeOf digs details.changes[field] out of an event's details map.
func changeOf(t *testing.T, details map[string]any, field string) map[string]any {
	t.Helper()
	changes, ok := details["changes"].(map[string]any)
	if !ok {
		t.Fatalf("expected details.changes map, got %v", details["changes"])
	}
	ch, ok := changes[field].(map[string]any)
	if !ok {
		t.Fatalf("expected changes.%s, got keys %v", field, changes)
	}
	return ch
}

// TestIntegrationProjectCreateAtomicEvent pins that a wizard create records one
// project.created event: settings + assignments + workflow steps travel IN the
// POST and fold into it. RED without CreateWithSetup (a POST plus follow-up
// PATCH/PUTs records 5+ events on a fresh project).
func TestIntegrationProjectCreateAtomicEvent(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AtomicCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@atomic.test", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Atomic TM")

	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Atomic Create",
		"source_language":  "en",
		"target_languages": []string{"de"},
		"settings": map[string]any{
			"fuzzy_threshold": 0.9,
			"mt_on_upload":    true,
		},
		"tm_assignments": []map[string]any{
			{"tm_id": tm.ID, "mode": "read_write", "priority": 1},
		},
		"workflow_steps": []map[string]any{
			testutil.LibraryStep(t, q, tenant.ID, "translation", map[string]any{"role_required": "linguist"}),
			testutil.LibraryStep(t, q, tenant.ID, "review", map[string]any{"role_required": "reviewer"}),
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var createResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &createResp)
	projectID := createResp.Data.ID

	page := listEvents(t, router, projectID, user.ID, tenant.ID)
	if len(page.Data) != 1 {
		types := make([]string, 0, len(page.Data))
		for _, e := range page.Data {
			types = append(types, e.EventType)
		}
		t.Fatalf("expected exactly 1 event on a configured create, got %d: %v", len(page.Data), types)
	}
	ev := page.Data[0]
	if ev.EventType != "project.created" {
		t.Fatalf("expected project.created, got %q", ev.EventType)
	}
	// The single event carries the full initial configuration.
	settingsDetails, ok := ev.Details["settings"].(map[string]any)
	if !ok {
		t.Fatalf("expected details.settings, got %v", ev.Details)
	}
	if settingsDetails["mt_on_upload"] != true {
		t.Errorf("expected settings.mt_on_upload=true in created event, got %v", settingsDetails["mt_on_upload"])
	}
	tms, _ := ev.Details["tm_assignments"].([]any)
	if len(tms) != 1 {
		t.Errorf("expected 1 tm_assignment in created event, got %v", ev.Details["tm_assignments"])
	}
	steps, _ := ev.Details["workflow_steps"].([]any)
	if len(steps) != 2 {
		t.Errorf("expected 2 workflow_steps in created event, got %v", ev.Details["workflow_steps"])
	}

	// The settings/assignments actually applied, not just got recorded.
	settingsRec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/settings", projectID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, settingsRec, http.StatusOK)
	var settingsResp struct {
		Data struct {
			Project struct {
				MtOnUpload     bool    `json:"mt_on_upload"`
				FuzzyThreshold float32 `json:"fuzzy_threshold"`
			} `json:"project"`
			TMAssignments []map[string]any `json:"tm_assignments"`
			WorkflowSteps []map[string]any `json:"workflow_steps"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, settingsRec, &settingsResp)
	if !settingsResp.Data.Project.MtOnUpload || settingsResp.Data.Project.FuzzyThreshold != 0.9 {
		t.Errorf("settings not applied: %+v", settingsResp.Data.Project)
	}
	if len(settingsResp.Data.TMAssignments) != 1 {
		t.Errorf("expected 1 TM assignment applied, got %d", len(settingsResp.Data.TMAssignments))
	}
	if len(settingsResp.Data.WorkflowSteps) != 2 {
		t.Errorf("expected 2 workflow steps applied, got %d", len(settingsResp.Data.WorkflowSteps))
	}
}

func TestIntegrationProjectEventEmission(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "EmitCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@emit.test", "pass")

	// --- project.created on POST /api/projects ---
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Audit Me",
		"source_language":  "en",
		"target_languages": []string{"de", "fr"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var createResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &createResp)
	projectID := createResp.Data.ID

	page := listEvents(t, router, projectID, user.ID, tenant.ID)
	created := eventsOfType(page, "project.created")
	if len(created) != 1 {
		t.Fatalf("expected 1 project.created event, got %d", len(created))
	}
	if created[0]["name"] != "Audit Me" {
		t.Errorf("expected created event to snapshot the name, got %v", created[0]["name"])
	}

	// --- project.metadata_updated with old→new diff ---
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", projectID),
		map[string]any{"name": "Renamed", "status": "on_hold"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	page = listEvents(t, router, projectID, user.ID, tenant.ID)
	meta := eventsOfType(page, "project.metadata_updated")
	if len(meta) != 1 {
		t.Fatalf("expected 1 metadata_updated event, got %d", len(meta))
	}
	nameCh := changeOf(t, meta[0], "name")
	if nameCh["from"] != "Audit Me" || nameCh["to"] != "Renamed" {
		t.Errorf("expected name change Audit Me→Renamed, got %v", nameCh)
	}
	statusCh := changeOf(t, meta[0], "status")
	if statusCh["from"] != "in_progress" || statusCh["to"] != "on_hold" {
		t.Errorf("expected status change in_progress→on_hold, got %v", statusCh)
	}
	if page.Data[0].Actor == nil || page.Data[0].Actor.ID != user.ID {
		t.Errorf("expected metadata event attributed to user %d", user.ID)
	}

	// --- no-op PATCH emits nothing ---
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", projectID),
		map[string]any{"name": "Renamed"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	page = listEvents(t, router, projectID, user.ID, tenant.ID)
	if got := len(eventsOfType(page, "project.metadata_updated")); got != 1 {
		t.Errorf("no-op PATCH must not emit: expected still 1 metadata event, got %d", got)
	}

	// --- project.settings_updated with diff ---
	// Read the current value and flip it, so the assertion doesn't depend on
	// the creation default (fee098f flipped mt_on_upload's default to true —
	// a hardcoded false→true expectation broke on that rebase).
	var settingsResp struct {
		Data struct {
			Project struct {
				MtOnUpload bool `json:"mt_on_upload"`
			} `json:"project"`
		} `json:"data"`
	}
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", projectID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &settingsResp)
	mtBefore := settingsResp.Data.Project.MtOnUpload

	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", projectID),
		map[string]any{"fuzzy_threshold": 0.9, "mt_on_upload": !mtBefore}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	page = listEvents(t, router, projectID, user.ID, tenant.ID)
	settings := eventsOfType(page, "project.settings_updated")
	if len(settings) != 1 {
		t.Fatalf("expected 1 settings_updated event, got %d", len(settings))
	}
	mtCh := changeOf(t, settings[0], "mt_on_upload")
	if mtCh["from"] != mtBefore || mtCh["to"] != !mtBefore {
		t.Errorf("expected mt_on_upload %v→%v, got %v", mtBefore, !mtBefore, mtCh)
	}

	// --- no-op settings save emits nothing ---
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", projectID),
		map[string]any{"mt_on_upload": !mtBefore}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	page = listEvents(t, router, projectID, user.ID, tenant.ID)
	if got := len(eventsOfType(page, "project.settings_updated")); got != 1 {
		t.Errorf("no-op settings save must not emit: expected still 1 event, got %d", got)
	}

	// --- workflow_steps_set with before/after ---
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", projectID),
		map[string]any{"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenant.ID, "translation", map[string]any{"role_required": "linguist"}),
			testutil.LibraryStep(t, q, tenant.ID, "review", map[string]any{"role_required": "reviewer"}),
			testutil.LibraryStep(t, q, tenant.ID, "proofread"),
		}}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	page = listEvents(t, router, projectID, user.ID, tenant.ID)
	wf := eventsOfType(page, "project.workflow_steps_set")
	if len(wf) != 1 {
		t.Fatalf("expected 1 workflow_steps_set event, got %d", len(wf))
	}
	before, _ := wf[0]["before"].([]any)
	after, _ := wf[0]["after"].([]any)
	// A new project carries the default Translation → Review pair.
	if len(before) != 2 || len(after) != 3 {
		t.Errorf("expected before=2 steps, after=3 steps, got %d/%d", len(before), len(after))
	}
}
