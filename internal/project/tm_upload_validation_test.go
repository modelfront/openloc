// All four combinations of (mt_on_upload, tm_on_upload) are valid at the
// schema and API level. The UI cascades discourage some combos but
// the backend permits them so future workflow features can use them without a
// schema migration.
//
// What this file pins:
//  1. Every combo round-trips: PATCH writes it, GET reads back the same
//     values. A handler that silently dropped one of the fields (e.g. forgot
//     to thread `tm_on_upload` through to UpdateSettingsProfileParams) would
//     still return 200 but fail the GET assertion.
//  2. Partial PATCH preserves untouched fields. A bug where a partial PATCH
//     zeros out absent fields would surface here in the partial-flip sub-test.
//
// Anti-tautology RED proofs:
//   - Add an `if tmOnUpload && !mtOnUpload { 400 }` guard in
//     handler_settings.go::HandleUpdateSettings → the `(tm=true, mt=false)`
//     row flips 200 → 400 in the round-trip table.
//   - In handler_settings.go, change the partial-PATCH branch so that an
//     absent field defaults to false instead of preserving current state →
//     the partial-PATCH sub-test catches it.
package project_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"openloc/internal/testutil"
)

// settingsView captures only the two flags this file is responsible for.
// The GET response wraps in {"data":{"project":{...}}}; we don't care about
// the rest of the payload for these assertions.
type settingsView struct {
	MtOnUpload bool `json:"mt_on_upload"`
	TmOnUpload bool `json:"tm_on_upload"`
}

func TestProjectSettingsRoundTripsAllUploadCombos(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TmMtRoundTrip")
	user := testutil.SeedUser(t, q, tenant.ID, "u@roundtrip.test", "pass")

	// Each combo gets its OWN project so a leak from one PATCH cannot make
	// the next assertion pass for the wrong reason.
	createProject := func(t *testing.T, name string) int64 {
		t.Helper()
		rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
			"name": name, "source_language": "en", "target_languages": []string{"fr"},
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var resp struct {
			Data struct{ ID int64 } `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data.ID
	}

	patchSettings := func(t *testing.T, projectID int64, body map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("PATCH",
			"/api/projects/"+strconv.FormatInt(projectID, 10)+"/settings",
			bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", testutil.AuthHeader(user.ID, tenant.ID))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("PATCH %v: want 200, got %d body=%s", body, rr.Code, rr.Body.String())
		}
	}

	getSettings := func(t *testing.T, projectID int64) settingsView {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET",
			"/api/projects/"+strconv.FormatInt(projectID, 10)+"/settings",
			nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Project settingsView `json:"project"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data.Project
	}

	// All 4 combos. Each is independently PATCHed-then-GETted so a per-field
	// drop bug flips its specific row from green to red.
	combos := []struct {
		mt bool
		tm bool
	}{
		{false, false},
		{true, false},
		{false, true},
		{true, true},
	}
	for _, c := range combos {
		name := fmt.Sprintf("mt=%v_tm=%v", c.mt, c.tm)
		t.Run(name, func(t *testing.T) {
			pid := createProject(t, "P-"+name)
			patchSettings(t, pid, map[string]any{
				"mt_on_upload": c.mt,
				"tm_on_upload": c.tm,
			})
			got := getSettings(t, pid)
			if got.MtOnUpload != c.mt {
				t.Errorf("mt_on_upload: want %v, got %v", c.mt, got.MtOnUpload)
			}
			if got.TmOnUpload != c.tm {
				t.Errorf("tm_on_upload: want %v, got %v", c.tm, got.TmOnUpload)
			}
		})
	}

	// Partial PATCH: flip ONE flag at a time, assert the other preserves its
	// seeded value. This is the contract a "PATCH that silently zeros absent
	// fields" bug would violate.
	t.Run("partial PATCH preserves untouched fields", func(t *testing.T) {
		pid := createProject(t, "P-partial")
		patchSettings(t, pid, map[string]any{
			"mt_on_upload": true,
			"tm_on_upload": true,
		})
		if v := getSettings(t, pid); !(v.MtOnUpload && v.TmOnUpload) {
			t.Fatalf("seed: want all true, got %+v", v)
		}

		flips := []struct {
			body   map[string]any
			expect settingsView
		}{
			{map[string]any{"tm_on_upload": false},
				settingsView{MtOnUpload: true, TmOnUpload: false}},
			{map[string]any{"mt_on_upload": false},
				settingsView{MtOnUpload: false, TmOnUpload: false}},
			{map[string]any{"tm_on_upload": true},
				settingsView{MtOnUpload: false, TmOnUpload: true}},
		}
		for _, f := range flips {
			patchSettings(t, pid, f.body)
			got := getSettings(t, pid)
			if got != f.expect {
				t.Errorf("after partial PATCH %v: want %+v, got %+v", f.body, f.expect, got)
			}
		}
	})
}
