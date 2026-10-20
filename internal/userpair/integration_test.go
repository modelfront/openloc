// User language pair CRUD + tenant isolation + role gating + normalization.
// Mirrors invite/customer integration test patterns.
package userpair_test

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

type pairDTO struct {
	SourceLang string `json:"source_lang"`
	TargetLang string `json:"target_lang"`
}

type pairListResp struct {
	Data []pairDTO `json:"data"`
}

type userListResp struct {
	Data []struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	} `json:"data"`
}

func TestIntegrationUserPairReplaceAndList(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")
	member := testutil.SeedUserWithRole(t, q, tenant.ID, "member@test.com", "pass", "project_manager")

	// Owner sets pairs for member
	rec := testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/users/%d/language-pairs", member.ID),
		map[string]any{
			"pairs": []pairDTO{
				{SourceLang: "en", TargetLang: "fr"},
				{SourceLang: "en", TargetLang: "de"},
			},
		}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var saved pairListResp
	testutil.DecodeJSON(t, rec, &saved)
	if len(saved.Data) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(saved.Data))
	}

	// Member can list their own pairs
	rec = testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/users/%d/language-pairs", member.ID),
		nil, member.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var listed pairListResp
	testutil.DecodeJSON(t, rec, &listed)
	if len(listed.Data) != 2 {
		t.Errorf("expected 2 pairs from list, got %d", len(listed.Data))
	}

	// Member CANNOT replace their own pairs (admin-only PUT)
	rec = testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/users/%d/language-pairs", member.ID),
		map[string]any{"pairs": []pairDTO{{SourceLang: "es", TargetLang: "pt"}}},
		member.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Replace semantics: PUT with new set REPLACES old set (not merges)
	rec = testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/users/%d/language-pairs", member.ID),
		map[string]any{
			"pairs": []pairDTO{{SourceLang: "es", TargetLang: "pt"}},
		}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/users/%d/language-pairs", member.ID),
		nil, member.ID, tenant.ID, "project_manager")
	testutil.DecodeJSON(t, rec, &listed)
	if len(listed.Data) != 1 || listed.Data[0].SourceLang != "es" || listed.Data[0].TargetLang != "pt" {
		t.Errorf("replace semantics broken: got %+v", listed.Data)
	}

	// Empty list clears all pairs
	rec = testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/users/%d/language-pairs", member.ID),
		map[string]any{"pairs": []pairDTO{}},
		owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/users/%d/language-pairs", member.ID),
		nil, member.ID, tenant.ID, "project_manager")
	testutil.DecodeJSON(t, rec, &listed)
	if len(listed.Data) != 0 {
		t.Errorf("expected empty pairs after clear, got %d", len(listed.Data))
	}
}

func TestIntegrationUserPairValidation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")
	member := testutil.SeedUserWithRole(t, q, tenant.ID, "member@test.com", "pass", "project_manager")

	// source == target → 400
	rec := testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/users/%d/language-pairs", member.ID),
		map[string]any{
			"pairs": []pairDTO{{SourceLang: "en", TargetLang: "en"}},
		}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Empty source → 400
	rec = testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/users/%d/language-pairs", member.ID),
		map[string]any{
			"pairs": []pairDTO{{SourceLang: "", TargetLang: "fr"}},
		}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// PUT for nonexistent user → 404
	rec = testutil.DoRequestWithRole(t, router, "PUT",
		"/api/users/9999999/language-pairs",
		map[string]any{"pairs": []pairDTO{{SourceLang: "en", TargetLang: "fr"}}},
		owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

func TestIntegrationUserPairNormalization(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")
	member := testutil.SeedUserWithRole(t, q, tenant.ID, "member@test.com", "pass", "project_manager")

	// PUT with uppercase / locale codes — server normalizes via langcode.Normalize.
	// Normalization is FULL lowercase including the region subtag (matches the rest
	// of the codebase, e.g. fr-fr, pt-br, zh-hans). The locale STRUCTURE is preserved
	// (region tag stays) but case is canonicalized.
	rec := testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/users/%d/language-pairs", member.ID),
		map[string]any{
			"pairs": []pairDTO{
				{SourceLang: "EN", TargetLang: "FR"},       // bare codes lowercase
				{SourceLang: "en-US", TargetLang: "es-MX"}, // locales lowercase but structure preserved
			},
		}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/users/%d/language-pairs", member.ID),
		nil, member.ID, tenant.ID, "project_manager")
	var listed pairListResp
	testutil.DecodeJSON(t, rec, &listed)
	if len(listed.Data) != 2 {
		t.Fatalf("expected 2 pairs after normalization, got %d", len(listed.Data))
	}
	// Confirm normalized canonical lowercase forms.
	foundLowercase := false
	foundLocale := false
	for _, p := range listed.Data {
		if p.SourceLang == "en" && p.TargetLang == "fr" {
			foundLowercase = true
		}
		if p.SourceLang == "en-us" && p.TargetLang == "es-mx" {
			foundLocale = true
		}
	}
	if !foundLowercase {
		t.Errorf("expected normalized en/fr pair, got %+v", listed.Data)
	}
	if !foundLocale {
		t.Errorf("expected locale en-us/es-mx (lowercase canonical), got %+v", listed.Data)
	}
}

