package flowtest

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/extension/exttest"
	"openloc/internal/file"
	"openloc/internal/testutil"
)

// TestIntegrationRBACRoleMatrix verifies the four-role model:
//   - linguist        : locked out of the whole management surface (403)
//   - project_manager : full management EXCEPT delete-project/TM/TB
//   - admin           : everything minus owner-only deletes
//   - owner           : owner-only deletes (customer, vendor)
func TestIntegrationRBACRoleMatrix(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "RBACCorp")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")
	admin := testutil.SeedUserWithRole(t, q, tenant.ID, "admin@test.com", "pass", "admin")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@test.com", "pass", "project_manager")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@test.com", "pass", "linguist")

	// --- Linguists are blocked from the entire management surface ---
	t.Run("linguist blocked from management tabs", func(t *testing.T) {
		for _, path := range []string{
			"/api/projects", "/api/tms", "/api/terminology", "/api/customers",
			"/api/templates", "/api/mt-engines", "/api/qa-profiles", "/api/labels",
			// Dashboards are part of that surface. A chart PERMALINK is a
			// shareable link into it, so the link must not
			// become a way around the gate: it adds no permission concept of its
			// own and inherits this one.
			"/api/dashboards", "/api/dashboards/1", "/api/dashboards/charts/1/data",
		} {
			rec := testutil.DoRequestWithRole(t, router, "GET", path, nil, linguist.ID, tenant.ID, "linguist")
			testutil.AssertStatus(t, rec, http.StatusForbidden)
		}
	})

	// --- A chart share link is the one dashboard read open to linguists ---:
	// the token is the authorization, so its routes sit
	// outside the manager-tier group — on the real router, not a fixture.
	t.Run("linguist opens a chart share link", func(t *testing.T) {
		ctx := context.Background()
		d, err := q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: tenant.ID, Name: "Shared ops"})
		if err != nil {
			t.Fatalf("seed dashboard: %v", err)
		}
		c, err := q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
			TenantID: tenant.ID, DashboardID: d.ID, Title: "QA by severity",
			Definition: []byte(`{"measure":"qa_issues","dimensions":["severity"],"last_days":30}`),
			Viz:        []byte(`{"type":"bar","x_key":"severity","series":["issues"]}`),
		})
		if err != nil {
			t.Fatalf("seed chart: %v", err)
		}
		token := "rbac-share-token-0123456789abcdefghijklmnopqrstuv"
		if n, err := q.SetChartShareToken(ctx, db.SetChartShareTokenParams{
			ShareToken: &token, ShareCreatedBy: &owner.ID, ID: c.ID, TenantID: tenant.ID, UserID: &owner.ID,
		}); err != nil || n != 1 {
			t.Fatalf("share: %d %v", n, err)
		}
		for _, path := range []string{"/api/shared/charts/" + token, "/api/shared/charts/" + token + "/data"} {
			rec := testutil.DoRequestWithRole(t, router, "GET", path, nil, linguist.ID, tenant.ID, "linguist")
			testutil.AssertStatus(t, rec, http.StatusOK)
		}
	})

	// --- Project managers manage projects but cannot delete them ---
	t.Run("PM can list + create projects", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "GET", "/api/projects", nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusOK)
		rec = testutil.DoRequestWithRole(t, router, "POST", "/api/projects",
			map[string]any{"name": "PM Project", "source_language": "en", "target_languages": []string{"fr"}},
			pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusCreated)
	})

	// Draft autosave + publish sit next to create.
	t.Run("PM can save and publish a draft; linguist cannot", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/projects",
			map[string]any{"status": "draft", "name": "PM Draft"}, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var created struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &created)
		body := map[string]any{"name": "PM Draft", "source_language": "en", "target_languages": []string{"fr"}}

		rec = testutil.DoRequestWithRole(t, router, "PUT",
			fmt.Sprintf("/api/projects/%d/draft", created.Data.ID), body, linguist.ID, tenant.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		rec = testutil.DoRequestWithRole(t, router, "POST",
			fmt.Sprintf("/api/projects/%d/publish", created.Data.ID), nil, linguist.ID, tenant.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusForbidden)

		rec = testutil.DoRequestWithRole(t, router, "PUT",
			fmt.Sprintf("/api/projects/%d/draft", created.Data.ID), body, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusOK)
		rec = testutil.DoRequestWithRole(t, router, "POST",
			fmt.Sprintf("/api/projects/%d/publish", created.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusOK)
	})

	t.Run("PM cannot delete project (owner/admin only)", func(t *testing.T) {
		p := testutil.SeedProject(t, q, tenant.ID, "DelProj", "en", []string{"fr"})
		rec := testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/projects/%d", p.ID), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		// Admin can.
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/projects/%d", p.ID), nil, admin.ID, tenant.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusNoContent)
	})

	// …except a draft, which the manager filling the wizard in may throw away
	// with Discard. Same carve-out shape as deleting
	// a file from a draft: the route is manager, the status check is the gate.
	t.Run("PM can delete a draft but not a live project; linguist neither", func(t *testing.T) {
		draftID := func(name string) int64 {
			rec := testutil.DoRequestWithRole(t, router, "POST", "/api/projects",
				map[string]any{"status": "draft", "name": name}, pm.ID, tenant.ID, "project_manager")
			testutil.AssertStatus(t, rec, http.StatusCreated)
			var created struct {
				Data struct {
					ID int64 `json:"id"`
				} `json:"data"`
			}
			testutil.DecodeJSON(t, rec, &created)
			return created.Data.ID
		}

		// A linguist gets nowhere near it — the route group is still manager.
		id := draftID("Linguist cannot delete")
		rec := testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/projects/%d", id), nil, linguist.ID, tenant.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusForbidden)

		// The PM may throw away the draft.
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/projects/%d", id), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusNoContent)
		rec = testutil.DoRequestWithRole(t, router, "GET",
			fmt.Sprintf("/api/projects/%d", id), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		// Publishing it puts it out of reach again — the carve-out is the
		// draft status, not the fact that a PM made it.
		live := draftID("Published then undeletable")
		rec = testutil.DoRequestWithRole(t, router, "PUT",
			fmt.Sprintf("/api/projects/%d/draft", live),
			map[string]any{"name": "Live", "source_language": "en", "target_languages": []string{"fr"}},
			pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusOK)
		rec = testutil.DoRequestWithRole(t, router, "POST",
			fmt.Sprintf("/api/projects/%d/publish", live), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusOK)
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/projects/%d", live), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		// The refusal leaves the project in place.
		rec = testutil.DoRequestWithRole(t, router, "GET",
			fmt.Sprintf("/api/projects/%d", live), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusOK)
	})

	t.Run("PM cannot delete TM (owner/admin only)", func(t *testing.T) {
		tm := testutil.SeedTM(t, q, tenant.ID, "DelTM")
		rec := testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/tms/%d", tm.ID), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/tms/%d", tm.ID), nil, admin.ID, tenant.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusNoContent)
	})

	t.Run("PM cannot delete term base (owner/admin only)", func(t *testing.T) {
		tb := testutil.SeedTermBase(t, q, tenant.ID, "DelTB", "en", "fr")
		rec := testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/terminology/%d", tb.ID), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
	})

	// --- Extensions: managers list, owner/admin upload and delete ---
	// Extension routes need storage, so this uses a router that has it.
	t.Run("extensions: manager lists, owner/admin upload + delete", func(t *testing.T) {
		extRouter := testutil.NewTestRouterWithExtensions(pool, file.NewMemoryStore(), nil)
		zip := string(exttest.Zip(t, nil))

		rec := testutil.DoRequestWithRole(t, extRouter, "GET", "/api/ai/extensions", nil, linguist.ID, tenant.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		rec = testutil.DoRequestWithRole(t, extRouter, "GET", "/api/ai/extensions", nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusOK)

		for _, who := range []struct {
			id   int64
			role string
		}{{linguist.ID, "linguist"}, {pm.ID, "project_manager"}} {
			rec = testutil.DoFileUploadWithRole(t, extRouter, "/api/ai/extensions", "mockqe.zip", zip, who.id, tenant.ID, who.role)
			testutil.AssertStatus(t, rec, http.StatusForbidden)
		}
		rec = testutil.DoFileUploadWithRole(t, extRouter, "/api/ai/extensions", "mockqe.zip", zip, admin.ID, tenant.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusCreated)

		rec = testutil.DoRequestWithRole(t, extRouter, "DELETE", "/api/ai/extensions/mockqe", nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		rec = testutil.DoRequestWithRole(t, extRouter, "DELETE", "/api/ai/extensions/mockqe", nil, admin.ID, tenant.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusNoContent)

		rec = testutil.DoFileUploadWithRole(t, extRouter, "/api/ai/extensions", "mockqe.zip", zip, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		rec = testutil.DoRequestWithRole(t, extRouter, "DELETE", "/api/ai/extensions/mockqe", nil, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusNoContent)
	})

	// --- PM CAN delete templates + MT engines ---
	t.Run("PM can delete template + MT engine", func(t *testing.T) {
		tmpl := testutil.SeedTemplate(t, q, tenant.ID, "DelTmpl")
		rec := testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/templates/%d", tmpl.ID), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusNoContent)

		engine := testutil.SeedMTEngine(t, q, tenant.ID, "DelEngine", "mock")
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/mt-engines/%d", engine.ID), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusNoContent)
	})

	// --- Labels: a manager creates, admin-or-above deletes ---
	t.Run("label create is manager, every removal is admin-or-above", func(t *testing.T) {
		// A PM fills in the project form, so a PM may add the label it needs.
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/labels",
			map[string]any{"name": "Country"}, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var created struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &created)

		// …and its values, which is the same day-to-day work.
		rec = testutil.DoRequestWithRole(t, router, "POST",
			fmt.Sprintf("/api/labels/%d/values", created.Data.ID),
			map[string]any{"value": "Germany"}, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusCreated)

		// Removing reaches every project that carried the value, so a PM
		// cannot retire one…
		var value struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		rec = testutil.DoRequestWithRole(t, router, "POST",
			fmt.Sprintf("/api/labels/%d/values", created.Data.ID),
			map[string]any{"value": "Japan"}, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		testutil.DecodeJSON(t, rec, &value)
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/labels/%d/values/%d", created.Data.ID, value.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/labels/%d/values/%d", created.Data.ID, value.Data.ID), nil, admin.ID, tenant.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusNoContent)

		// …nor the label itself. Admin and owner can.
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/labels/%d", created.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/labels/%d", created.Data.ID), nil, admin.ID, tenant.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusNoContent)
	})

	// --- Org notification rules: a manager reads, admin-or-above changes ---
	t.Run("org notification rules are changed by admin-or-above only", func(t *testing.T) {
		const path = "/api/org/notification-rules"
		assignmentOn := func() bool {
			t.Helper()
			rec := testutil.DoRequestWithRole(t, router, "GET", path, nil, pm.ID, tenant.ID, "project_manager")
			testutil.AssertStatus(t, rec, http.StatusOK)
			var rules struct {
				Data struct {
					NotifyAssignment bool `json:"notify_assignment"`
				} `json:"data"`
			}
			testutil.DecodeJSON(t, rec, &rules)
			return rules.Data.NotifyAssignment
		}
		off := map[string]any{"notify_assignment": false}

		// The rules switch notifications off for every member of the org.
		rec := testutil.DoRequestWithRole(t, router, "PUT", path, off, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		if !assignmentOn() {
			t.Fatal("a refused PUT switched assignment notifications off")
		}

		rec = testutil.DoRequestWithRole(t, router, "PUT", path, off, admin.ID, tenant.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusOK)
		if assignmentOn() {
			t.Fatal("an admin's PUT did not switch assignment notifications off")
		}
	})

	// --- Customer delete is owner-only ---
	t.Run("customer delete is owner-only", func(t *testing.T) {
		// Create a customer via the API as admin (allowed).
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/customers",
			map[string]any{"name": "Acme"}, admin.ID, tenant.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var created struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &created)

		// Admin + PM cannot delete; owner can.
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/customers/%d", created.Data.ID), nil, admin.ID, tenant.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/customers/%d", created.Data.ID), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		rec = testutil.DoRequestWithRole(t, router, "DELETE",
			fmt.Sprintf("/api/customers/%d", created.Data.ID), nil, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusNoContent)
	})

	// unused var guard (SeedFile import parity with other flow tests)
	_ = db.FileFormatJsonI18n
}
