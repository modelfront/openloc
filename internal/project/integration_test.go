package project_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/projectdelete"
	"openloc/internal/testutil"
)

func TestIntegrationProjectCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	// Create project
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Test Project",
		"source_language":  "en",
		"target_languages": []string{"fr", "de"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var createResp struct {
		Data struct {
			ID              int64    `json:"id"`
			Name            string   `json:"name"`
			SourceLanguage  string   `json:"source_language"`
			TargetLanguages []string `json:"target_languages"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &createResp)

	projectID := createResp.Data.ID
	if projectID == 0 {
		t.Fatal("expected non-zero project ID")
	}
	if createResp.Data.Name != "Test Project" {
		t.Errorf("expected name 'Test Project', got %q", createResp.Data.Name)
	}

	// List projects
	rec = testutil.DoRequest(t, router, "GET", "/api/projects", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var listResp struct {
		Data []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 1 {
		t.Fatalf("expected 1 project, got %d", len(listResp.Data))
	}

	// Get project
	rec = testutil.DoRequest(t, router, "GET", "/api/projects/"+itoa(projectID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Delete project
	rec = testutil.DoRequest(t, router, "DELETE", "/api/projects/"+itoa(projectID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// Verify deleted
	rec = testutil.DoRequest(t, router, "GET", "/api/projects", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 0 {
		t.Errorf("expected 0 projects after delete, got %d", len(listResp.Data))
	}
}

func TestIntegrationProjectLanguageNormalization(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	// Create project with mixed-case languages
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Lang Test",
		"source_language":  "EN",
		"target_languages": []string{"FR-FR", "pt_BR", "DE"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var resp struct {
		Data struct {
			ID              int64    `json:"id"`
			SourceLanguage  string   `json:"source_language"`
			TargetLanguages []string `json:"target_languages"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	if resp.Data.SourceLanguage != "en" {
		t.Errorf("expected source 'en', got %q", resp.Data.SourceLanguage)
	}

	// Check target languages are normalized
	expected := map[string]bool{"fr-fr": true, "pt-br": true, "de": true}
	for _, lang := range resp.Data.TargetLanguages {
		if !expected[lang] {
			t.Errorf("unexpected target language %q", lang)
		}
	}
}

func TestIntegrationProjectTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "Company A")
	userA := testutil.SeedUser(t, q, tenantA.ID, "a@test.com", "pass")

	tenantB := testutil.SeedTenant(t, q, "Company B")
	userB := testutil.SeedUser(t, q, tenantB.ID, "b@test.com", "pass")

	// Create project as tenant A
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "A's Project",
		"source_language":  "en",
		"target_languages": []string{"fr"},
	}, userA.ID, tenantA.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	// Tenant B should see zero projects
	rec = testutil.DoRequest(t, router, "GET", "/api/projects", nil, userB.ID, tenantB.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var listResp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 0 {
		t.Errorf("tenant B should see 0 projects, got %d", len(listResp.Data))
	}
}

func TestIntegrationProjectValidation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ValCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "val@test.com", "pass")

	// 12.2: source == target should be rejected
	t.Run("rejects source equals target", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
			"name":             "Bad Project",
			"source_language":  "en",
			"target_languages": []string{"en"},
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
	})

	// 12.3: duplicate target languages should be rejected
	t.Run("rejects duplicate targets", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
			"name":             "Dup Project",
			"source_language":  "en",
			"target_languages": []string{"fr", "fr"},
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
	})

	// 12.2 variant: source == target after normalization
	t.Run("rejects source equals target after normalization", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
			"name":             "Case Project",
			"source_language":  "en",
			"target_languages": []string{"EN"},
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
	})

	// Valid project should still work
	t.Run("accepts valid project", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
			"name":             "Good Project",
			"source_language":  "en",
			"target_languages": []string{"fr", "de"},
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
	})
}

func itoa(n int64) string {
	return fmt.Sprintf("%d", n)
}

// TestIntegrationProjectDelete_RollsBackOnSettingsProfileFKViolation pins that
// the purge's last step deletes the project row and its settings profile in
// one transaction: when the profile delete fails, the row stays (still marked
// deleted and hidden) with its profile, and the next purge removes both.
//
// FK injection: a template pinned to the project's settings profile. Templates
// do not cascade from a project, so DeleteSettingsProfile fails with 23503
// (templates.settings_profile_id has NO ACTION).
func TestIntegrationProjectDelete_RollsBackOnSettingsProfileFKViolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "RollbackCorp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "rollback@test.com", "pass", "admin")

	// Step 1: create the project via API (which creates a fresh settings_profile).
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Rollback Project",
		"source_language":  "en",
		"target_languages": []string{"es"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct {
		Data struct {
			ID                int64 `json:"id"`
			SettingsProfileID int64 `json:"settings_profile_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &created)
	projectID := created.Data.ID
	settingsProfileID := created.Data.SettingsProfileID

	// Step 2: pin a template to the same settings_profile. Templates do NOT
	// cascade-delete on project delete, so this row will still FK to the
	// settings_profile when the handler tries to delete it — forcing the
	// `DeleteSettingsProfile` step to fail with 23503 FK violation.
	tag, err := pool.Exec(ctx,
		`INSERT INTO templates (name, tenant_id, settings_profile_id) VALUES ($1, $2, $3)`,
		"Rollback Pinning Template", tenant.ID, settingsProfileID,
	)
	if err != nil {
		t.Fatalf("seed pinning template: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 template row, got %d", tag.RowsAffected())
	}

	// Step 3: delete. Without a worker queue the purge runs in the request
	// and fails on its last step.
	rec = testutil.DoRequest(t, router, "DELETE",
		fmt.Sprintf("/api/projects/%d", projectID), nil, user.ID, tenant.ID)
	if rec.Code < 500 || rec.Code >= 600 {
		t.Fatalf("expected 5xx (settings_profile delete blocked by FK), got %d body=%s", rec.Code, rec.Body.String())
	}

	// Step 4: the row and its profile are both still there; the project stays
	// marked deleted, so no read shows it.
	rows := func() (projects, profiles int64) {
		return testutil.CountRows(t, pool, `SELECT count(*) FROM projects WHERE id = $1 AND deleted_at IS NOT NULL`, projectID),
			testutil.CountRows(t, pool, `SELECT count(*) FROM settings_profiles WHERE id = $1`, settingsProfileID)
	}
	if p, sp := rows(); p != 1 || sp != 1 {
		t.Fatalf("after the failed purge: deleted project rows %d, profiles %d; want 1, 1", p, sp)
	}
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d", projectID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// Step 5: without the pinning template the next purge removes both.
	if _, err := pool.Exec(ctx, `DELETE FROM templates WHERE settings_profile_id = $1`, settingsProfileID); err != nil {
		t.Fatalf("remove pinning template: %v", err)
	}
	if err := projectdelete.New(pool, nil).Purge(ctx, tenant.ID, projectID); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if p, sp := rows(); p != 0 || sp != 0 {
		t.Fatalf("after the purge: project rows %d, profiles %d; want 0, 0", p, sp)
	}
}
