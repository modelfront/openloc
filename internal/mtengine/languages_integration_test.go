// The engine languages endpoint, end to end: a DeepL engine reports the
// regional targets DeepL really supports, and one without a key still answers.
package mtengine_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/secrets"
	"openloc/internal/testutil"
)

type languagesResp struct {
	Data struct {
		Sources []struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"sources"`
		Targets []struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"targets"`
	} `json:"data"`
}

func deeplServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") == "source" {
			fmt.Fprint(w, `[{"language":"EN","name":"English"},{"language":"DE","name":"German"}]`)
			return
		}
		fmt.Fprint(w, `[{"language":"EN-GB","name":"English (British)"},{"language":"EN-US","name":"English (American)"},{"language":"DE","name":"German"}]`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestIntegrationEngineLanguages_DeepLReportsRegionalTargets(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "A")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@a.test", "pass", "project_manager")

	key, url := "test-key", deeplServer(t).URL
	engine, err := q.CreateMTEngine(context.Background(), db.CreateMTEngineParams{
		Name: "DeepL", EngineType: "deepl", ApiKey: &key, ApiUrl: &url, Enabled: true, TenantID: tenant.ID,
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}

	rec := testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/mt-engines/%d/languages", engine.ID), nil, pm.ID, tenant.ID, "project_manager")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out languagesResp
	testutil.DecodeJSON(t, rec, &out)

	has := func(langs []struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}, code string) bool {
		for _, l := range langs {
			if l.Code == code {
				return true
			}
		}
		return false
	}
	if !has(out.Data.Sources, "en") {
		t.Fatalf("no 'en' source in the catalog: %+v", out.Data.Sources)
	}
	if !has(out.Data.Targets, "en-gb") || !has(out.Data.Targets, "en-us") {
		t.Errorf("regional targets missing from %+v", out.Data.Targets)
	}
	// The asymmetry the two-list shape exists for: en-gb is written, never read.
	if has(out.Data.Sources, "en-gb") {
		t.Error("en-gb is target-only and must not appear as a source")
	}
}

// An engine saved before its key must still answer with the static table
// rather than a 500.
func TestIntegrationEngineLanguages_NoKeyStillAnswers(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "A")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@a.test", "pass", "project_manager")
	engine := testutil.SeedMTEngine(t, q, tenant.ID, "DeepL", "deepl")

	rec := testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/mt-engines/%d/languages", engine.ID), nil, pm.ID, tenant.ID, "project_manager")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out languagesResp
	testutil.DecodeJSON(t, rec, &out)
	if len(out.Data.Sources) == 0 || len(out.Data.Targets) == 0 {
		t.Error("keyless engine returned an empty catalog")
	}
}

// Another tenant's engine must not be readable through this endpoint.
func TestIntegrationEngineLanguages_TenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "A")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@a.test", "pass", "project_manager")
	other := testutil.SeedTenant(t, q, "B")
	foreign := testutil.SeedMTEngine(t, q, other.ID, "B's DeepL", "deepl")

	rec := testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/mt-engines/%d/languages", foreign.ID), nil, pm.ID, tenant.ID, "project_manager")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// How a real DeepL row is stored: the key lives encrypted on a vendor token and
// `mt_engines.api_key` stays NULL, because the create handler always writes nil
// there. Reading the column instead of the token hands the fetcher an empty key,
// and an engine with no key answers from the static table — which carries no
// regional targets at all.
func TestIntegrationEngineLanguages_TokenBackedDeepLReportsRegionalTargets(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	enc, err := secrets.NewEncryptor(testutil.TestAISecretsKey)
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	sealed, err := enc.Encrypt("test-key")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	tenant := testutil.SeedTenant(t, q, "TokenCo")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@tokenco.test", "pass", "project_manager")
	tok := testutil.SeedAIVendorTokenBase(t, q, tenant.ID, "deepl", "DeepL key", sealed, deeplServer(t).URL)
	engine, err := q.CreateMTEngine(context.Background(), db.CreateMTEngineParams{
		Name: "DeepL", EngineType: "deepl", Enabled: true, TenantID: tenant.ID, TokenID: &tok.ID,
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}

	rec := testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/mt-engines/%d/languages", engine.ID), nil, pm.ID, tenant.ID, "project_manager")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out languagesResp
	testutil.DecodeJSON(t, rec, &out)

	for _, code := range []string{"en-gb", "en-us"} {
		found := false
		for _, l := range out.Data.Targets {
			if l.Code == code {
				found = true
			}
		}
		if !found {
			t.Errorf("%s missing — the catalog came from the static table, not the token's endpoint: %+v", code, out.Data.Targets)
		}
	}
}
