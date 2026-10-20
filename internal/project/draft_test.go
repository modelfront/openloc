package project_test

// Draft projects: the wizard's autosave creates a
// status='draft' row that may miss its name and languages, PUT /draft replays
// the form, POST /publish turns it into an in_progress project. Every test
// asserts on rows or responses — removing the status-aware branch in
// validateMetadata, the draft guard in UpdateProjectDraft, or the publish-time
// recordPublish makes the matching test fail.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type draftFixture struct {
	pool   *pgxpool.Pool
	router http.Handler
	q      db.Querier
	tenant db.Tenant
	user   db.User
}

func newDraftFixture(t *testing.T, name string) (draftFixture, func()) {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, name)
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@"+name+".test", "pass", "project_manager")
	return draftFixture{pool: pool, router: router, q: q, tenant: tenant, user: user}, cleanup
}

type projectResp struct {
	Data struct {
		ID              int64    `json:"id"`
		Name            string   `json:"name"`
		Status          string   `json:"status"`
		SourceLanguage  string   `json:"source_language"`
		TargetLanguages []string `json:"target_languages"`
		Domain          *string  `json:"domain"`
		TemplateID      *int64   `json:"template_id"`
	} `json:"data"`
}

// createDraft POSTs a draft with only the given fields and returns its id.
func (f draftFixture) createDraft(t *testing.T, body map[string]any) int64 {
	t.Helper()
	body["status"] = "draft"
	rec := testutil.DoRequestWithRole(t, f.router, "POST", "/api/projects", body, f.user.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp projectResp
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Status != "draft" {
		t.Fatalf("expected status=draft, got %q", resp.Data.Status)
	}
	return resp.Data.ID
}

func (f draftFixture) saveDraft(t *testing.T, id int64, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, "PUT", fmt.Sprintf("/api/projects/%d/draft", id), body, f.user.ID, f.tenant.ID, "project_manager")
}

func (f draftFixture) publish(t *testing.T, id int64) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", id), map[string]any{"analyze": false}, f.user.ID, f.tenant.ID, "project_manager")
}

func (f draftFixture) project(t *testing.T, id int64) db.Project {
	t.Helper()
	p, err := f.q.GetProject(context.Background(), db.GetProjectParams{ID: id, TenantID: f.tenant.ID})
	if err != nil {
		t.Fatalf("GetProject(%d): %v", id, err)
	}
	return p
}

