// PATCH /api/projects/{id}/settings's `qa_profile_id`
// field is tri-state. Each scenario below corresponds to a distinct user-visible
// behavior — if any of these regress, the QA Profile dropdown in the UI breaks
// in subtle ways (silent no-op, wrong assignment, accepting garbage, etc.).
//
// Skipped with -short.
package project_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestQAProfileIDTriState(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "QAProfileTriState")
	user := testutil.SeedUser(t, q, tenant.ID, "u@qatristate.test", "pass")
	ctx := testutil.Ctx()

	// Create a project
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "TriState Project", "source_language": "en", "target_languages": []string{"fr"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var createResp struct {
		Data struct{ ID int64 } `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &createResp)
	projectID := createResp.Data.ID

	// Create two QA profiles so we can swap and verify
	prof1, err := q.CreateQAProfile(ctx, db.CreateQAProfileParams{
		TenantID: tenant.ID, Name: "Profile A",
	})
	if err != nil {
		t.Fatalf("create profile A: %v", err)
	}
	prof2, err := q.CreateQAProfile(ctx, db.CreateQAProfileParams{
		TenantID: tenant.ID, Name: "Profile B",
	})
	if err != nil {
		t.Fatalf("create profile B: %v", err)
	}

	// Helper: PATCH settings with arbitrary raw JSON body, return status
	patchRaw := func(t *testing.T, body string) int {
		t.Helper()
		req := httptest.NewRequest("PATCH", "/api/projects/"+itoa(projectID)+"/settings",
			bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", testutil.AuthHeader(user.ID, tenant.ID))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr.Code
	}

	// Helper: get current qa_profile_id
	getQAProfileID := func(t *testing.T) *int64 {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", "/api/projects/"+itoa(projectID)+"/settings",
			nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Project struct {
					QAProfileID *int64 `json:"qa_profile_id"`
				} `json:"project"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data.Project.QAProfileID
	}

	// ── Initial state: no QA profile assigned ──
	if id := getQAProfileID(t); id != nil {
		t.Fatalf("initial qa_profile_id should be nil, got %v", *id)
	}

	// ── Set to profile A: should succeed ──
	if code := patchRaw(t, `{"qa_profile_id":`+itoa(prof1.ID)+`}`); code != http.StatusOK {
		t.Fatalf("set to A: expected 200, got %d", code)
	}
	if id := getQAProfileID(t); id == nil || *id != prof1.ID {
		t.Fatalf("after set A: expected %d, got %v", prof1.ID, id)
	}

	// ── Field absent: should be no-op (keep A) ──
	if code := patchRaw(t, `{"fuzzy_threshold":0.8}`); code != http.StatusOK {
		t.Fatalf("absent field: expected 200, got %d", code)
	}
	if id := getQAProfileID(t); id == nil || *id != prof1.ID {
		t.Fatalf("after absent field: expected %d (kept), got %v", prof1.ID, id)
	}

	// ── Switch to B ──
	if code := patchRaw(t, `{"qa_profile_id":`+itoa(prof2.ID)+`}`); code != http.StatusOK {
		t.Fatalf("set to B: expected 200, got %d", code)
	}
	if id := getQAProfileID(t); id == nil || *id != prof2.ID {
		t.Fatalf("after set B: expected %d, got %v", prof2.ID, id)
	}

	// ── null clears (REST convention) ──
	if code := patchRaw(t, `{"qa_profile_id":null}`); code != http.StatusOK {
		t.Fatalf("null clear: expected 200, got %d", code)
	}
	if id := getQAProfileID(t); id != nil {
		t.Fatalf("null SHOULD clear qa_profile_id (REST convention). Got %v.", *id)
	}

	// ── Set back to B then test 0 (legacy clear) ──
	patchRaw(t, `{"qa_profile_id":`+itoa(prof2.ID)+`}`)
	if code := patchRaw(t, `{"qa_profile_id":0}`); code != http.StatusOK {
		t.Fatalf("legacy 0 clear: expected 200, got %d", code)
	}
	if id := getQAProfileID(t); id != nil {
		t.Fatalf("legacy 0 SHOULD clear qa_profile_id (rolling-deploy compat). Got %v.", *id)
	}

	// ── Bad value: negative ──
	if code := patchRaw(t, `{"qa_profile_id":-5}`); code != http.StatusBadRequest {
		t.Fatalf("negative qa_profile_id: expected 400, got %d", code)
	}

	// ── Bad value: string ──
	if code := patchRaw(t, `{"qa_profile_id":"abc"}`); code != http.StatusBadRequest {
		t.Fatalf("string qa_profile_id: expected 400, got %d", code)
	}

	// ── Bad value: float (e.g. 3.5) ──
	if code := patchRaw(t, `{"qa_profile_id":3.5}`); code != http.StatusBadRequest {
		t.Fatalf("float qa_profile_id: expected 400, got %d", code)
	}

	// ── Bad value: object ──
	if code := patchRaw(t, `{"qa_profile_id":{"not":"valid"}}`); code != http.StatusBadRequest {
		t.Fatalf("object qa_profile_id: expected 400, got %d", code)
	}

	// ── Bad value: array ──
	if code := patchRaw(t, `{"qa_profile_id":[1,2,3]}`); code != http.StatusBadRequest {
		t.Fatalf("array qa_profile_id: expected 400, got %d", code)
	}

	// ── After all the bad attempts, state must still be cleared ──
	if id := getQAProfileID(t); id != nil {
		t.Fatalf("after bad-value attempts: state must be unchanged (cleared), got %v", *id)
	}
}

// (uses itoa() from integration_test.go in the same package)
