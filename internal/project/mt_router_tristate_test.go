package project_test

// `settings.mt_router_id` on POST /api/projects and PUT /draft is tri-state:
// absent keeps the template's router, null or 0 clears it, N > 0 sets it. The
// wizard sends null when the user clears a template's prefilled router.
//
// Skipped with -short.

import (
	"context"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

func TestIntegrationProjectCreateMTRouterTriState(t *testing.T) {
	f, cleanup := newDraftFixture(t, "MTRouterTriState")
	defer cleanup()
	ctx := context.Background()
	engine := testutil.SeedMTEngine(t, f.q, f.tenant.ID, "Engine", "libretranslate")
	tmpl := testutil.SeedTemplate(t, f.q, f.tenant.ID, "Template with router")
	routerID := testutil.SeedMTRouterForProfile(t, f.q, f.tenant.ID, tmpl.SettingsProfileID, &engine.ID, nil)

	routerOf := func(t *testing.T, projectID int64) *int64 {
		t.Helper()
		profile, err := f.q.GetSettingsProfile(ctx, f.project(t, projectID).SettingsProfileID)
		if err != nil {
			t.Fatalf("GetSettingsProfile: %v", err)
		}
		return profile.MtRouterID
	}

	t.Run("absent keeps the template's router", func(t *testing.T) {
		id := f.createDraft(t, map[string]any{"name": "Keep", "template_id": tmpl.ID, "settings": map[string]any{}})
		if got := routerOf(t, id); got == nil || *got != routerID {
			t.Fatalf("router = %v, want the template's %d", got, routerID)
		}
	})

	t.Run("null on create clears the template's router", func(t *testing.T) {
		id := f.createDraft(t, map[string]any{"name": "Clear", "template_id": tmpl.ID, "settings": map[string]any{"mt_router_id": nil}})
		if got := routerOf(t, id); got != nil {
			t.Fatalf("router = %d, want none", *got)
		}
	})

	t.Run("null on a draft save clears it", func(t *testing.T) {
		id := f.createDraft(t, map[string]any{"name": "Save", "template_id": tmpl.ID})
		if got := routerOf(t, id); got == nil {
			t.Fatal("template's router not copied on create")
		}
		rec := f.saveDraft(t, id, map[string]any{"name": "Save", "template_id": tmpl.ID, "settings": map[string]any{"mt_router_id": nil}})
		testutil.AssertStatus(t, rec, http.StatusOK)
		if got := routerOf(t, id); got != nil {
			t.Fatalf("router = %d after the save cleared it, want none", *got)
		}
	})

	t.Run("0 clears too", func(t *testing.T) {
		id := f.createDraft(t, map[string]any{"name": "Zero", "template_id": tmpl.ID, "settings": map[string]any{"mt_router_id": 0}})
		if got := routerOf(t, id); got != nil {
			t.Fatalf("router = %d, want none", *got)
		}
	})

	t.Run("a garbage value is refused", func(t *testing.T) {
		body := map[string]any{"name": "Bad", "status": "draft", "settings": map[string]any{"mt_router_id": "abc"}}
		rec := testutil.DoRequestWithRole(t, f.router, "POST", "/api/projects", body, f.user.ID, f.tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
	})
}
