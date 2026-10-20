package projectevents_test

// Read-API tests for GET /api/projects/{projectID}/events.
// Anti-tautology: each test seeds project_events rows
// directly, so a handler that ignored the cursor, the ordering, or the tenant
// scope fails on the exact assertion that names that property.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/projectevents"
	"openloc/internal/testutil"
)

type eventsPage struct {
	Data []struct {
		ID        int64  `json:"id"`
		EventType string `json:"event_type"`
		Actor     *struct {
			ID    int64  `json:"id"`
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"actor"`
		Details map[string]any `json:"details"`
	} `json:"data"`
	Cursor  int64 `json:"cursor"`
	HasMore bool  `json:"has_more"`
}

func seedEvent(t *testing.T, q db.Querier, projectID, tenantID int64, eventType string, actorID *int64) int64 {
	t.Helper()
	id, err := q.InsertProjectEvent(context.Background(), db.InsertProjectEventParams{
		ProjectID: projectID,
		TenantID:  tenantID,
		EventType: eventType,
		ActorID:   actorID,
	})
	if err != nil {
		t.Fatalf("seed event %s: %v", eventType, err)
	}
	return id
}

func TestIntegrationProjectEventsList(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "EventsCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@events.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Audited", "en", []string{"de"})

	types := []string{"project.created", "project.metadata_updated", "file.uploaded"}
	for _, typ := range types {
		seedEvent(t, q, project.ID, tenant.ID, typ, &user.ID)
	}

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/events", project.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var page eventsPage
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 3 {
		t.Fatalf("expected 3 events, got %d", len(page.Data))
	}
	// Newest first: last insert comes back first.
	if page.Data[0].EventType != "file.uploaded" || page.Data[2].EventType != "project.created" {
		t.Errorf("expected newest-first order, got %q..%q", page.Data[0].EventType, page.Data[2].EventType)
	}
	if page.HasMore {
		t.Error("expected has_more=false for a fully returned page")
	}
	if page.Data[0].Actor == nil || page.Data[0].Actor.ID != user.ID {
		t.Errorf("expected actor join to resolve user %d, got %+v", user.ID, page.Data[0].Actor)
	}
}

func TestIntegrationProjectEventsCursorPagination(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PageCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@page.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Paged", "en", []string{"de"})

	var ids []int64
	for i := 0; i < 5; i++ {
		ids = append(ids, seedEvent(t, q, project.ID, tenant.ID, "project.metadata_updated", &user.ID))
	}

	// Page 1: limit=2 → the two newest, has_more=true, cursor = oldest id on page.
	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/events?limit=2", project.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var page1 eventsPage
	testutil.DecodeJSON(t, rec, &page1)
	if len(page1.Data) != 2 || !page1.HasMore {
		t.Fatalf("page1: expected 2 rows + has_more, got %d rows has_more=%v", len(page1.Data), page1.HasMore)
	}
	if page1.Data[0].ID != ids[4] || page1.Data[1].ID != ids[3] {
		t.Errorf("page1: expected ids [%d %d], got [%d %d]", ids[4], ids[3], page1.Data[0].ID, page1.Data[1].ID)
	}

	// Page 2 via cursor.
	rec = testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/events?limit=2&cursor=%d", project.ID, page1.Cursor), nil, user.ID, tenant.ID)
	var page2 eventsPage
	testutil.DecodeJSON(t, rec, &page2)
	if len(page2.Data) != 2 || !page2.HasMore {
		t.Fatalf("page2: expected 2 rows + has_more, got %d rows has_more=%v", len(page2.Data), page2.HasMore)
	}
	if page2.Data[0].ID != ids[2] || page2.Data[1].ID != ids[1] {
		t.Errorf("page2: expected ids [%d %d], got [%d %d]", ids[2], ids[1], page2.Data[0].ID, page2.Data[1].ID)
	}

	// Page 3: the final row, has_more flips false.
	rec = testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/events?limit=2&cursor=%d", project.ID, page2.Cursor), nil, user.ID, tenant.ID)
	var page3 eventsPage
	testutil.DecodeJSON(t, rec, &page3)
	if len(page3.Data) != 1 || page3.HasMore {
		t.Fatalf("page3: expected 1 row + has_more=false, got %d rows has_more=%v", len(page3.Data), page3.HasMore)
	}
	if page3.Data[0].ID != ids[0] {
		t.Errorf("page3: expected id %d, got %d", ids[0], page3.Data[0].ID)
	}
}

func TestIntegrationProjectEventsFilters(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FilterCorp")
	alice := testutil.SeedUser(t, q, tenant.ID, "alice@filter.test", "pass")
	bob := testutil.SeedUser(t, q, tenant.ID, "bob@filter.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Filtered", "en", []string{"de"})

	seedEvent(t, q, project.ID, tenant.ID, "project.created", &alice.ID)
	seedEvent(t, q, project.ID, tenant.ID, "file.uploaded", &alice.ID)
	seedEvent(t, q, project.ID, tenant.ID, "file.deleted", &bob.ID)
	seedEvent(t, q, project.ID, tenant.ID, "workflow.step_advanced", &bob.ID)

	get := func(qs string) eventsPage {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/projects/%d/events%s", project.ID, qs), nil, alice.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var page eventsPage
		testutil.DecodeJSON(t, rec, &page)
		return page
	}

	// Category prefix: "file." matches uploaded + deleted only.
	page := get("?type=file.")
	if len(page.Data) != 2 {
		t.Fatalf("type=file.: expected 2 events, got %d", len(page.Data))
	}
	for _, e := range page.Data {
		if e.EventType != "file.uploaded" && e.EventType != "file.deleted" {
			t.Errorf("type=file. returned %q", e.EventType)
		}
	}

	// Exact type via full prefix.
	page = get("?type=file.uploaded")
	if len(page.Data) != 1 || page.Data[0].EventType != "file.uploaded" {
		t.Fatalf("type=file.uploaded: expected exactly that event, got %+v", page.Data)
	}

	// Actor filter.
	page = get(fmt.Sprintf("?actor_id=%d", bob.ID))
	if len(page.Data) != 2 {
		t.Fatalf("actor_id=bob: expected 2 events, got %d", len(page.Data))
	}
	for _, e := range page.Data {
		if e.Actor == nil || e.Actor.ID != bob.ID {
			t.Errorf("actor filter leaked event %q by %+v", e.EventType, e.Actor)
		}
	}

	// Combined: bob + file. → just file.deleted.
	page = get(fmt.Sprintf("?actor_id=%d&type=file.", bob.ID))
	if len(page.Data) != 1 || page.Data[0].EventType != "file.deleted" {
		t.Fatalf("combined filter: expected [file.deleted], got %+v", page.Data)
	}

	// Malformed actor_id falls back to unfiltered, not 400.
	page = get("?actor_id=banana")
	if len(page.Data) != 4 {
		t.Fatalf("malformed actor_id: expected all 4 events, got %d", len(page.Data))
	}
}

func TestIntegrationProjectEventsTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	userA := testutil.SeedUser(t, q, tenantA.ID, "a@iso.test", "pass")
	projectA := testutil.SeedProject(t, q, tenantA.ID, "A-Project", "en", []string{"de"})
	seedEvent(t, q, projectA.ID, tenantA.ID, "project.created", &userA.ID)

	tenantB := testutil.SeedTenant(t, q, "TenantB")
	userB := testutil.SeedUser(t, q, tenantB.ID, "b@iso.test", "pass")

	// Cross-tenant read must 404 (not 200-empty, not 403 — no existence leak).
	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/events", projectA.ID), nil, userB.ID, tenantB.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

func TestIntegrationProjectEventsSystemActorAndSnapshot(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "SnapCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@snap.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Snapped", "en", []string{"de"})

	// System event (nil actor) with an emit-time actor_name snapshot in
	// details — the shape a deleted user leaves behind after SET NULL.
	if err := projectevents.EmitInTx(context.Background(), q, projectevents.Event{
		ProjectID: project.ID,
		TenantID:  tenant.ID,
		Type:      "project.settings_updated",
		ActorName: "ghost@snap.test",
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/events", project.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var page eventsPage
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 1 {
		t.Fatalf("expected 1 event, got %d", len(page.Data))
	}
	if page.Data[0].Actor != nil {
		t.Errorf("expected null actor for system event, got %+v", page.Data[0].Actor)
	}
	if got := page.Data[0].Details["actor_name"]; got != "ghost@snap.test" {
		t.Errorf("expected details.actor_name snapshot, got %v", got)
	}
}