// A draft may miss everything a live project needs; the same body without
// the draft status is refused. RED without the `draft` branch in
// validateMetadata.
func TestIntegrationDraftCreateAllowsMissingRequiredFields(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftCreate")
	defer cleanup()

	id := f.createDraft(t, map[string]any{"name": ""})
	p := f.project(t, id)
	if p.Name != "" || p.SourceLanguage != "" || len(p.TargetLanguages) != 0 {
		t.Errorf("draft row = name %q src %q targets %v, want all empty", p.Name, p.SourceLanguage, p.TargetLanguages)
	}
	if p.Status != db.ProjectStatusDraft {
		t.Errorf("status = %s, want draft", p.Status)
	}
	assertOnlyCreated(t, f, id, "fresh draft")

	rec := testutil.DoRequestWithRole(t, f.router, "POST", "/api/projects", map[string]any{"name": ""}, f.user.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

// A partial language list still has to be coherent.
func TestIntegrationDraftCreateStillChecksLanguages(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftLangs")
	defer cleanup()

	rec := testutil.DoRequestWithRole(t, f.router, "POST", "/api/projects", map[string]any{
		"status": "draft", "source_language": "en", "target_languages": []string{"en"},
	}, f.user.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

// PUT /draft replaces metadata and applies the settings half like create
// does; against a live project it is a 404 that changes nothing. RED without
// the `status = 'draft'` predicate on UpdateProjectDraft.
func TestIntegrationDraftSaveReplacesFormAndRefusesLiveProjects(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftSave")
	defer cleanup()
	tm := testutil.SeedTM(t, f.q, f.tenant.ID, "Draft TM")

	id := f.createDraft(t, map[string]any{"name": "First"})
	rec := f.saveDraft(t, id, map[string]any{
		"name": "  Second  ", "source_language": "EN", "target_languages": []string{"fr", "de"},
		"domain":         "legal",
		"settings":       map[string]any{"fuzzy_threshold": 0.9, "mt_on_upload": false},
		"tm_assignments": []map[string]any{{"tm_id": tm.ID, "mode": "read_write", "priority": 1}},
	})
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp projectResp
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Name != "Second" || resp.Data.SourceLanguage != "en" || len(resp.Data.TargetLanguages) != 2 {
		t.Errorf("saved draft = %+v, want name Second / en / 2 targets", resp.Data)
	}
	if resp.Data.Status != "draft" {
		t.Errorf("status after save = %q, want draft", resp.Data.Status)
	}
	if resp.Data.Domain == nil || *resp.Data.Domain != "legal" {
		t.Errorf("domain = %v, want legal", resp.Data.Domain)
	}
	p := f.project(t, id)
	profile, err := f.q.GetSettingsProfile(context.Background(), p.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.FuzzyThreshold != 0.9 || profile.MtOnUpload {
		t.Errorf("settings not applied: threshold %v mt_on_upload %v", profile.FuzzyThreshold, profile.MtOnUpload)
	}
	tms, err := f.q.ListSettingsTMAssignments(context.Background(), p.SettingsProfileID)
	if err != nil || len(tms) != 1 || tms[0].TmID != tm.ID {
		t.Errorf("tm assignments = %v (%v), want the one TM", tms, err)
	}
	// A draft save is not an audit-worthy change.
	assertOnlyCreated(t, f, id, "after a draft save")

	// A form that clears the languages is saved as cleared — full replace.
	rec = f.saveDraft(t, id, map[string]any{"name": "Second", "target_languages": []string{}})
	testutil.AssertStatus(t, rec, http.StatusOK)
	if p := f.project(t, id); len(p.TargetLanguages) != 0 || p.SourceLanguage != "" {
		t.Errorf("cleared languages survived the save: %q %v", p.SourceLanguage, p.TargetLanguages)
	}

	live := testutil.SeedProject(t, f.q, f.tenant.ID, "Live", "en", []string{"fr"})
	rec = f.saveDraft(t, live.ID, map[string]any{"name": "Hijacked", "source_language": "en", "target_languages": []string{"fr"}})
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	if p := f.project(t, live.ID); p.Name != "Live" {
		t.Errorf("live project renamed through the draft route: %q", p.Name)
	}
}

// A draft that stops following a TM profile gets its own TM rows back; one
// that starts following one drops them. RED without the detach step in
// HandleSaveDraft (the replace is refused as "linked").
func TestIntegrationDraftSaveSwitchesTMProfileBothWays(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftTMProfile")
	defer cleanup()
	tm := testutil.SeedTM(t, f.q, f.tenant.ID, "Own TM")
	tp := testutil.SeedTMProfile(t, f.q, f.tenant.ID, "Shared", 0.8)

	id := f.createDraft(t, map[string]any{"name": "P", "settings": map[string]any{"tm_profile_id": tp.ID}})
	p := f.project(t, id)
	if profile, _ := f.q.GetSettingsProfile(context.Background(), p.SettingsProfileID); profile.TmProfileID == nil {
		t.Fatal("draft not linked to the TM profile on create")
	}

	rec := f.saveDraft(t, id, map[string]any{
		"name": "P", "tm_assignments": []map[string]any{{"tm_id": tm.ID, "mode": "read", "priority": 1}},
	})
	testutil.AssertStatus(t, rec, http.StatusOK)
	profile, _ := f.q.GetSettingsProfile(context.Background(), p.SettingsProfileID)
	if profile.TmProfileID != nil {
		t.Error("TM profile link survived a save without tm_profile_id")
	}
	if tms, _ := f.q.ListSettingsTMAssignments(context.Background(), p.SettingsProfileID); len(tms) != 1 || tms[0].TmID != tm.ID {
		t.Errorf("own TM rows after detach = %v, want the one TM", tms)
	}

	rec = f.saveDraft(t, id, map[string]any{"name": "P", "settings": map[string]any{"tm_profile_id": tp.ID}})
	testutil.AssertStatus(t, rec, http.StatusOK)
	if profile, _ := f.q.GetSettingsProfile(context.Background(), p.SettingsProfileID); profile.TmProfileID == nil || *profile.TmProfileID != tp.ID {
		t.Error("draft not re-linked to the TM profile")
	}
}

// A draft save naming another org's TM profile is refused before anything is
// written: the draft keeps its name and its link to its own profile. The same
// save with the caller's own profile is the positive control.
func TestIntegrationDraftSaveRefusesForeignTMProfile(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftForeignTMProfile")
	defer cleanup()
	ctx := context.Background()
	own := testutil.SeedTMProfile(t, f.q, f.tenant.ID, "Own", 0.8)
	other := testutil.SeedTenant(t, f.q, "OtherCorp")
	foreign := testutil.SeedTMProfile(t, f.q, other.ID, "Foreign", 0.8)
	link := func(p db.Project) *int64 {
		t.Helper()
		profile, err := f.q.GetSettingsProfile(ctx, p.SettingsProfileID)
		if err != nil {
			t.Fatalf("GetSettingsProfile: %v", err)
		}
		return profile.TmProfileID
	}

	id := f.createDraft(t, map[string]any{"name": "P", "settings": map[string]any{"tm_profile_id": own.ID}})

	rec := f.saveDraft(t, id, map[string]any{"name": "Renamed", "settings": map[string]any{"tm_profile_id": foreign.ID}})
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	p := f.project(t, id)
	if p.Name != "P" {
		t.Errorf("refused save renamed the draft to %q", p.Name)
	}
	if l := link(p); l == nil || *l != own.ID {
		t.Errorf("refused save changed the TM profile link to %s, want %d", testutil.Show(l), own.ID)
	}
	if n := testutil.CountRows(t, f.pool,
		`SELECT count(*) FROM settings_profiles WHERE tm_profile_id = $1 AND tenant_id = $2`, foreign.ID, f.tenant.ID); n != 0 {
		t.Errorf("%d settings row(s) of this org follow the other org's TM profile", n)
	}

	rec = f.saveDraft(t, id, map[string]any{"name": "Renamed", "settings": map[string]any{"tm_profile_id": own.ID}})
	testutil.AssertStatus(t, rec, http.StatusOK)
	if p := f.project(t, id); p.Name != "Renamed" {
		t.Errorf("own-profile save: name = %q, want Renamed", p.Name)
	}
}

// Publish is where the required fields are enforced: a draft without a name
// stays a draft.
func TestIntegrationDraftPublishRequiresCompleteMetadata(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftPublishIncomplete")
	defer cleanup()

	id := f.createDraft(t, map[string]any{"source_language": "en", "target_languages": []string{"fr"}})
	rec := f.publish(t, id)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if p := f.project(t, id); p.Status != db.ProjectStatusDraft {
		t.Errorf("status after refused publish = %s, want draft", p.Status)
	}
	assertOnlyCreated(t, f, id, "refused publish")
}

// Publish flips the status and completes the project's ONE project.created event,
// carrying the configuration as it stands at publish (one event for the
// whole initial setup). A second publish is refused.
func TestIntegrationDraftPublishEmitsSingleCreatedEvent(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftPublish")
	defer cleanup()
	tm := testutil.SeedTM(t, f.q, f.tenant.ID, "Publish TM")

	id := f.createDraft(t, map[string]any{"name": "Half"})
	testutil.AssertStatus(t, f.saveDraft(t, id, map[string]any{
		"name": "Whole", "source_language": "en", "target_languages": []string{"fr"},
		"settings":       map[string]any{"mt_on_upload": true},
		"tm_assignments": []map[string]any{{"tm_id": tm.ID, "mode": "read", "priority": 1}},
	}), http.StatusOK)

	rec := f.publish(t, id)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp projectResp
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Status != "in_progress" {
		t.Errorf("published status = %q, want in_progress", resp.Data.Status)
	}

	page := listEvents(t, f.router, id, f.user.ID, f.tenant.ID)
	if len(page.Data) != 1 || page.Data[0].EventType != "project.created" {
		types := make([]string, 0, len(page.Data))
		for _, e := range page.Data {
			types = append(types, e.EventType)
		}
		t.Fatalf("expected exactly one project.created after publish, got %v", types)
	}
	ev := page.Data[0]
	if ev.Details["name"] != "Whole" {
		t.Errorf("event name = %v, want the published name", ev.Details["name"])
	}
	settingsDetails, _ := ev.Details["settings"].(map[string]any)
	if settingsDetails["mt_on_upload"] != true {
		t.Errorf("event settings snapshot = %v, want mt_on_upload=true", ev.Details["settings"])
	}
	if tms, _ := ev.Details["tm_assignments"].([]any); len(tms) != 1 {
		t.Errorf("event tm_assignments = %v, want 1", ev.Details["tm_assignments"])
	}
	if ev.Actor == nil || ev.Actor.ID != f.user.ID {
		t.Errorf("event actor = %v, want the publishing user", ev.Actor)
	}

	testutil.AssertStatus(t, f.publish(t, id), http.StatusBadRequest)
	testutil.AssertStatus(t, f.saveDraft(t, id, map[string]any{"name": "Late edit"}), http.StatusNotFound)
}

// The constraint behind the handler: a row cannot leave draft while
// incomplete, whatever path writes it.
func TestIntegrationDraftCheckConstraintBacksTheHandler(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DraftCheck")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@draftcheck.test", "pass", "project_manager")
	f := draftFixture{router: router, q: q, tenant: tenant, user: user}

	// One draft per missing field: the constraint has three arms and a typo in
	// any one of them would otherwise hide behind the other two.
	for _, tc := range []struct {
		name  string
		draft map[string]any
	}{
		{"no name", map[string]any{"name": "", "source_language": "en", "target_languages": []string{"fr"}}},
		{"no source language", map[string]any{"name": "P", "target_languages": []string{"fr"}}},
		{"no target languages", map[string]any{"name": "P", "source_language": "en"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := f.createDraft(t, tc.draft)
			_, err := pool.Exec(context.Background(), `UPDATE projects SET status = 'in_progress' WHERE id = $1`, id)
			if err == nil {
				t.Fatal("expected projects_complete_unless_draft to reject the incomplete row")
			}
			// Any error would pass a bare nil check — a typo in the UPDATE
			// included. Pin the constraint that has to be the one refusing.
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("expected a PgError, got %T: %v", err, err)
			}
			if pgErr.Code != "23514" || pgErr.ConstraintName != "projects_complete_unless_draft" {
				t.Errorf("got SQLSTATE %s on %q, want 23514 on projects_complete_unless_draft", pgErr.Code, pgErr.ConstraintName)
			}
		})
	}
}

// A draft records where its settings came from: the template id is stamped on
// save (and cleared when the form drops it), so the published project shows
// its provenance. RED without the SetProjectTemplate call in HandleSaveDraft.
func TestIntegrationDraftSaveStampsTemplateProvenance(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftTemplate")
	defer cleanup()
	tmpl := testutil.SeedTemplate(t, f.q, f.tenant.ID, "Draft Template")

	id := f.createDraft(t, map[string]any{"name": "P"})
	body := map[string]any{"name": "P", "source_language": "en", "target_languages": []string{"fr"}}

	rec := f.saveDraft(t, id, withKey(body, "template_id", tmpl.ID))
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp projectResp
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.TemplateID == nil || *resp.Data.TemplateID != tmpl.ID {
		t.Fatalf("template_id after save = %v, want %d", resp.Data.TemplateID, tmpl.ID)
	}
	if p := f.project(t, id); p.TemplateID == nil || *p.TemplateID != tmpl.ID {
		t.Errorf("stored template_id = %v, want %d", p.TemplateID, tmpl.ID)
	}

	// The form dropped the template again: the stamp goes with it.
	rec = f.saveDraft(t, id, body)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if p := f.project(t, id); p.TemplateID != nil {
		t.Errorf("stored template_id = %v after the form cleared it, want nil", p.TemplateID)
	}

	// Another tenant's template is not a template this draft can name.
	other := testutil.SeedTenant(t, f.q, "DraftTemplateOther")
	foreign := testutil.SeedTemplate(t, f.q, other.ID, "Foreign Template")
	rec = f.saveDraft(t, id, withKey(body, "template_id", foreign.ID))
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if p := f.project(t, id); p.TemplateID != nil {
		t.Errorf("stored template_id = %v after a refused save, want nil", p.TemplateID)
	}
}

// withKey returns a copy of body with one extra key — the tests above reuse
// one body across saves that differ by a single field.
func withKey(body map[string]any, key string, value any) map[string]any {
	out := make(map[string]any, len(body)+1)
	for k, v := range body {
		out[k] = v
	}
	out[key] = value
	return out
}

// Both draft routes are tenant-scoped: another tenant's draft is a 404.
func TestIntegrationDraftRoutesAreTenantScoped(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftTenantA")
	defer cleanup()
	other := testutil.SeedTenant(t, f.q, "DraftTenantB")
	intruder := testutil.SeedUserWithRole(t, f.q, other.ID, "pm@draftb.test", "pass", "project_manager")

	id := f.createDraft(t, map[string]any{"name": "Mine", "source_language": "en", "target_languages": []string{"fr"}})
	rec := testutil.DoRequestWithRole(t, f.router, "PUT", fmt.Sprintf("/api/projects/%d/draft", id),
		map[string]any{"name": "Theirs"}, intruder.ID, other.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	rec = testutil.DoRequestWithRole(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", id),
		nil, intruder.ID, other.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	// Delete is the third: it carries no RequireRole, so GetProject's tenant
	// predicate is the whole gate. RED if it is ever swapped for the
	// `-- Internal:` GetProjectByID.
	rec = testutil.DoRequestWithRole(t, f.router, "DELETE", fmt.Sprintf("/api/projects/%d", id),
		nil, intruder.ID, other.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	if p := f.project(t, id); p.Name != "Mine" || p.Status != db.ProjectStatusDraft {
		t.Errorf("cross-tenant request changed the draft: %q %s", p.Name, p.Status)
	}
}

// A live project never goes back to draft through PATCH: the wizard's select
// no longer offers it, and the server enforces the same.
func TestIntegrationProjectPatchRefusesDraftStatus(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftPatch")
	defer cleanup()
	live := testutil.SeedProject(t, f.q, f.tenant.ID, "Live", "en", []string{"fr"})

	rec := testutil.DoRequestWithRole(t, f.router, "PATCH", fmt.Sprintf("/api/projects/%d", live.ID),
		map[string]any{"status": "draft"}, f.user.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if p := f.project(t, live.ID); p.Status != db.ProjectStatusInProgress {
		t.Errorf("status = %s, want in_progress", p.Status)
	}
}

// …and a draft never goes live through PATCH either: that route skips the
// required-field check, leaves the draft's files without jobs and records a
// metadata change where the project's one project.created belongs. Publish is
// the only way out. RED without the oldProject.Status guard in
// HandleUpdateMetadata.
func TestIntegrationProjectPatchRefusesPublishingADraft(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftPatchOut")
	defer cleanup()

	// Complete enough that the CHECK constraint would NOT have caught it —
	// the handler is what has to refuse.
	id := f.createDraft(t, map[string]any{
		"name": "Complete draft", "source_language": "en", "target_languages": []string{"fr"},
	})

	for _, status := range []string{"in_progress", "completed", "on_hold"} {
		rec := testutil.DoRequestWithRole(t, f.router, "PATCH", fmt.Sprintf("/api/projects/%d", id),
			map[string]any{"status": status}, f.user.ID, f.tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		if p := f.project(t, id); p.Status != db.ProjectStatusDraft {
			t.Fatalf("status after PATCH %s = %s, want draft", status, p.Status)
		}
	}
	assertOnlyCreated(t, f, id, "refused PATCHes")

	// The rest of the PATCH surface still reaches a draft — only the status is
	// off limits.
	rec := testutil.DoRequestWithRole(t, f.router, "PATCH", fmt.Sprintf("/api/projects/%d", id),
		map[string]any{"domain": "legal"}, f.user.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if p := f.project(t, id); p.Status != db.ProjectStatusDraft || p.Domain == nil || *p.Domain != "legal" {
		t.Errorf("after a domain PATCH: status %s domain %v, want draft / legal", p.Status, p.Domain)
	}

	// And publish still takes it live.
	testutil.AssertStatus(t, f.publish(t, id), http.StatusOK)
}
