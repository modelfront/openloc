package template_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The per-template routes resolve the template by id and tenant before they
// read or write, so another tenant's template id is a 404 that changes
// nothing. Each route also runs for the template's own tenant, so the 404
// comes from the tenant check and not from the request.
func TestIntegrationTemplateRoutesCrossTenant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	victimUser, victim := testutil.RegisterUser(t, q, "victim")
	attackerUser, attacker := testutil.RegisterUser(t, q, "attacker")

	// The victim's template follows a TM profile, so detach and save-tm-profile
	// have something to change.
	tmpl := testutil.SeedTemplate(t, q, victim, "Victim template")
	profileTM := testutil.SeedTM(t, q, victim, "Victim TM")
	tp := testutil.SeedTMProfile(t, q, victim, "Victim profile", 0.8)
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: tp.SettingsProfileID, TmID: profileTM.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatalf("assign profile TM: %v", err)
	}
	testutil.LinkTMProfile(t, q, tmpl.SettingsProfileID, tp.ID)
	path := fmt.Sprintf("/api/templates/%d", tmpl.ID)

	linkedProfile := func() *int64 {
		t.Helper()
		sp, err := q.GetSettingsProfile(ctx, tmpl.SettingsProfileID)
		if err != nil {
			t.Fatalf("GetSettingsProfile: %v", err)
		}
		return sp.TmProfileID
	}

	t.Run("GET reads only the own tenant's template", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodGet, path, nil, attackerUser, attacker)
		if strings.Contains(rec.Body.String(), "Victim") {
			t.Errorf("attacker GET leaked the template: %s", rec.Body.String())
		}
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		rec = testutil.DoRequest(t, router, http.MethodGet, path, nil, victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var got struct {
			Data struct {
				Template struct {
					Name string `json:"name"`
				} `json:"template"`
				TMProfile *struct {
					ID int64 `json:"id"`
				} `json:"tm_profile"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &got)
		if got.Data.Template.Name != "Victim template" || got.Data.TMProfile == nil || got.Data.TMProfile.ID != tp.ID {
			t.Fatalf("owner GET = %+v", got.Data)
		}
	})

	t.Run("tms/detach changes only the own tenant's template", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodPost, path+"/tms/detach", nil, attackerUser, attacker)
		if got := linkedProfile(); got == nil || *got != tp.ID {
			t.Errorf("attacker detach moved the link to %s, want %d", testutil.Show(got), tp.ID)
		}
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		rec = testutil.DoRequest(t, router, http.MethodPost, path+"/tms/detach", nil, victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if got := linkedProfile(); got != nil {
			t.Fatalf("owner detach left the link at %d", *got)
		}
	})

	t.Run("save-tm-profile reads and links only the own tenant's template", func(t *testing.T) {
		before := testutil.Show(linkedProfile())
		rec := testutil.DoRequest(t, router, http.MethodPost, path+"/save-tm-profile",
			map[string]any{"name": "Stolen", "link": true}, attackerUser, attacker)
		if n := testutil.CountRows(t, pool, `SELECT count(*) FROM tm_profiles WHERE name = 'Stolen'`); n != 0 {
			t.Errorf("attacker save-tm-profile created %d profile(s) from the victim's template", n)
		}
		if after := testutil.Show(linkedProfile()); after != before {
			t.Errorf("attacker save-tm-profile moved the victim's link from %s to %s", before, after)
		}
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		rec = testutil.DoRequest(t, router, http.MethodPost, path+"/save-tm-profile",
			map[string]any{"name": "Mine", "link": true}, victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		got := linkedProfile()
		if got == nil {
			t.Fatal("owner save-tm-profile with link left the template unlinked")
		}
		saved, err := q.GetTMProfile(ctx, db.GetTMProfileParams{ID: *got, TenantID: victim})
		if err != nil || saved.Name != "Mine" {
			t.Fatalf("template links profile %d (%q, err %v), want the new profile Mine in the victim tenant", *got, saved.Name, err)
		}
	})
}

// A template may only follow a TM profile of its own tenant: create and update
// refuse another tenant's tm_profile_id and store nothing. The attacker's own
// profile, on the same routes, is the positive control.
func TestIntegrationTemplateForeignTMProfileID(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	_, victim := testutil.RegisterUser(t, q, "victim")
	victimProfile := testutil.SeedTMProfile(t, q, victim, "Victim profile", 0.8)
	attackerUser, attacker := testutil.RegisterUser(t, q, "attacker")
	ownProfile := testutil.SeedTMProfile(t, q, attacker, "Own profile", 0.7)

	// followers counts the attacker's settings rows that follow the victim's profile.
	followers := func() int64 {
		t.Helper()
		return testutil.CountRows(t, pool,
			`SELECT count(*) FROM settings_profiles WHERE tm_profile_id = $1 AND tenant_id = $2`,
			victimProfile.ID, attacker)
	}
	linkOf := func(templateID int64) *int64 {
		t.Helper()
		tmpl, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: templateID, TenantID: attacker})
		if err != nil {
			t.Fatalf("GetTemplate: %v", err)
		}
		sp, err := q.GetSettingsProfile(ctx, tmpl.SettingsProfileID)
		if err != nil {
			t.Fatalf("GetSettingsProfile: %v", err)
		}
		return sp.TmProfileID
	}

	t.Run("create", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodPost, "/api/templates", map[string]any{
			"name": "Leak", "settings": map[string]any{"tm_profile_id": victimProfile.ID},
		}, attackerUser, attacker)
		if n := testutil.CountRows(t, pool, `SELECT count(*) FROM templates WHERE name = 'Leak'`); n != 0 {
			t.Errorf("a template was created with a foreign TM profile (%d rows)", n)
		}
		if n := followers(); n != 0 {
			t.Errorf("%d attacker settings row(s) follow the victim's TM profile", n)
		}
		testutil.AssertStatus(t, rec, http.StatusBadRequest)

		rec = testutil.DoRequest(t, router, http.MethodPost, "/api/templates", map[string]any{
			"name": "Own", "settings": map[string]any{"tm_profile_id": ownProfile.ID},
		}, attackerUser, attacker)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var resp struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if got := linkOf(resp.Data.ID); got == nil || *got != ownProfile.ID {
			t.Fatalf("own-profile create links %s, want %d", testutil.Show(got), ownProfile.ID)
		}
	})

	t.Run("update", func(t *testing.T) {
		tmpl := testutil.SeedTemplate(t, q, attacker, "Attacker template")
		path := fmt.Sprintf("/api/templates/%d", tmpl.ID)

		rec := testutil.DoRequest(t, router, http.MethodPatch, path,
			map[string]any{"tm_profile_id": victimProfile.ID}, attackerUser, attacker)
		if got := linkOf(tmpl.ID); got != nil {
			t.Errorf("PATCH linked the template to %d", *got)
		}
		if n := followers(); n != 0 {
			t.Errorf("%d attacker settings row(s) follow the victim's TM profile", n)
		}
		testutil.AssertStatus(t, rec, http.StatusBadRequest)

		rec = testutil.DoRequest(t, router, http.MethodPatch, path,
			map[string]any{"tm_profile_id": ownProfile.ID}, attackerUser, attacker)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if got := linkOf(tmpl.ID); got == nil || *got != ownProfile.ID {
			t.Fatalf("own-profile PATCH links %s, want %d", testutil.Show(got), ownProfile.ID)
		}
	})
}

// A template may point only at a QA profile of its own tenant: PATCH with
// another tenant's profile id is refused and leaves the template's profile
// unchanged, the same answer as for an id that does not exist.
func TestIntegrationTemplateUpdateRefusesCrossTenantQAProfile(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	_, victim := testutil.RegisterUser(t, q, "qa-victim")
	attackerUser, attacker := testutil.RegisterUser(t, q, "qa-attacker")
	victimQA, err := q.CreateQAProfile(ctx, db.CreateQAProfileParams{TenantID: victim, Name: "Victim QA"})
	if err != nil {
		t.Fatalf("seed QA profile: %v", err)
	}
	tmpl := testutil.SeedTemplate(t, q, attacker, "Attacker template")
	path := fmt.Sprintf("/api/templates/%d", tmpl.ID)

	for _, id := range []int64{victimQA.ID, victimQA.ID + 1000} {
		rec := testutil.DoRequest(t, router, http.MethodPatch, path, map[string]any{"qa_profile_id": id}, attackerUser, attacker)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		if !strings.Contains(rec.Body.String(), "QA profile not found in this tenant") {
			t.Errorf("qa_profile_id %d: body = %s", id, rec.Body.String())
		}
	}
	sp, err := q.GetSettingsProfile(ctx, tmpl.SettingsProfileID)
	if err != nil {
		t.Fatalf("GetSettingsProfile: %v", err)
	}
	if sp.QaProfileID != nil {
		t.Errorf("template QA profile = %d, want none", *sp.QaProfileID)
	}
}
