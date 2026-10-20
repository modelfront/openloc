// TM profile CRUD + tenant isolation + role gating + delete guard.
package tmprofile_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type profileResp struct {
	Data struct {
		ID                    int64   `json:"id"`
		TenantID              int64   `json:"tenant_id"`
		SettingsProfileID     int64   `json:"settings_profile_id"`
		Name                  string  `json:"name"`
		Description           string  `json:"description"`
		FuzzyThreshold        float32 `json:"fuzzy_threshold"`
		SublanguagePenaltyPct int32   `json:"sublanguage_penalty_pct"`
		NearExactPenaltyPct   int32   `json:"near_exact_penalty_pct"`
		TmCount               int32   `json:"tm_count"`
		UsageCount            int32   `json:"usage_count"`
		TMAssignments         []struct {
			TmID       int64  `json:"tm_id"`
			Mode       string `json:"mode"`
			Priority   int32  `json:"priority"`
			PenaltyPct int32  `json:"penalty_pct"`
		} `json:"tm_assignments"`
	} `json:"data"`
}

func TestIntegrationTMProfileCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ProfCorp")
	admin := testutil.SeedUserWithRole(t, q, tenant.ID, "admin@test.com", "pass", "admin")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@test.com", "pass", "project_manager")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@test.com", "pass", "linguist")
	tm := testutil.SeedTM(t, q, tenant.ID, "Main TM")

	// Linguists have no TM management surface.
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/tm-profiles", nil, linguist.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/tm-profiles", map[string]any{"name": "x"}, linguist.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// PM creates with scoring + one TM row in a single call.
	thr := float32(0.6)
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/tm-profiles", map[string]any{
		"name": "  Customer X  ", "description": "Legal", "fuzzy_threshold": thr,
		"sublanguage_penalty_pct": 3, "near_exact_penalty_pct": 250, // clamped to 100
		"tms": []map[string]any{{"tm_id": tm.ID, "mode": "read_write", "priority": 1, "penalty_pct": 5}},
	}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created profileResp
	testutil.DecodeJSON(t, rec, &created)
	if created.Data.Name != "Customer X" || created.Data.Description != "Legal" {
		t.Errorf("name/description = %q/%q", created.Data.Name, created.Data.Description)
	}
	if created.Data.FuzzyThreshold != thr || created.Data.SublanguagePenaltyPct != 3 || created.Data.NearExactPenaltyPct != 100 {
		t.Errorf("scoring = %v/%d/%d, want 0.6/3/100", created.Data.FuzzyThreshold, created.Data.SublanguagePenaltyPct, created.Data.NearExactPenaltyPct)
	}
	if created.Data.TmCount != 1 || len(created.Data.TMAssignments) != 1 || created.Data.TMAssignments[0].PenaltyPct != 5 {
		t.Errorf("tm rows = %+v (count %d), want one row with penalty 5", created.Data.TMAssignments, created.Data.TmCount)
	}
	id := created.Data.ID

	// Duplicate name, case-insensitive → 409.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/tm-profiles", map[string]any{"name": "customer x"}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// Bad threshold → 400, and nothing was created.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/tm-profiles", map[string]any{"name": "Bad", "fuzzy_threshold": 1.5}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/tm-profiles", map[string]any{"name": "   "}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// List.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/tm-profiles", nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 1 || list.Data[0].ID != id {
		t.Fatalf("list = %+v, want just %d", list.Data, id)
	}

	// PATCH name + threshold; absent penalties keep their values.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/tm-profiles/%d", id), map[string]any{
		"name": "Customer Y", "fuzzy_threshold": 0.9,
	}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var patched profileResp
	testutil.DecodeJSON(t, rec, &patched)
	if patched.Data.Name != "Customer Y" || patched.Data.FuzzyThreshold != 0.9 || patched.Data.SublanguagePenaltyPct != 3 {
		t.Errorf("after patch: %q %v %d", patched.Data.Name, patched.Data.FuzzyThreshold, patched.Data.SublanguagePenaltyPct)
	}
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/tm-profiles/%d", id), map[string]any{"fuzzy_threshold": -0.1}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// PUT tms: replace rows; a foreign TM is rejected and the list is untouched.
	other := testutil.SeedTenant(t, q, "Other")
	foreignTM := testutil.SeedTM(t, q, other.ID, "Foreign")
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/tm-profiles/%d/tms", id), map[string]any{
		"assignments": []map[string]any{{"tm_id": foreignTM.ID, "mode": "read", "priority": 1}},
	}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	rows, err := q.ListSettingsTMAssignments(ctx, created.Data.SettingsProfileID)
	if err != nil || len(rows) != 1 || rows[0].TmID != tm.ID {
		t.Fatalf("rejected PUT must leave rows intact, got %+v err=%v", rows, err)
	}
	rec = testutil.DoRequestWithRole(t, router, "PUT", fmt.Sprintf("/api/tm-profiles/%d/tms", id), map[string]any{
		"assignments": []map[string]any{{"tm_id": tm.ID, "mode": "read", "priority": 2, "penalty_pct": 9}},
	}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/tm-profiles/%d", id), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got profileResp
	testutil.DecodeJSON(t, rec, &got)
	if len(got.Data.TMAssignments) != 1 || got.Data.TMAssignments[0].Mode != "read" || got.Data.TMAssignments[0].PenaltyPct != 9 {
		t.Errorf("GET tm rows = %+v, want read/9", got.Data.TMAssignments)
	}

	// Delete: PM forbidden; admin blocked while a project follows the profile.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/tm-profiles/%d", id), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	project := testutil.SeedProject(t, q, tenant.ID, "Follower", "en", []string{"fr"})
	testutil.LinkTMProfile(t, q, project.SettingsProfileID, id)
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/tm-profiles/%d", id), nil, admin.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	var conflict struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	testutil.DecodeJSON(t, rec, &conflict)
	if conflict.Error.Code != "tm_profile_in_use" || !strings.Contains(conflict.Error.Message, "1 project(s)") {
		t.Errorf("409 body = %+v, want tm_profile_in_use naming 1 project", conflict)
	}
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/tm-profiles/%d", id), nil, pm.ID, tenant.ID, "project_manager")
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.UsageCount != 1 {
		t.Errorf("usage_count = %d, want 1", got.Data.UsageCount)
	}

	// Unlink, then delete succeeds and the profile disappears from list + GET.
	if _, err := q.SetSettingsProfileTMProfile(ctx, db.SetSettingsProfileTMProfileParams{ID: project.SettingsProfileID}); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/tm-profiles/%d", id), nil, admin.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/tm-profiles/%d", id), nil, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/tm-profiles", nil, pm.ID, tenant.ID, "project_manager")
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 0 {
		t.Errorf("soft-deleted profile still listed: %+v", list.Data)
	}
	// Soft: the row is still there.
	var deletedAt *string
	if err := pool.QueryRow(ctx, `SELECT deleted_at::text FROM tm_profiles WHERE id = $1`, id).Scan(&deletedAt); err != nil || deletedAt == nil {
		t.Errorf("expected a soft-deleted row, got deleted_at=%v err=%v", deletedAt, err)
	}
	// The name is free again after the soft delete.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/tm-profiles", map[string]any{"name": "Customer Y"}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusCreated)
}

func TestIntegrationTMProfileTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	a := testutil.SeedTenant(t, q, "A")
	b := testutil.SeedTenant(t, q, "B")
	adminA := testutil.SeedUserWithRole(t, q, a.ID, "a@test.com", "pass", "admin")
	adminB := testutil.SeedUserWithRole(t, q, b.ID, "b@test.com", "pass", "admin")
	tp := testutil.SeedTMProfile(t, q, a.ID, "A's profile", 0.75)
	tmB := testutil.SeedTM(t, q, b.ID, "B TM")
	path := fmt.Sprintf("/api/tm-profiles/%d", tp.ID)

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", path, nil},
		{"PATCH", path, map[string]any{"name": "stolen"}},
		{"PUT", path + "/tms", map[string]any{"assignments": []map[string]any{{"tm_id": tmB.ID, "mode": "read", "priority": 1}}}},
		{"DELETE", path, nil},
	} {
		rec := testutil.DoRequestWithRole(t, router, c.method, c.path, c.body, adminB.ID, b.ID, "admin")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	}
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/tm-profiles", nil, adminB.ID, b.ID, "admin")
	var list struct {
		Data []any `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 0 {
		t.Errorf("tenant B sees tenant A's profiles: %+v", list.Data)
	}
	// Same-name profiles in different tenants are fine.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/tm-profiles", map[string]any{"name": "A's profile"}, adminB.ID, b.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	rec = testutil.DoRequestWithRole(t, router, "GET", path, nil, adminA.ID, a.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// In-place edits from a project page carry `max_followers`: once another
// project or template follows the profile, the edit is refused so a setup
// someone else relies on is not changed from a page that thought it was alone.
func TestIntegrationTMProfileMaxFollowersGuard(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "GuardCorp")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@test.com", "pass", "project_manager")
	tm := testutil.SeedTM(t, q, tenant.ID, "TM")
	tp := testutil.SeedTMProfile(t, q, tenant.ID, "Solo", 0.75)
	do := func(method, path string, body any) *httptest.ResponseRecorder {
		return testutil.DoRequestWithRole(t, router, method, path, body, pm.ID, tenant.ID, "project_manager")
	}
	patch := fmt.Sprintf("/api/tm-profiles/%d?max_followers=0", tp.ID)
	put := fmt.Sprintf("/api/tm-profiles/%d/tms?max_followers=1", tp.ID)
	rows := map[string]any{"assignments": []map[string]any{{"tm_id": tm.ID, "mode": "read", "priority": 0}}}

	// Nobody follows it: both edits go through.
	testutil.AssertStatus(t, do("PATCH", patch, map[string]any{"fuzzy_threshold": 0.6}), http.StatusOK)
	testutil.AssertStatus(t, do("PUT", put, rows), http.StatusOK)

	// One project follows: the wizard's condition (0) fails, a project page's (1) still holds.
	p1 := testutil.SeedProject(t, q, tenant.ID, "P1", "en", []string{"fr"})
	testutil.LinkTMProfile(t, q, p1.SettingsProfileID, tp.ID)
	rec := do("PATCH", patch, map[string]any{"fuzzy_threshold": 0.5})
	testutil.AssertStatus(t, rec, http.StatusConflict)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	testutil.DecodeJSON(t, rec, &body)
	if body.Error.Code != "tm_profile_shared" {
		t.Errorf("code = %q, want tm_profile_shared", body.Error.Code)
	}
	testutil.AssertStatus(t, do("PUT", put, rows), http.StatusOK)

	// A second follower: the project page's condition fails too; the refused
	// edit must not have touched the rows.
	p2 := testutil.SeedProject(t, q, tenant.ID, "P2", "en", []string{"fr"})
	testutil.LinkTMProfile(t, q, p2.SettingsProfileID, tp.ID)
	testutil.AssertStatus(t, do("PUT", put, map[string]any{"assignments": []map[string]any{}}), http.StatusConflict)
	if got, _ := q.ListSettingsTMAssignments(context.Background(), tp.SettingsProfileID); len(got) != 1 {
		t.Errorf("refused edit changed the rows: %d rows, want 1", len(got))
	}
	// Without the parameter (the profile's own page) the edit still applies.
	testutil.AssertStatus(t, do("PATCH", fmt.Sprintf("/api/tm-profiles/%d", tp.ID), map[string]any{"fuzzy_threshold": 0.5}), http.StatusOK)
	testutil.AssertStatus(t, do("PATCH", fmt.Sprintf("/api/tm-profiles/%d?max_followers=x", tp.ID), map[string]any{}), http.StatusBadRequest)
}

// The wizard saves its form as a real `status='draft'` project,
// so the moment a PM creates a TM profile from that form
// the draft follows it. The edit guard must not count that: the wizard sends
// max_followers=0, and counting the user's own unsaved form refuses the very
// next threshold change with "followed by 1 project(s); edit it on its own
// page" — about a project they cannot see.
//
// Anti-tautology: drop `AND p.status <> 'draft'` from
// CountTMProfileFollowers and the first PATCH flips to 409.
func TestIntegrationTMProfileGuardIgnoresDrafts(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DraftGuardCo")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@draftguard.test", "pass", "project_manager")
	tp := testutil.SeedTMProfile(t, q, tenant.ID, "FromWizard", 0.62)
	patch := fmt.Sprintf("/api/tm-profiles/%d?max_followers=0", tp.ID)
	do := func(body any) *httptest.ResponseRecorder {
		return testutil.DoRequestWithRole(t, router, "PATCH", patch, body, pm.ID, tenant.ID, "project_manager")
	}

	// The wizard's own draft follows the profile it just created.
	draft := testutil.SeedProject(t, q, tenant.ID, "Untitled draft", "en", []string{"es"})
	if _, err := pool.Exec(ctx, `UPDATE projects SET status = 'draft' WHERE id = $1`, draft.ID); err != nil {
		t.Fatalf("make draft: %v", err)
	}
	testutil.LinkTMProfile(t, q, draft.SettingsProfileID, tp.ID)

	testutil.AssertStatus(t, do(map[string]any{"fuzzy_threshold": 0.7}), http.StatusOK)

	// A published project is a real follower, and still refuses the edit.
	live := testutil.SeedProject(t, q, tenant.ID, "Live", "en", []string{"es"})
	testutil.LinkTMProfile(t, q, live.SettingsProfileID, tp.ID)
	testutil.AssertStatus(t, do(map[string]any{"fuzzy_threshold": 0.5}), http.StatusConflict)
}
