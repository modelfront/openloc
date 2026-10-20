package project_test

// History of a project that starts as a wizard draft: project.created is
// written when the draft starts, names the person who started it, and is
// filled in with the final configuration at publish. A publish by someone
// else adds a project.published entry naming the publisher.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/projectevents"
	"openloc/internal/testutil"
)

func (f draftFixture) publishAs(t *testing.T, id, userID int64) {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", id),
		map[string]any{"analyze": false}, userID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// emitDuringDraft records an event between draft start and publish, the way a
// wizard upload does.
func (f draftFixture) emitDuringDraft(t *testing.T, id int64) {
	t.Helper()
	if err := projectevents.EmitInTx(context.Background(), f.q, projectevents.Event{
		ProjectID: id, TenantID: f.tenant.ID, Type: projectevents.TypeFileUploaded, ActorID: &f.user.ID,
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}
}

func eventTypes(page emissionPage) []string {
	out := make([]string, 0, len(page.Data))
	for _, e := range page.Data {
		out = append(out, e.EventType)
	}
	return out
}

func actorID(t *testing.T, page emissionPage, i int) int64 {
	t.Helper()
	if page.Data[i].Actor == nil {
		t.Fatalf("event %d (%s) has no actor", i, page.Data[i].EventType)
	}
	return page.Data[i].Actor.ID
}

func (f draftFixture) fillAndSave(t *testing.T, id int64) {
	t.Helper()
	testutil.AssertStatus(t, f.saveDraft(t, id, map[string]any{
		"name": "Whole", "source_language": "en", "target_languages": []string{"fr"},
		"settings": map[string]any{"mt_on_upload": true},
	}), http.StatusOK)
}

func TestIntegrationDraftCreatedEventComesFirstAndNamesStarter(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftEvStarter")
	defer cleanup()
	publisher := testutil.SeedUserWithRole(t, f.q, f.tenant.ID, "ben@DraftEvStarter.test", "pass", "project_manager")

	id := f.createDraft(t, map[string]any{"name": "Half"})
	f.emitDuringDraft(t, id)
	f.fillAndSave(t, id)
	f.publishAs(t, id, publisher.ID)

	// Newest first.
	page := listEvents(t, f.router, id, f.user.ID, f.tenant.ID)
	got := eventTypes(page)
	want := []string{projectevents.TypeProjectPublished, projectevents.TypeFileUploaded, projectevents.TypeProjectCreated}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if a := actorID(t, page, 0); a != publisher.ID {
		t.Errorf("published actor = %d, want the publisher %d", a, publisher.ID)
	}
	if a := actorID(t, page, 2); a != f.user.ID {
		t.Errorf("created actor = %d, want the starter %d", a, f.user.ID)
	}
	if p := f.project(t, id); p.CreatedBy == nil || *p.CreatedBy != f.user.ID {
		t.Errorf("projects.created_by = %v, want the starter %d", p.CreatedBy, f.user.ID)
	}

	created := page.Data[2].Details
	if created["name"] != "Whole" {
		t.Errorf("created name = %v, want the published name", created["name"])
	}
	if langs, _ := created["target_languages"].([]any); len(langs) != 1 || langs[0] != "fr" {
		t.Errorf("created target_languages = %v, want [fr]", created["target_languages"])
	}
	if s, _ := created["settings"].(map[string]any); s["mt_on_upload"] != true {
		t.Errorf("created settings = %v, want the publish-time snapshot", created["settings"])
	}
}

// Publish replaces the draft-start details rather than merging into them, so a
// template dropped in the form does not linger; the actor_name snapshot taken
// at draft start stays the starter's.
func TestIntegrationDraftCreatedEventReplacesDetailsKeepsStarterSnapshot(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftEvReplace")
	defer cleanup()
	publisher := testutil.SeedUserWithRole(t, f.q, f.tenant.ID, "ben@DraftEvReplace.test", "pass", "project_manager")
	tmpl := testutil.SeedTemplate(t, f.q, f.tenant.ID, "Dropped Template")

	id := f.createDraft(t, map[string]any{"name": "Half", "template_id": tmpl.ID})
	before := listEvents(t, f.router, id, f.user.ID, f.tenant.ID)
	if len(before.Data) != 1 || before.Data[0].Details["template_name"] != "Dropped Template" {
		t.Fatalf("draft-start event = %+v, want project.created carrying the template", before.Data)
	}
	f.fillAndSave(t, id) // the form no longer names a template
	f.publishAs(t, id, publisher.ID)

	page := listEvents(t, f.router, id, f.user.ID, f.tenant.ID)
	var created map[string]any
	for _, e := range page.Data {
		if e.EventType == projectevents.TypeProjectCreated {
			created = e.Details
		}
	}
	if created == nil {
		t.Fatalf("no project.created in %v", eventTypes(page))
	}
	for _, k := range []string{"template_id", "template_name"} {
		if _, ok := created[k]; ok {
			t.Errorf("created details still carry %q = %v after the form dropped the template", k, created[k])
		}
	}
	if created["actor_name"] != f.user.Email {
		t.Errorf("created actor_name = %v, want the starter's %q", created["actor_name"], f.user.Email)
	}
}

func TestIntegrationDraftPublishedBySameUserHasNoPublishedEvent(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftEvSame")
	defer cleanup()

	id := f.createDraft(t, map[string]any{"name": "Half"})
	f.emitDuringDraft(t, id)
	f.fillAndSave(t, id)
	f.publishAs(t, id, f.user.ID)

	page := listEvents(t, f.router, id, f.user.ID, f.tenant.ID)
	want := []string{projectevents.TypeFileUploaded, projectevents.TypeProjectCreated}
	if got := eventTypes(page); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if page.Data[1].Details["name"] != "Whole" {
		t.Errorf("created name = %v, want the published name", page.Data[1].Details["name"])
	}
}

// A draft with no project.created has nothing to fill in: publish writes one,
// still naming the starter.
func TestIntegrationDraftWithoutCreatedEventGetsOneAtPublish(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftEvLegacy")
	defer cleanup()
	publisher := testutil.SeedUserWithRole(t, f.q, f.tenant.ID, "ben@DraftEvLegacy.test", "pass", "project_manager")

	id := f.createDraft(t, map[string]any{"name": "Half"})
	if _, err := f.pool.Exec(context.Background(),
		`DELETE FROM project_events WHERE project_id = $1 AND event_type = 'project.created'`, id); err != nil {
		t.Fatalf("delete created event: %v", err)
	}
	f.fillAndSave(t, id)
	f.publishAs(t, id, publisher.ID)

	page := listEvents(t, f.router, id, f.user.ID, f.tenant.ID)
	want := []string{projectevents.TypeProjectPublished, projectevents.TypeProjectCreated}
	if got := eventTypes(page); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if a := actorID(t, page, 1); a != f.user.ID {
		t.Errorf("created actor = %d, want the starter %d", a, f.user.ID)
	}
	if page.Data[1].Details["name"] != "Whole" {
		t.Errorf("created name = %v, want the published name", page.Data[1].Details["name"])
	}
}

// assertOnlyCreated checks that a draft's history holds just the
// project.created written when the draft started: draft saves, refused
// publishes and refused PATCHes record nothing.
func assertOnlyCreated(t *testing.T, f draftFixture, id int64, when string) {
	t.Helper()
	page := listEvents(t, f.router, id, f.user.ID, f.tenant.ID)
	if got := eventTypes(page); fmt.Sprint(got) != fmt.Sprint([]string{projectevents.TypeProjectCreated}) {
		t.Errorf("%s: events = %v, want only project.created", when, got)
	}
}
