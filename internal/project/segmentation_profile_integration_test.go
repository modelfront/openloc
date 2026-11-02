package project_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/segmentation"
	"openloc/internal/segmentationapi"
	"openloc/internal/testutil"
)

type profileFixture struct {
	t        *testing.T
	pool     *pgxpool.Pool
	q        db.Querier
	router   http.Handler
	svc      *segmentationapi.Service
	tenantID int64
	userID   int64
	// workflowID is the org's library workflow, which every project it creates names.
	workflowID int64
}

func newProfileFixture(t *testing.T, name string) profileFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	tenant := testutil.SeedTenant(t, q, name)
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@"+name+".test", "pass")
	return profileFixture{t: t, pool: pool, q: q, router: testutil.NewTestRouter(pool), svc: segmentationapi.NewService(q, pool), tenantID: tenant.ID, userID: owner.ID,
		workflowID: testutil.LibraryWorkflow(t, q, tenant.ID)}
}

// otherOrg is the fixture for a second organization in the same database.
func (f profileFixture) otherOrg(name string) profileFixture {
	f.t.Helper()
	tenant := testutil.SeedTenant(f.t, f.q, name)
	f.tenantID = tenant.ID
	f.userID = testutil.SeedUser(f.t, f.q, tenant.ID, "owner@"+name+".test", "pass").ID
	f.workflowID = testutil.LibraryWorkflow(f.t, f.q, tenant.ID)
	return f
}

func (f profileFixture) do(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	return testutil.DoRequest(f.t, f.router, method, path, body, f.userID, f.tenantID)
}

func (f profileFixture) profile(name, lang string) int64 {
	f.t.Helper()
	rs := segmentation.RuleSet{Rules: []segmentation.Rule{{Before: `\.`, After: `\s`, Break: true}}}
	p, err := f.svc.Create(context.Background(), f.tenantID, f.userID, segmentationapi.Input{Name: name, SourceLanguages: []string{lang}, Definition: &rs})
	if err != nil {
		f.t.Fatal(err)
	}
	return p.ID
}

func (f profileFixture) suggested(lang string) int64 {
	f.t.Helper()
	list, err := f.svc.List(context.Background(), f.tenantID, lang)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, p := range list {
		if p.Suggested {
			return p.ID
		}
	}
	f.t.Fatalf("no suggested profile for %s", lang)
	return 0
}

// createProject posts a project in lang with settings and returns the status
// and, on success, the profile its settings name.
func (f profileFixture) createProject(lang string, settings map[string]any, templateID int64) (int, int64) {
	f.t.Helper()
	code, _, profile := f.createProjectFull(lang, settings, templateID)
	return code, profile
}

// createProjectFull is createProject that also returns the project's id.
func (f profileFixture) createProjectFull(lang string, settings map[string]any, templateID int64) (int, int64, int64) {
	f.t.Helper()
	body := map[string]any{"name": "P", "source_language": lang, "target_languages": []string{"it"}, "settings": settings, "workflow_id": f.workflowID}
	if templateID != 0 {
		body["template_id"] = templateID
	}
	rec := f.do("POST", "/api/projects", body)
	if rec.Code != http.StatusCreated {
		return rec.Code, 0, 0
	}
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(f.t, rec, &created)
	return rec.Code, created.Data.ID, f.projectProfile(created.Data.ID)
}

func (f profileFixture) projectProfile(projectID int64) int64 {
	f.t.Helper()
	rec := f.do("GET", fmt.Sprintf("/api/projects/%d/settings", projectID), nil)
	testutil.AssertStatus(f.t, rec, http.StatusOK)
	var out struct {
		Data struct {
			Project struct {
				SegmentationProfileID *int64 `json:"segmentation_profile_id"`
			} `json:"project"`
		} `json:"data"`
	}
	testutil.DecodeJSON(f.t, rec, &out)
	if out.Data.Project.SegmentationProfileID == nil {
		f.t.Fatal("project has no segmentation profile")
	}
	return *out.Data.Project.SegmentationProfileID
}

