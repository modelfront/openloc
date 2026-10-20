package template_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Duplicate must deep-copy the whole template: profile scalars INCLUDING
// qa_profile_id (which template create doesn't even accept), the MT router
// pointer, plus the TM/term/workflow assignment lists — and never share the
// settings profile with the source.
func TestIntegrationTemplateDuplicate(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DupCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "dup@test.com", "pass")

	qaProfile, err := q.CreateQAProfile(ctx, db.CreateQAProfileParams{
		TenantID: tenant.ID, Name: "Strict QA",
	})
	if err != nil {
		t.Fatalf("seed QA profile: %v", err)
	}
	src := testutil.SeedTemplate(t, q, tenant.ID, "Source Template")
	if _, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID: src.SettingsProfileID, FuzzyThreshold: 0.91, MtOnUpload: true,
		TmOnUpload: true, QaProfileID: &qaProfile.ID,
	}); err != nil {
		t.Fatalf("configure source profile: %v", err)
	}
	tm := testutil.SeedTM(t, q, tenant.ID, "Dup TM")
	mt := testutil.SeedMTEngine(t, q, tenant.ID, "Dup MT", "mock")
	tb := testutil.SeedTermBase(t, q, tenant.ID, "Dup Terms", "en", "de")
	rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/tms", src.ID), map[string]any{
		"assignments": []map[string]any{{"tm_id": tm.ID, "mode": "read_write", "priority": 1}},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	mtRouter := testutil.SeedMTRouter(t, q, tenant.ID, "Dup router", &mt.ID, nil)
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", src.ID), map[string]any{
		"mt_router_id": mtRouter,
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/terminology", src.ID), map[string]any{
		"assignments": []map[string]any{{"term_base_id": tb.ID, "source_language": "en", "target_language": "de", "mode": "enforce", "priority": 1}},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", src.ID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenant.ID, "translation", map[string]any{"role_required": "linguist"}),
			testutil.LibraryStep(t, q, tenant.ID, "review", map[string]any{"role_required": "reviewer"}),
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Duplicate
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/templates/%d/duplicate", src.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var dupResp struct {
		Data struct {
			ID                int64   `json:"id"`
			Name              string  `json:"name"`
			SettingsProfileID int64   `json:"settings_profile_id"`
			FuzzyThreshold    float32 `json:"fuzzy_threshold"`
			MtOnUpload        bool    `json:"mt_on_upload"`
			TmOnUpload        bool    `json:"tm_on_upload"`
			QaProfileID       *int64  `json:"qa_profile_id"`
			MTRouterID        *int64  `json:"mt_router_id"`
			MTRouterName      *string `json:"mt_router_name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &dupResp)
	d := dupResp.Data

	if d.Name != "Copy of Source Template" {
		t.Errorf("name = %q, want %q", d.Name, "Copy of Source Template")
	}
	if d.SettingsProfileID == src.SettingsProfileID {
		t.Fatal("duplicate shares the source settings profile — must be a deep copy")
	}
	if d.FuzzyThreshold != 0.91 || !d.MtOnUpload || !d.TmOnUpload {
		t.Errorf("scalars not copied: %+v", d)
	}
	if d.QaProfileID == nil || *d.QaProfileID != qaProfile.ID {
		t.Errorf("qa_profile_id not copied: got %v, want %d", d.QaProfileID, qaProfile.ID)
	}
	// The MT router is shared by reference: both profiles point at the same
	// router, no router copy is made.
	if d.MTRouterID == nil || *d.MTRouterID != mtRouter {
		t.Errorf("mt_router_id not copied: got %v, want %d", d.MTRouterID, mtRouter)
	}
	if d.MTRouterName == nil || *d.MTRouterName != "Dup router" {
		t.Errorf("mt_router_name not resolved on the duplicate: got %v", d.MTRouterName)
	}
	dupProfile, err := q.GetSettingsProfile(ctx, d.SettingsProfileID)
	if err != nil {
		t.Fatalf("get duplicate profile: %v", err)
	}
	if dupProfile.MtRouterID == nil || *dupProfile.MtRouterID != mtRouter {
		t.Fatalf("duplicate profile row must carry mt_router_id %d, got %v", mtRouter, dupProfile.MtRouterID)
	}
	if routers, err := q.ListMTRouters(ctx, tenant.ID); err != nil || len(routers) != 1 {
		t.Fatalf("duplicate must not create a second router: %v / %d", err, len(routers))
	}

	// The three assignment lists copied onto the NEW profile.
	tms, _ := q.ListSettingsTMAssignments(ctx, d.SettingsProfileID)
	terms, _ := q.ListSettingsTermAssignments(ctx, d.SettingsProfileID)
	steps, _ := q.ListSettingsWorkflowSteps(ctx, d.SettingsProfileID)
	if len(tms) != 1 || len(terms) != 1 || len(steps) != 2 {
		t.Fatalf("assignments not copied: tm=%d term=%d steps=%d", len(tms), len(terms), len(steps))
	}

	// Mutating the duplicate must not touch the source (independent profiles).
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/tms", d.ID), map[string]any{
		"assignments": []map[string]any{},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	srcTMs, _ := q.ListSettingsTMAssignments(ctx, src.SettingsProfileID)
	if len(srcTMs) != 1 {
		t.Fatalf("clearing the duplicate's TMs leaked into the source: %d", len(srcTMs))
	}

	// Tenant isolation: a foreign tenant cannot duplicate this template.
	tenantB := testutil.SeedTenant(t, q, "OtherCorp")
	userB := testutil.SeedUser(t, q, tenantB.ID, "other@test.com", "pass")
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/templates/%d/duplicate", src.ID), nil, userB.ID, tenantB.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

// Templates are create-time-only on projects: the post-create apply/re-point
// endpoints must be gone, and template_id on POST /api/projects must reject
// foreign templates without leaving a half-created project.
func TestIntegrationTemplateCreateTimeOnly(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "LockCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "lock@test.com", "pass")
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Lock Template")
	project := testutil.SeedProject(t, q, tenant.ID, "Lock Project", "en", []string{"de"})

	// Removed routes are gone.
	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/apply-template", project.ID), map[string]any{
		"template_id": tmpl.ID,
	}, user.ID, tenant.ID)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("apply-template still routed: %d", rec.Code)
	}
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d/template", project.ID), map[string]any{
		"template_id": tmpl.ID,
	}, user.ID, tenant.ID)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PATCH /template still routed: %d", rec.Code)
	}

	// Cross-tenant template on create: 400 + no orphan project.
	tenantB := testutil.SeedTenant(t, q, "LockCorpB")
	tmplB := testutil.SeedTemplate(t, q, tenantB.ID, "Foreign Template")
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "Cross Project", "source_language": "en", "target_languages": []string{"de"},
		"template_id": tmplB.ID,
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	projects, err := q.ListProjects(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	for _, p := range projects {
		if p.Name == "Cross Project" {
			t.Fatal("orphan project created despite foreign template_id")
		}
	}
}

// Penalties are settings like any other: a duplicated template has to carry
// them, including the per-TM penalty on each assignment. Dropping either would
// silently hand the copy different matching behaviour from the original.
func TestIntegrationTemplateDuplicateCarriesProfileScalars(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PenaltyDupCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "pen@test.com", "pass")

	src := testutil.SeedTemplate(t, q, tenant.ID, "Penalty Template")
	sublanguage, nearExact := int32(7), int32(3)
	// auto_complete is seeded to the NON-default (false) on purpose: at the
	// column default both source and copy read true, so the copy would pass
	// without carrying anything.
	autoComplete := false
	if _, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID: src.SettingsProfileID, FuzzyThreshold: 0.75,
		SublanguagePenaltyPct: &sublanguage, NearExactPenaltyPct: &nearExact,
		AutoComplete: &autoComplete,
	}); err != nil {
		t.Fatalf("configure source profile: %v", err)
	}

	tm := testutil.SeedTM(t, q, tenant.ID, "Penalised TM")
	rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/tms", src.ID), map[string]any{
		"assignments": []map[string]any{{"tm_id": tm.ID, "mode": "read_write", "priority": 1, "penalty_pct": 12}},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/templates/%d/duplicate", src.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var dupResp struct {
		Data struct {
			ID                    int64 `json:"id"`
			SettingsProfileID     int64 `json:"settings_profile_id"`
			SublanguagePenaltyPct int32 `json:"sublanguage_penalty_pct"`
			NearExactPenaltyPct   int32 `json:"near_exact_penalty_pct"`
			AutoComplete          bool  `json:"auto_complete"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &dupResp)

	if dupResp.Data.SublanguagePenaltyPct != 7 {
		t.Errorf("sublanguage_penalty_pct = %d, want 7", dupResp.Data.SublanguagePenaltyPct)
	}
	if dupResp.Data.NearExactPenaltyPct != 3 {
		t.Errorf("near_exact_penalty_pct = %d, want 3", dupResp.Data.NearExactPenaltyPct)
	}
	if dupResp.Data.AutoComplete {
		t.Errorf("auto_complete = true, want false (the source's value)")
	}

	assigns, err := q.ListSettingsTMAssignments(ctx, dupResp.Data.SettingsProfileID)
	if err != nil {
		t.Fatalf("ListSettingsTMAssignments: %v", err)
	}
	if len(assigns) != 1 {
		t.Fatalf("expected 1 TM assignment on the copy, got %d", len(assigns))
	}
	if assigns[0].PenaltyPct != 12 {
		t.Errorf("copied assignment penalty_pct = %d, want 12", assigns[0].PenaltyPct)
	}
}