// ListUsersForPair returns only users in the caller's tenant qualified for the pair.
func TestIntegrationListUsersForPair(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	tenantB := testutil.SeedTenant(t, q, "TenantB")
	ownerA := testutil.SeedUserWithRole(t, q, tenantA.ID, "ownerA@test.com", "pass", "owner")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "ownerB@test.com", "pass", "owner")

	// Tenant A: 2 members, only one qualified for en→fr
	memberA1 := testutil.SeedUserWithRole(t, q, tenantA.ID, "memberA1@test.com", "pass", "project_manager")
	memberA2 := testutil.SeedUserWithRole(t, q, tenantA.ID, "memberA2@test.com", "pass", "project_manager")

	// Tenant B: 1 member, ALSO has en→fr pair (cross-tenant data)
	memberB1 := testutil.SeedUserWithRole(t, q, tenantB.ID, "memberB1@test.com", "pass", "project_manager")

	// Set pairs for memberA1 and memberB1 (both en→fr); memberA2 has nothing
	for _, setup := range []struct {
		callerID int64
		tenantID int64
		userID   int64
	}{
		{ownerA.ID, tenantA.ID, memberA1.ID},
		{ownerB.ID, tenantB.ID, memberB1.ID},
	} {
		rec := testutil.DoRequestWithRole(t, router, "PUT",
			fmt.Sprintf("/api/users/%d/language-pairs", setup.userID),
			map[string]any{
				"pairs": []pairDTO{{SourceLang: "en", TargetLang: "fr"}},
			}, setup.callerID, setup.tenantID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
	}

	// Owner A asks for users qualified for en→fr — should get only memberA1
	rec := testutil.DoRequestWithRole(t, router, "GET",
		"/api/users/by-language-pair?source=en&target=fr",
		nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var users userListResp
	testutil.DecodeJSON(t, rec, &users)
	if len(users.Data) != 1 {
		t.Fatalf("expected 1 user qualified in tenant A, got %d (cross-tenant leak?)", len(users.Data))
	}
	if users.Data[0].ID != memberA1.ID {
		t.Errorf("expected memberA1, got user ID %d", users.Data[0].ID)
	}

	// memberA2 should NOT appear (no pairs)
	for _, u := range users.Data {
		if u.ID == memberA2.ID {
			t.Error("memberA2 has no pairs but appeared in qualified list")
		}
	}

	// Owner A asks for missing pair → empty list
	rec = testutil.DoRequestWithRole(t, router, "GET",
		"/api/users/by-language-pair?source=zh&target=ja",
		nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &users)
	if len(users.Data) != 0 {
		t.Errorf("expected 0 qualified users for unrequested pair, got %d", len(users.Data))
	}

	// Missing source param → 400
	rec = testutil.DoRequestWithRole(t, router, "GET",
		"/api/users/by-language-pair?target=fr",
		nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Missing target param → 400 (exercises the second clause of the OR guard)
	rec = testutil.DoRequestWithRole(t, router, "GET",
		"/api/users/by-language-pair?source=en",
		nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Both params omitted → 400 (must not silently return all users)
	rec = testutil.DoRequestWithRole(t, router, "GET",
		"/api/users/by-language-pair",
		nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

// Cross-tenant attack: admin in tenant A tries to PUT pairs for a user in
// tenant B. Server returns 404 (not 403) because tenant isolation hides
// existence.
func TestIntegrationUserPairCrossTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	tenantB := testutil.SeedTenant(t, q, "TenantB")
	ownerA := testutil.SeedUserWithRole(t, q, tenantA.ID, "ownerA@test.com", "pass", "owner")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "ownerB@test.com", "pass", "owner")
	memberB := testutil.SeedUserWithRole(t, q, tenantB.ID, "memberB@test.com", "pass", "project_manager")

	// Owner B gives memberB pairs, so a leaking GET has something to leak.
	rec := testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/users/%d/language-pairs", memberB.ID),
		map[string]any{"pairs": []pairDTO{
			{SourceLang: "en", TargetLang: "de"},
			{SourceLang: "en", TargetLang: "es"},
		}},
		ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Owner A tries to PUT pairs for memberB — must 404
	rec = testutil.DoRequestWithRole(t, router, "PUT",
		fmt.Sprintf("/api/users/%d/language-pairs", memberB.ID),
		map[string]any{"pairs": []pairDTO{{SourceLang: "en", TargetLang: "fr"}}},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// Owner A tries to GET memberB's pairs — must return empty (not memberB's data)
	rec = testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/users/%d/language-pairs", memberB.ID),
		nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listed pairListResp
	testutil.DecodeJSON(t, rec, &listed)
	if len(listed.Data) != 0 {
		t.Errorf("cross-tenant GET leaked data: %+v", listed.Data)
	}

	// Owner B still sees exactly the pairs it set: A's refused PUT changed nothing.
	rec = testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/users/%d/language-pairs", memberB.ID),
		nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var own pairListResp
	testutil.DecodeJSON(t, rec, &own)
	got := map[pairDTO]bool{}
	for _, p := range own.Data {
		got[p] = true
	}
	if len(own.Data) != 2 || !got[pairDTO{"en", "de"}] || !got[pairDTO{"en", "es"}] {
		t.Errorf("tenant B's own GET = %+v, want exactly en→de and en→es", own.Data)
	}
}
