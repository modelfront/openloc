// Templates accept all four combinations of (mt_on_upload, tm_on_upload) on
// both Create and Update — mirrors the project-side contract documented in
// internal/project/tm_upload_validation_test.go.
//
// What this file pins:
//   1. Each combo round-trips: POST creates a template with the requested
//      flags, GET reads back the same values; PATCH writes a flag, GET reads
//      back the new value and preserves the others.
//   2. Partial PATCH preserves untouched fields. A bug where a partial PATCH
//      zeros out absent fields would surface in the partial-flip sub-test.
//
// Anti-tautology RED proofs:
//   - Drop the `TmOnUpload` field from the UpdateSettingsProfileParams struct
//     literal in template/handler.go::HandleUpdate → every PATCH that flips tm
//     round-trips as the old value; the rows that change tm_on_upload fail.
//   - Restore the rejected `if req.TmOnUpload && !req.MtOnUpload { 400 }` guard
//     in HandleCreate → the `(tm=true, mt=false)` create rows flip 201 → 400.
package template_test

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

type settingsView struct {
	MtOnUpload bool `json:"mt_on_upload"`
	TmOnUpload bool `json:"tm_on_upload"`
}

func TestTemplateSettingsRoundTripsAllUploadCombos(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TmplRoundTrip")
	user := testutil.SeedUser(t, q, tenant.ID, "u@tmplroundtrip.test", "pass")

	post := func(t *testing.T, path string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", testutil.AuthHeader(user.ID, tenant.ID))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}
	patch := func(t *testing.T, path string, body map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("PATCH", path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", testutil.AuthHeader(user.ID, tenant.ID))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("PATCH %s %v: want 200, got %d body=%s", path, body, rr.Code, rr.Body.String())
		}
	}
	getView := func(t *testing.T, id int64) settingsView {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET",
			"/api/templates/"+strconv.FormatInt(id, 10), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Template settingsView `json:"template"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data.Template
	}

	// Each combo gets its own template — sharing one template across combos
	// would let a "field didn't actually change" bug pass when the prior
	// create/update had already set the value.
	combos := []struct {
		mt bool
		tm bool
	}{
		{false, false},
		{true, false},
		{false, true},
		{true, true},
	}

	t.Run("Create round-trips every combo", func(t *testing.T) {
		for _, c := range combos {
			name := fmt.Sprintf("Create_mt=%v_tm=%v", c.mt, c.tm)
			t.Run(name, func(t *testing.T) {
				rr := post(t, "/api/templates", map[string]any{
					"name":         "C-" + name,
					"mt_on_upload": c.mt,
					"tm_on_upload": c.tm,
				})
				if rr.Code != http.StatusCreated {
					t.Fatalf("Create: want 201, got %d body=%s", rr.Code, rr.Body.String())
				}
				var resp struct {
					Data struct{ ID int64 } `json:"data"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode create response: %v", err)
				}
				got := getView(t, resp.Data.ID)
				if got.MtOnUpload != c.mt || got.TmOnUpload != c.tm {
					t.Errorf("after create: want mt=%v,tm=%v; got %+v", c.mt, c.tm, got)
				}
			})
		}
	})

	t.Run("Update round-trips every combo", func(t *testing.T) {
		rr := post(t, "/api/templates", map[string]any{
			"name": "UpdateProbe", "mt_on_upload": false, "tm_on_upload": false,
		})
		if rr.Code != http.StatusCreated {
			t.Fatalf("seed: want 201, got %d body=%s", rr.Code, rr.Body.String())
		}
		var resp struct {
			Data struct{ ID int64 } `json:"data"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode seed: %v", err)
		}
		path := "/api/templates/" + strconv.FormatInt(resp.Data.ID, 10)

		for _, c := range combos {
			name := fmt.Sprintf("Update_mt=%v_tm=%v", c.mt, c.tm)
			t.Run(name, func(t *testing.T) {
				patch(t, path, map[string]any{
					"mt_on_upload": c.mt,
					"tm_on_upload": c.tm,
				})
				got := getView(t, resp.Data.ID)
				if got.MtOnUpload != c.mt || got.TmOnUpload != c.tm {
					t.Errorf("after update: want mt=%v,tm=%v; got %+v", c.mt, c.tm, got)
				}
			})
		}
	})

	// Auto_complete is nil-keeps-stored, so the risk is the
	// mirror image of the on-upload flags — a PATCH that omits it must NOT
	// silently reset it to the column default (true). Both directions asserted.
	//
	// RED without `AutoComplete: req.AutoComplete` in HandleUpdate (the flip to
	// false never lands) or if the query drops its COALESCE (the unrelated
	// PATCH resets it to true).
	t.Run("auto_complete flips and survives an unrelated PATCH", func(t *testing.T) {
		rr := post(t, "/api/templates", map[string]any{"name": "AutoCompleteProbe"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("seed: want 201, got %d body=%s", rr.Code, rr.Body.String())
		}
		var resp struct {
			Data struct {
				ID           int64 `json:"id"`
				AutoComplete bool  `json:"auto_complete"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode seed: %v", err)
		}
		if !resp.Data.AutoComplete {
			t.Fatalf("pre-condition: a new template should start auto_complete=true (column default), got false")
		}
		path := "/api/templates/" + strconv.FormatInt(resp.Data.ID, 10)

		// Deliberately not via getView: widening its `settingsView` would break
		// the exact-struct comparisons the on-upload subtests rely on.
		readAutoComplete := func() bool {
			t.Helper()
			rec := testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
			testutil.AssertStatus(t, rec, http.StatusOK)
			var v struct {
				Data struct {
					Template struct {
						AutoComplete bool `json:"auto_complete"`
					} `json:"template"`
				} `json:"data"`
			}
			testutil.DecodeJSON(t, rec, &v)
			return v.Data.Template.AutoComplete
		}

		patch(t, path, map[string]any{"auto_complete": false})
		if readAutoComplete() {
			t.Fatalf("after PATCH auto_complete=false: want false, got true")
		}

		// An unrelated PATCH must leave it alone (nil-keeps-stored).
		patch(t, path, map[string]any{"tm_on_upload": true})
		if readAutoComplete() {
			t.Fatalf("after an unrelated PATCH: auto_complete was reset to true; it must keep the stored false")
		}
	})

	t.Run("partial PATCH preserves untouched fields", func(t *testing.T) {
		rr := post(t, "/api/templates", map[string]any{
			"name": "PartialProbe", "mt_on_upload": true, "tm_on_upload": true,
		})
		if rr.Code != http.StatusCreated {
			t.Fatalf("seed: want 201, got %d body=%s", rr.Code, rr.Body.String())
		}
		var resp struct {
			Data struct{ ID int64 } `json:"data"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode seed: %v", err)
		}
		path := "/api/templates/" + strconv.FormatInt(resp.Data.ID, 10)

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
			patch(t, path, f.body)
			got := getView(t, resp.Data.ID)
			if got != f.expect {
				t.Errorf("after partial PATCH %v: want %+v, got %+v", f.body, f.expect, got)
			}
		}
	})
}