// A new project names the profile it asks for when that suits its language,
// else the suggested one for its language; a profile for another language,
// or another org's, is refused.
func TestIntegrationProjectCreate_SegmentationProfile(t *testing.T) {
	f := newProfileFixture(t, "SegProjectCreate")
	other := f.otherOrg("SegProjectCreateOther")
	legal := f.profile("Legal", "en")
	german := f.profile("German", "de")
	theirs := other.profile("Theirs", "en")

	if code, got := f.createProject("en-GB", map[string]any{"segmentation_profile_id": legal}, 0); code != http.StatusCreated || got != legal {
		t.Errorf("create with an en profile = %d, profile %d, want 201 and %d", code, got, legal)
	}
	if code, got := f.createProject("fr", map[string]any{}, 0); code != http.StatusCreated || got != f.suggested("fr") {
		t.Errorf("create without a profile = %d, profile %d, want the suggested one", code, got)
	}
	for name, id := range map[string]int64{"for another language": german, "of another org": theirs} {
		if code, _ := f.createProject("en", map[string]any{"segmentation_profile_id": id}, 0); code != http.StatusBadRequest {
			t.Errorf("create with a profile %s = %d, want 400", name, code)
		}
	}

	// From a template: its profile while it suits the language.
	rec := f.do("POST", "/api/templates", map[string]any{"name": "Legal EN", "source_language": "en", "settings": map[string]any{"segmentation_profile_id": legal}})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tmpl struct {
		Data struct {
			ID                    int64  `json:"id"`
			SegmentationProfileID *int64 `json:"segmentation_profile_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmpl)
	if tmpl.Data.SegmentationProfileID == nil || *tmpl.Data.SegmentationProfileID != legal {
		t.Fatalf("template profile = %v, want %d", tmpl.Data.SegmentationProfileID, legal)
	}
	if code, got := f.createProject("en", map[string]any{}, tmpl.Data.ID); code != http.StatusCreated || got != legal {
		t.Errorf("create from the template = %d, profile %d, want the template's %d", code, got, legal)
	}
	if code, got := f.createProject("de", map[string]any{}, tmpl.Data.ID); code != http.StatusCreated || got != f.suggested("de") {
		t.Errorf("create in German from an English template = %d, profile %d, want the suggested German one", code, got)
	}
}

// A template names a profile from creation on; an update may change it, and
// a language change it no longer suits moves it to the suggested one.
func TestIntegrationTemplate_SegmentationProfile(t *testing.T) {
	f := newProfileFixture(t, "SegTemplate")
	legal := f.profile("Legal", "en")
	german := f.profile("German", "de")

	rec := f.do("POST", "/api/templates", map[string]any{"name": "T", "source_language": "en"})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	type tmplResp struct {
		Data struct {
			ID                    int64  `json:"id"`
			SegmentationProfileID *int64 `json:"segmentation_profile_id"`
		} `json:"data"`
	}
	var created tmplResp
	testutil.DecodeJSON(t, rec, &created)
	if p := created.Data.SegmentationProfileID; p == nil || *p != f.suggested("en") {
		t.Errorf("template created without a profile = %v, want the suggested English one", p)
	}
	path := fmt.Sprintf("/api/templates/%d", created.Data.ID)

	profileOf := func() int64 {
		t.Helper()
		rec := f.do("GET", path, nil)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var out struct {
			Data struct {
				Template struct {
					SegmentationProfileID *int64 `json:"segmentation_profile_id"`
				} `json:"template"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &out)
		if out.Data.Template.SegmentationProfileID == nil {
			t.Fatal("template has no segmentation profile")
		}
		return *out.Data.Template.SegmentationProfileID
	}
	testutil.AssertStatus(t, f.do("PATCH", path, map[string]any{"segmentation_profile_id": legal}), http.StatusOK)
	if got := profileOf(); got != legal {
		t.Errorf("profile = %d, want %d", got, legal)
	}
	if rec := f.do("PATCH", path, map[string]any{"segmentation_profile_id": german}); rec.Code != http.StatusBadRequest {
		t.Errorf("set a German profile on an English template = %d, want 400", rec.Code)
	}
	testutil.AssertStatus(t, f.do("PATCH", path, map[string]any{"source_language": "de"}), http.StatusOK)
	if got := profileOf(); got != f.suggested("de") {
		t.Errorf("profile after the language change = %d, want the suggested German one", got)
	}
	testutil.AssertStatus(t, f.do("PATCH", path, map[string]any{"segmentation_profile_id": german}), http.StatusOK)
	if got := profileOf(); got != german {
		t.Errorf("profile = %d, want %d", got, german)
	}
}

