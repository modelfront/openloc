package project_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Saving the New project form as a template must carry the TM match penalties
// and the TM profile link, not just the insert-time scalars. Penalties are
// non-default values so a template on the column defaults fails the check.
func TestIntegrationTemplateFromForm_CarriesPenaltiesAndTMProfile(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "SnapCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "snap@test.com", "pass")
	tp := testutil.SeedTMProfile(t, q, tenant.ID, "Shared", 0.6)

	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name":     "Snapshot",
		"settings": map[string]any{"sublanguage_penalty_pct": 9, "near_exact_penalty_pct": 4, "tm_profile_id": tp.ID},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp struct {
		Data struct {
			SettingsProfileID int64 `json:"settings_profile_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	tmplProfile, err := q.GetSettingsProfile(ctx, resp.Data.SettingsProfileID)
	if err != nil {
		t.Fatalf("GetSettingsProfile: %v", err)
	}
	if tmplProfile.SublanguagePenaltyPct != 9 || tmplProfile.NearExactPenaltyPct != 4 {
		t.Errorf("template penalties = %d/%d, want 9/4 (the project's)", tmplProfile.SublanguagePenaltyPct, tmplProfile.NearExactPenaltyPct)
	}
	if tmplProfile.TmProfileID == nil || *tmplProfile.TmProfileID != tp.ID {
		t.Errorf("template tm_profile_id = %v, want %d (the project's link)", tmplProfile.TmProfileID, tp.ID)
	}
}

// Linking, the read-only guards, detach and save-as, all through the HTTP
// surface a PM uses.
func TestIntegrationProjectTMProfile_LinkDetachSaveAs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "LinkCorp")
	other := testutil.SeedTenant(t, q, "OtherCorp")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@test.com", "pass", "project_manager")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"fr"})
	ownTM := testutil.SeedTMWithLangs(t, q, tenant.ID, "Own TM", "en", []string{"fr"})
	profTM := testutil.SeedTMWithLangs(t, q, tenant.ID, "Profile TM", "en", []string{"fr"})
	settingsPath := fmt.Sprintf("/api/projects/%d/settings", project.ID)
	tmsPath := fmt.Sprintf("/api/projects/%d/tms", project.ID)
	do := func(method, path string, body any) *httptest.ResponseRecorder {
		return testutil.DoRequestWithRole(t, router, method, path, body, pm.ID, tenant.ID, "project_manager")
	}

	rec := do("PUT", tmsPath, map[string]any{"assignments": []map[string]any{{"tm_id": ownTM.ID, "mode": "read_write", "priority": 1}}})
	testutil.AssertStatus(t, rec, http.StatusOK)

	tp := testutil.SeedTMProfile(t, q, tenant.ID, "Shared", 0.6)
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: tp.SettingsProfileID, TmID: profTM.ID, Mode: "read", Priority: 2, PenaltyPct: 8,
	}); err != nil {
		t.Fatalf("assign profile TM: %v", err)
	}
	foreign := testutil.SeedTMProfile(t, q, other.ID, "Foreign", 0.5)

	type settingsResp struct {
		Data struct {
			Project struct {
				FuzzyThreshold        float32 `json:"fuzzy_threshold"`
				SublanguagePenaltyPct int32   `json:"sublanguage_penalty_pct"`
			} `json:"project"`
			TMAssignments []struct {
				TmID       int64 `json:"tm_id"`
				PenaltyPct int32 `json:"penalty_pct"`
			} `json:"tm_assignments"`
			TMProfile *struct {
				ID         int64  `json:"id"`
				Name       string `json:"name"`
				UsageCount int32  `json:"usage_count"`
			} `json:"tm_profile"`
		} `json:"data"`
	}
	getSettings := func() settingsResp {
		t.Helper()
		rec := do("GET", settingsPath, nil)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var s settingsResp
		testutil.DecodeJSON(t, rec, &s)
		return s
	}

	// Unlinked: own values, tm_profile null.
	s := getSettings()
	if s.Data.TMProfile != nil || len(s.Data.TMAssignments) != 1 || s.Data.TMAssignments[0].TmID != ownTM.ID {
		t.Fatalf("unlinked GET = %+v", s.Data)
	}

	// Bad links.
	testutil.AssertStatus(t, do("PATCH", settingsPath, map[string]any{"tm_profile_id": foreign.ID}), http.StatusBadRequest)
	testutil.AssertStatus(t, do("PATCH", settingsPath, map[string]any{"tm_profile_id": nil}), http.StatusBadRequest)
	testutil.AssertStatus(t, do("PATCH", settingsPath, map[string]any{"tm_profile_id": -3}), http.StatusBadRequest)
	// Linking and editing scoring in one request → 409, nothing applied.
	testutil.AssertStatus(t, do("PATCH", settingsPath, map[string]any{"tm_profile_id": tp.ID, "fuzzy_threshold": 0.9}), http.StatusConflict)
	if s = getSettings(); s.Data.TMProfile != nil {
		t.Fatal("a refused PATCH must not link")
	}

	// Link: GET shows the profile's rows + scoring and names the profile.
	testutil.AssertStatus(t, do("PATCH", settingsPath, map[string]any{"tm_profile_id": tp.ID}), http.StatusOK)
	s = getSettings()
	if s.Data.TMProfile == nil || s.Data.TMProfile.ID != tp.ID || s.Data.TMProfile.Name != "Shared" {
		t.Fatalf("linked GET tm_profile = %+v, want Shared", s.Data.TMProfile)
	}
	if s.Data.TMProfile.UsageCount != 1 {
		t.Errorf("usage_count = %d, want 1 (this project is the only follower)", s.Data.TMProfile.UsageCount)
	}
	if s.Data.Project.FuzzyThreshold != 0.6 {
		t.Errorf("linked threshold = %v, want the profile's 0.6", s.Data.Project.FuzzyThreshold)
	}
	if len(s.Data.TMAssignments) != 1 || s.Data.TMAssignments[0].TmID != profTM.ID || s.Data.TMAssignments[0].PenaltyPct != 8 {
		t.Errorf("linked rows = %+v, want the profile's TM with penalty 8", s.Data.TMAssignments)
	}
	if own, _ := q.ListSettingsTMAssignments(ctx, project.SettingsProfileID); len(own) != 0 {
		t.Errorf("link must clear the project's own rows, got %d", len(own))
	}

	// Read-only while linked: scoring PATCH and PUT tms → 409 with a stable code.
	rec = do("PATCH", settingsPath, map[string]any{"sublanguage_penalty_pct": 5})
	testutil.AssertStatus(t, rec, http.StatusConflict)
	var conflict struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	testutil.DecodeJSON(t, rec, &conflict)
	if conflict.Error.Code != "tm_profile_linked" {
		t.Errorf("409 code = %q, want tm_profile_linked", conflict.Error.Code)
	}
	testutil.AssertStatus(t, do("PUT", tmsPath, map[string]any{"assignments": []map[string]any{{"tm_id": ownTM.ID, "mode": "read", "priority": 1}}}), http.StatusConflict)
	// Non-TM settings still editable while linked.
	testutil.AssertStatus(t, do("PATCH", settingsPath, map[string]any{"mt_on_upload": true}), http.StatusOK)

	// Detach: rows + scoring copied, link gone, editing works again.
	rec = do("POST", fmt.Sprintf("/api/projects/%d/tms/detach", project.ID), nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	s = getSettings()
	if s.Data.TMProfile != nil {
		t.Fatalf("detached GET still names a profile: %+v", s.Data.TMProfile)
	}
	if s.Data.Project.FuzzyThreshold != 0.6 || len(s.Data.TMAssignments) != 1 || s.Data.TMAssignments[0].TmID != profTM.ID {
		t.Errorf("detach must copy scoring + rows: %+v", s.Data)
	}
	testutil.AssertStatus(t, do("PATCH", settingsPath, map[string]any{"fuzzy_threshold": 0.8}), http.StatusOK)
	testutil.AssertStatus(t, do("POST", fmt.Sprintf("/api/projects/%d/tms/detach", project.ID), nil), http.StatusBadRequest)

	// Save current TMs as a new profile and follow it.
	rec = do("POST", fmt.Sprintf("/api/projects/%d/save-tm-profile", project.ID), map[string]any{"name": "From project", "link": true})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var saved struct {
		Data struct {
			TMProfile struct {
				ID int64 `json:"id"`
			} `json:"tm_profile"`
			Linked bool `json:"linked"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &saved)
	if !saved.Data.Linked || saved.Data.TMProfile.ID == 0 {
		t.Fatalf("save-tm-profile = %+v", saved.Data)
	}
	s = getSettings()
	if s.Data.TMProfile == nil || s.Data.TMProfile.ID != saved.Data.TMProfile.ID || s.Data.Project.FuzzyThreshold != 0.8 {
		t.Errorf("after save+link: %+v (threshold %v, want 0.8 carried into the new profile)", s.Data.TMProfile, s.Data.Project.FuzzyThreshold)
	}
	newRows, err := q.ListSettingsTMAssignments(ctx, mustProfileSettings(t, q, saved.Data.TMProfile.ID, tenant.ID))
	if err != nil || len(newRows) != 1 || newRows[0].TmID != profTM.ID {
		t.Errorf("new profile rows = %+v err=%v, want the project's TM", newRows, err)
	}
	// Duplicate name → 409.
	testutil.AssertStatus(t, do("POST", fmt.Sprintf("/api/projects/%d/save-tm-profile", project.ID), map[string]any{"name": "from PROJECT"}), http.StatusConflict)
}

func mustProfileSettings(t *testing.T, q db.Querier, profileID, tenantID int64) int64 {
	t.Helper()
	tp, err := q.GetTMProfile(context.Background(), db.GetTMProfileParams{ID: profileID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("GetTMProfile: %v", err)
	}
	return tp.SettingsProfileID
}

// Create wizard: settings.tm_profile_id links the new project; combined with
// tm_assignments it is refused; a linked template hands its link down.
func TestIntegrationProjectCreate_TMProfile(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "WizardCorp")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@test.com", "pass", "project_manager")
	tm := testutil.SeedTM(t, q, tenant.ID, "TM")
	tp := testutil.SeedTMProfile(t, q, tenant.ID, "Wizard profile", 0.7)
	do := func(body any) *httptest.ResponseRecorder {
		return testutil.DoRequestWithRole(t, router, "POST", "/api/projects", body, pm.ID, tenant.ID, "project_manager")
	}
	base := func(name string) map[string]any {
		return map[string]any{"name": name, "source_language": "en", "target_languages": []string{"fr"}}
	}
	linkOf := func(projectID int64) *int64 {
		t.Helper()
		p, err := q.GetProjectByID(ctx, projectID)
		if err != nil {
			t.Fatalf("GetProjectByID: %v", err)
		}
		sp, err := q.GetSettingsProfile(ctx, p.SettingsProfileID)
		if err != nil {
			t.Fatalf("GetSettingsProfile: %v", err)
		}
		return sp.TmProfileID
	}
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}

	// Both → 400, no project created.
	body := base("Both")
	body["settings"] = map[string]any{"tm_profile_id": tp.ID}
	body["tm_assignments"] = []map[string]any{{"tm_id": tm.ID, "mode": "read", "priority": 1}}
	testutil.AssertStatus(t, do(body), http.StatusBadRequest)

	// Another tenant's profile → 400 before any row exists. The link step would
	// refuse it too, but only after the project row is written.
	otherTenant := testutil.SeedTenant(t, q, "OtherCorp")
	foreignTP := testutil.SeedTMProfile(t, q, otherTenant.ID, "Foreign profile", 0.7)
	body = base("Foreign")
	body["settings"] = map[string]any{"tm_profile_id": foreignTP.ID}
	testutil.AssertStatus(t, do(body), http.StatusBadRequest)
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM projects WHERE tenant_id = $1 AND name = 'Foreign'`, tenant.ID); n != 0 {
		t.Errorf("a refused create left %d project row(s) behind", n)
	}

	// Linked at create.
	body = base("Linked")
	body["settings"] = map[string]any{"tm_profile_id": tp.ID}
	rec := do(body)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	testutil.DecodeJSON(t, rec, &created)
	if l := linkOf(created.Data.ID); l == nil || *l != tp.ID {
		t.Errorf("wizard link = %v, want %d", l, tp.ID)
	}

	// A template that follows the profile hands the link to projects made from it.
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Linked template")
	testutil.LinkTMProfile(t, q, tmpl.SettingsProfileID, tp.ID)
	body = base("From template")
	body["template_id"] = tmpl.ID
	rec = do(body)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	testutil.DecodeJSON(t, rec, &created)
	if l := linkOf(created.Data.ID); l == nil || *l != tp.ID {
		t.Errorf("template-derived link = %v, want %d", l, tp.ID)
	}
}