// The Settings tab's PATCH takes a profile that suits the project's language
// and records the change by name; another org's profile, or one for another
// language, is refused and changes nothing.
func TestIntegrationProjectSettings_SegmentationProfile(t *testing.T) {
	f := newProfileFixture(t, "SegProjectSettings")
	other := f.otherOrg("SegProjectSettingsOther")
	legal := f.profile("Legal", "en")
	german := f.profile("German", "de")
	theirs := other.profile("Theirs", "en")
	code, projectID, before := f.createProjectFull("en", map[string]any{}, 0)
	if code != http.StatusCreated {
		t.Fatalf("create project = %d", code)
	}
	path := fmt.Sprintf("/api/projects/%d/settings", projectID)

	for name, id := range map[string]int64{"for another language": german, "of another org": theirs} {
		if rec := f.do("PATCH", path, map[string]any{"segmentation_profile_id": id}); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH a profile %s = %d, want 400", name, rec.Code)
		}
		if got := f.projectProfile(projectID); got != before {
			t.Errorf("profile after the refused PATCH (%s) = %d, want still %d", name, got, before)
		}
	}

	testutil.AssertStatus(t, f.do("PATCH", path, map[string]any{"segmentation_profile_id": legal}), http.StatusOK)
	if got := f.projectProfile(projectID); got != legal {
		t.Fatalf("profile = %d, want %d", got, legal)
	}
	page := listEvents(t, f.router, projectID, f.userID, f.tenantID)
	updates := eventsOfType(page, "project.settings_updated")
	if len(updates) != 1 {
		t.Fatalf("settings_updated events = %d, want 1", len(updates))
	}
	if ch := changeOf(t, updates[0], "segmentation_profile_name"); ch["from"] != "English" || ch["to"] != "Legal" {
		t.Errorf("segmentation_profile_name change = %v, want English → Legal", ch)
	}
}

// A template without a source language that keeps the any-language built-in
// passes on nothing: a project made from it takes the built-in for its own
// language. A profile the template names on purpose is passed on while it
// suits the project's language.
func TestIntegrationProjectCreate_TemplateWithoutLanguage(t *testing.T) {
	f := newProfileFixture(t, "SegTemplateNoLang")
	legal := f.profile("Legal", "en")
	template := func(settings map[string]any) int64 {
		t.Helper()
		rec := f.do("POST", "/api/templates", map[string]any{"name": "T", "settings": settings})
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var out struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &out)
		return out.Data.ID
	}
	placeholder := template(map[string]any{})
	chosen := template(map[string]any{"segmentation_profile_id": legal})

	if code, got := f.createProject("de", map[string]any{}, placeholder); code != http.StatusCreated || got != f.suggested("de") {
		t.Errorf("German project from a template with the any-language profile = %d, profile %d, want the German built-in", code, got)
	}
	if code, got := f.createProject("en", map[string]any{}, chosen); code != http.StatusCreated || got != legal {
		t.Errorf("English project from a template naming Legal = %d, profile %d, want Legal", code, got)
	}
	if code, got := f.createProject("de", map[string]any{}, chosen); code != http.StatusCreated || got != f.suggested("de") {
		t.Errorf("German project from a template naming Legal = %d, profile %d, want the German built-in", code, got)
	}
}

// Deleting a profile takes it off the organization's projects: their
// settings name none, so the Settings tab asks for another. Another
// organization's settings are never touched.
func TestIntegrationProjectSettings_DeletedSegmentationProfile(t *testing.T) {
	f := newProfileFixture(t, "SegProjectDeleted")
	legal := f.profile("Legal", "en")
	code, projectID, _ := f.createProjectFull("en", map[string]any{"segmentation_profile_id": legal}, 0)
	if code != http.StatusCreated {
		t.Fatalf("create project = %d", code)
	}
	other := f.otherOrg("SegProjectDeletedOther")
	theirs := testutil.SeedProject(t, f.q, other.tenantID, "Theirs", "en", []string{"fr"})
	if _, err := f.q.SetSettingsProfileSegmentationProfile(context.Background(), db.SetSettingsProfileSegmentationProfileParams{ID: theirs.SettingsProfileID, SegmentationProfileID: &legal}); err != nil {
		t.Fatal(err)
	}
	read := func() (id *int64, name *string) {
		t.Helper()
		rec := f.do("GET", fmt.Sprintf("/api/projects/%d/settings", projectID), nil)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var out struct {
			Data struct {
				Project struct {
					ID   *int64  `json:"segmentation_profile_id"`
					Name *string `json:"segmentation_profile_name"`
				} `json:"project"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &out)
		return out.Data.Project.ID, out.Data.Project.Name
	}
	if id, name := read(); id == nil || *id != legal || testutil.Show(name) != "Legal" {
		t.Errorf("before the delete = %s %s, want Legal", testutil.Show(id), testutil.Show(name))
	}
	if err := f.svc.Delete(context.Background(), f.tenantID, legal); err != nil {
		t.Fatal(err)
	}
	if id, name := read(); id != nil || name != nil {
		t.Errorf("after the delete = %s %s, want no profile", testutil.Show(id), testutil.Show(name))
	}
	sp, err := f.q.GetSettingsProfile(context.Background(), theirs.SettingsProfileID)
	if err != nil || sp.SegmentationProfileID == nil || *sp.SegmentationProfileID != legal {
		t.Errorf("another organization's project after the delete = %s (%v), want unchanged", testutil.Show(sp.SegmentationProfileID), err)
	}
}
