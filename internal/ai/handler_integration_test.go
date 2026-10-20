// AI vendor registry: list/status with tokens, tenant isolation, role
// gating, and the hard "never return the API key" rule. The add-token live-test
// path is covered by connect_test.go (tester) + secrets_test.go (encryption);
// here we seed tokens directly so no external calls are made.
package ai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/mtdefaults"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

func TestIntegrationAIVendors(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	tenantB := testutil.SeedTenant(t, q, "TenantB")
	ownerA := testutil.SeedUserWithRole(t, q, tenantA.ID, "ownerA@test.com", "pass", "owner")
	pmA := testutil.SeedUserWithRole(t, q, tenantA.ID, "pmA@test.com", "pass", "project_manager")
	linguistA := testutil.SeedUserWithRole(t, q, tenantA.ID, "lingA@test.com", "pass", "linguist")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "ownerB@test.com", "pass", "owner")

	// Seed a token on OpenAI for tenant A directly (bypasses the live test). The
	// stored key is opaque ciphertext to this layer.
	tok := testutil.SeedAIVendorToken(t, q, tenantA.ID, "openai", "prod", "ENC(sk-secret-should-never-leak)")

	type statusResp struct {
		Data []struct {
			Provider  string `json:"provider"`
			Connected bool   `json:"connected"`
			Tokens    []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"tokens"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
			ModelsError string `json:"models_error"`
		} `json:"data"`
	}

	// Tenant A manager (PM) sees openai connected with its token; key never leaks.
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/ai/vendors", nil, pmA.ID, tenantA.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "api_key") || strings.Contains(rec.Body.String(), "ENC(") {
		t.Fatalf("AI vendor list leaked a key: %s", rec.Body.String())
	}
	var respA statusResp
	testutil.DecodeJSON(t, rec, &respA)
	// Every known provider is listed whether or not it is configured, so this
	// tracks ai.KnownProviders rather than a hardcoded count — adding a vendor
	// should not mean editing an unrelated assertion.
	if len(respA.Data) != len(ai.KnownProviders) {
		t.Fatalf("expected %d vendors (ai.KnownProviders), got %d", len(ai.KnownProviders), len(respA.Data))
	}
	byProvider := map[string]bool{}
	tokenCount := map[string]int{}
	modelCount := map[string]int{}
	for _, p := range respA.Data {
		byProvider[p.Provider] = p.Connected
		tokenCount[p.Provider] = len(p.Tokens)
		modelCount[p.Provider] = len(p.Models)
	}
	if !byProvider["openai"] || tokenCount["openai"] != 1 {
		t.Errorf("openai should be connected with 1 token, got connected=%v tokens=%d", byProvider["openai"], tokenCount["openai"])
	}
	if byProvider["claude"] {
		t.Error("claude should be not-connected for tenant A")
	}
	// Each vendor's model list is fetched with this tenant's own key, so a vendor
	// lists models only if the tenant has a key for it.
	// Tenant A has an OpenAI key and no Claude key, so OpenAI lists models and
	// Claude lists none.
	if modelCount["openai"] == 0 {
		t.Error("openai is connected, so it should expose the models that key can reach")
	}
	if modelCount["claude"] != 0 {
		t.Errorf("claude has no key for this tenant, so it should expose no models, got %d", modelCount["claude"])
	}

	// Tenant isolation: tenant B sees nothing connected.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/vendors", nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var respB statusResp
	testutil.DecodeJSON(t, rec, &respB)
	for _, p := range respB.Data {
		if p.Connected {
			t.Errorf("tenant B must not see tenant A's connection (%s)", p.Provider)
		}
	}

	// Role gating: linguists can't even list (manager-only surface).
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/vendors", nil, linguistA.ID, tenantA.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Adding a token is owner/admin-only — a PM is forbidden (blocked before the
	// live test runs, so no external call).
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/vendors/openai/tokens",
		map[string]string{"api_key": "sk-x"}, pmA.ID, tenantA.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Unknown vendor → 400 (validated before the tester is called).
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/vendors/gemini/tokens",
		map[string]string{"api_key": "sk-x"}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// A key an AI engine runs with DELETES: the FK nulls the
	// row's token, the engine keeps its prompts, model and routes, and it goes
	// unusable — which every surface flags.
	{
		sysPrompt, err := q.CreateAIPrompt(context.Background(), db.CreateAIPromptParams{
			TenantID: tenantA.ID, Name: "guard-prompt", PromptText: "x", OutStatus: true,
		})
		if err != nil {
			t.Fatalf("seed prompt: %v", err)
		}
		sys, err := q.CreateAIEngine(context.Background(), db.CreateAIEngineParams{
			TenantID: tenantA.ID, Name: "guard-system", Purpose: "qe_ape",
		})
		if err != nil {
			t.Fatalf("seed system: %v", err)
		}
		engineRow, err := q.CreateAIEngineRow(context.Background(), db.CreateAIEngineRowParams{
			EngineID: sys.ID, SourceLang: "*", TargetLangs: []string{"*"},
			SystemPromptID: &sysPrompt.ID, TokenID: &tok.ID, Model: "gpt-4o", Settings: []byte("{}"),
		})
		if err != nil {
			t.Fatalf("seed system row: %v", err)
		}
		rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d", tok.ID), nil, ownerA.ID, tenantA.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusNoContent)

		// The engine survived, minus its key.
		after, err := q.ListAIEngineRows(context.Background(), sys.ID)
		if err != nil {
			t.Fatalf("list rows after delete: %v", err)
		}
		if len(after) != 1 || after[0].ID != engineRow.ID {
			t.Fatalf("the engine row went with the key: %+v", after)
		}
		if after[0].TokenID != nil {
			t.Errorf("token_id = %v after the key was deleted, want NULL", after[0].TokenID)
		}

		// The list endpoint says WHY it cannot run, which is the whole point of
		// allowing the delete.
		rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/engines", nil, ownerA.ID, tenantA.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		if !strings.Contains(rec.Body.String(), `"unusable":"no_key"`) {
			t.Errorf("engine list does not flag the keyless engine: %s", rec.Body.String())
		}

		if err := q.SoftDeleteAIEngine(context.Background(), db.SoftDeleteAIEngineParams{ID: sys.ID, TenantID: tenantA.ID}); err != nil {
			t.Fatalf("cleanup system: %v", err)
		}
		// Re-seed the token the rest of this test deletes on the happy path.
		tok = testutil.SeedAIVendorToken(t, q, tenantA.ID, "openai", "prod2", "ENC(sk-secret-should-never-leak)")
	}

	// Owner deletes the token → 204; list then shows openai not connected.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d", tok.ID), nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/vendors", nil, ownerA.ID, tenantA.ID, "owner")
	var respAfter statusResp
	testutil.DecodeJSON(t, rec, &respAfter)
	for _, p := range respAfter.Data {
		if p.Provider == "openai" && p.Connected {
			t.Error("openai should be disconnected after token delete")
		}
	}
}

// A vendor whose call failed must say WHY on the wire. Without this the settings
// page shows an empty dropdown that looks identical to "this key reaches no
// models", and the user has no way to debug it.
func TestIntegrationAIVendorsReportsWhyModelsAreEmpty(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	// A real vendor rejecting a real key: the production lister against a server
	// that 401s. A hand-rolled error double can't produce the classification —
	// that is derived from the HTTP status — so it would assert nothing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid x-api-key","org":"acme-corp"}}`))
	}))
	defer srv.Close()

	router := testutil.NewTestRouterWithCatalog(pool, nil, "",
		testutil.TestAICatalogWith(pool, ai.NewHTTPModelLister()))
	tenant := testutil.SeedTenant(t, q, "BadKey")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner-badkey@test.com", "pass", "owner")
	testutil.SeedAIVendorTokenBase(t, q, tenant.ID, "claude", "prod", "ENC(whatever)", srv.URL)

	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/ai/vendors", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data []struct {
			Provider    string                `json:"provider"`
			Models      []struct{ ID string } `json:"models"`
			ModelsError string                `json:"models_error"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	var seen bool
	for _, p := range resp.Data {
		if p.Provider != "claude" {
			// A vendor that never failed carries no explanation.
			if p.ModelsError != "" {
				t.Errorf("%s reports %q with nothing wrong", p.Provider, p.ModelsError)
			}
			continue
		}
		seen = true
		if len(p.Models) != 0 {
			t.Errorf("a rejected key lists no models, got %d", len(p.Models))
		}
		if p.ModelsError != "The API key was rejected." {
			t.Errorf("models_error = %q, want the rejected-key sentence", p.ModelsError)
		}
	}
	if !seen {
		t.Fatal("claude missing from the vendor list")
	}
	// The provider's own words never reach the client, only our classification.
	if strings.Contains(rec.Body.String(), "acme-corp") || strings.Contains(rec.Body.String(), "invalid x-api-key") {
		t.Errorf("the provider's error body leaked to the client: %s", rec.Body.String())
	}
}

// A token's custom endpoint is what points a tenant at Azure OpenAI instead of
// api.openai.com, so losing it silently reroutes every
// translation to the wrong provider — with a key that provider will reject.
// Pins that an edit which omits the field keeps it, distinct from an explicit ""
func TestIntegrationAIVendorTokenBaseURL(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AzureTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@azure.test", "pass", "owner")

	const azureBase = "https://acme.openai.azure.com/openai"
	tok := testutil.SeedAIVendorTokenBase(t, q, tenant.ID, "openai", "azure-sweden", "ENC(key)", azureBase)

	baseURLOf := func(t *testing.T, tokenID int64) string {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "GET", "/api/ai/vendors", nil, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []struct {
				Provider string `json:"provider"`
				Tokens   []struct {
					ID      int64  `json:"id"`
					BaseURL string `json:"base_url"`
				} `json:"tokens"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		for _, v := range resp.Data {
			for _, tk := range v.Tokens {
				if tk.ID == tokenID {
					return tk.BaseURL
				}
			}
		}
		t.Fatalf("token %d not found in vendor list", tokenID)
		return ""
	}

	if got := baseURLOf(t, tok.ID); got != azureBase {
		t.Fatalf("seeded base_url = %q, want %q", got, azureBase)
	}

	// The endpoint lives on the token's CONNECTION, so the endpoint
	// guards apply to the connection.
	connID := func(t *testing.T) int64 {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "GET", "/api/ai/vendors/connections", nil, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []struct {
				ID      int64  `json:"id"`
				BaseURL string `json:"base_url"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		for _, c := range resp.Data {
			if c.BaseURL == azureBase {
				return c.ID
			}
		}
		t.Fatalf("no connection carrying %q", azureBase)
		return 0
	}
	cid := connID(t)
	patchConn := func(t *testing.T, body map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		return testutil.DoRequestWithRole(t, router, "PATCH",
			fmt.Sprintf("/api/ai/vendors/openai/connections/%d", cid), body, owner.ID, tenant.ID, "owner")
	}

	// Renaming a TOKEN must not disturb where it points — the original incident.
	rec := testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d", tok.ID),
		map[string]string{"name": "renamed"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if got := baseURLOf(t, tok.ID); got != azureBase {
		t.Errorf("renaming a token wiped its endpoint: base_url = %q, want %q", got, azureBase)
	}

	// Renaming the CONNECTION with no base_url in the body must keep it too.
	testutil.AssertStatus(t, patchConn(t, map[string]string{"name": "renamed conn"}), http.StatusOK)
	if got := baseURLOf(t, tok.ID); got != azureBase {
		t.Errorf("renaming the connection wiped the endpoint: got %q", got)
	}

	// An explicit empty string still clears it — "absent" and "cleared" are
	// different intents and must stay distinguishable.
	testutil.AssertStatus(t, patchConn(t, map[string]string{"name": "renamed conn", "base_url": ""}), http.StatusOK)
	if got := baseURLOf(t, tok.ID); got != "" {
		t.Errorf("explicit empty base_url should clear it, got %q", got)
	}

	// A pasted endpoint is normalised on save: the Azure portal hands out the
	// bare resource root, which without the /openai prefix 404s at translation.
	testutil.AssertStatus(t, patchConn(t, map[string]string{
		"name": "renamed conn", "base_url": "https://acme.openai.azure.com/",
	}), http.StatusOK)
	if got := baseURLOf(t, tok.ID); got != azureBase {
		t.Errorf("bare Azure endpoint should normalise to %q, got %q", azureBase, got)
	}

	// A malformed endpoint is rejected rather than stored for later failure.
	testutil.AssertStatus(t, patchConn(t, map[string]string{
		"name": "renamed conn", "base_url": "http://api.openai.com",
	}), http.StatusBadRequest)
	if got := baseURLOf(t, tok.ID); got != azureBase {
		t.Errorf("a rejected update must not change the stored endpoint, got %q", got)
	}

	// The connection is the shared endpoint for every key on it: changing it
	// once moves them all, which is the reason the concept exists.
	tok2 := testutil.SeedAIVendorTokenBase(t, q, tenant.ID, "openai", "second", "ENC(key2)", azureBase)
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d", tok2.ID),
		map[string]any{"name": "second", "connection_id": cid}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.AssertStatus(t, patchConn(t, map[string]string{
		"name": "renamed conn", "base_url": "https://other-resource.openai.azure.com/",
	}), http.StatusOK)
	const moved = "https://other-resource.openai.azure.com/openai"
	for _, id := range []int64{tok.ID, tok2.ID} {
		if got := baseURLOf(t, id); got != moved {
			t.Errorf("token %d should follow its connection to %q, got %q", id, moved, got)
		}
	}
}

// A connection is "live" purely because it has a key — there is no separate
// enabled flag, since nothing can call an endpoint it has no credential for.
// token_count is what the UI reads, so it has to be right the moment a key is
// added or removed.
func TestIntegrationAIConnectionTokenCount(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "CountTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@count.test", "pass", "owner")

	tokenCount := func(t *testing.T) int {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "GET", "/api/ai/vendors/connections", nil, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []struct {
				TokenCount int `json:"token_count"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode connections: %v", err)
		}
		if len(resp.Data) != 1 {
			t.Fatalf("expected exactly 1 connection, got %d", len(resp.Data))
		}
		return resp.Data[0].TokenCount
	}

	// SeedAIVendorToken creates the connection AND a key on it.
	tok := testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "prod", "ENC(key)")
	if got := tokenCount(t); got != 1 {
		t.Fatalf("token_count = %d, want 1 right after the key is created", got)
	}

	rec := testutil.DoRequestWithRole(t, router,
		"DELETE", "/api/ai/vendors/openai/tokens/"+strconv.FormatInt(tok.ID, 10),
		nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// The connection survives its last key — you keep the endpoint and re-key it
	// later — but it must no longer count as live.
	if got := tokenCount(t); got != 0 {
		t.Errorf("token_count = %d after deleting the only key, want 0", got)
	}
}

// The PATCH response must carry the REAL key count. token_count is what says
// whether a connection is live, and this DTO is assembled
// by hand here rather than through connectionStatusFrom, so pins that editing
// the endpoint of a working connection still reports its keys.
func TestIntegrationAIConnectionUpdateReportsTokenCount(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PatchCountTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@patchcount.test", "pass", "owner")
	testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "prod", "ENC(key)")

	conns, err := q.ListAIConnections(context.Background(), tenant.ID)
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(conns))
	}

	rec := testutil.DoRequestWithRole(t, router,
		"PATCH", "/api/ai/vendors/openai/connections/"+strconv.FormatInt(conns[0].ID, 10),
		map[string]any{"base_url": "https://gw.example.com"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			BaseURL    string `json:"base_url"`
			TokenCount int    `json:"token_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.BaseURL != "https://gw.example.com" {
		t.Errorf("base_url = %q, want the value just saved", resp.Data.BaseURL)
	}
	if resp.Data.TokenCount != 1 {
		t.Errorf("token_count = %d, want 1 — the connection still has its key", resp.Data.TokenCount)
	}
}

// A key is a CREDENTIAL, not an engine. Adding one must
// create nothing on the MT engines page: an engine is a configuration built
// there, naming a key and carrying that provider's options, and auto-creating
// one would put a row in every router picker that nobody asked for.
func TestIntegrationMTTokenMakesNoEngine(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "NoProjectionTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@noproj.test", "pass", "owner")

	// Creating a token verifies the key against the vendor for real, so the
	// connection points at a stub instead of DeepL.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer stub.Close()

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/ai/vendors/deepl/connections",
		map[string]any{"base_url": stub.URL}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var connResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &connResp); err != nil {
		t.Fatalf("decode connection: %v", err)
	}

	// Both an MT vendor and an LLM one: neither makes an engine, and the LLM
	// side is additionally reached through the token itself.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/vendors/deepl/tokens",
		map[string]any{"name": "Prod", "api_key": "dk-123:fx", "connection_id": connResp.Data.ID},
		owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "prod", "ENC(key)")

	got := listEngines(t, q, tenant.ID)
	if len(got) != 0 {
		t.Errorf("adding keys created %d MT engine(s), want 0: %+v", len(got), got)
	}
}

// A key an MT engine spends DELETES, and the engine goes unusable rather than
// standing in the way. The FK is SET NULL, so the engine
// keeps its name, options and routes and every surface flags it.
func TestIntegrationTokenDeleteLeavesTheEngineUnusable(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "KeyInUseTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@keyinuse.test", "pass", "owner")
	tok := testutil.SeedAIVendorToken(t, q, tenant.ID, "deepl", "EU", "ENC(dk-eu)")

	engine, err := q.CreateMTEngine(context.Background(), db.CreateMTEngineParams{
		Name: "DeepL formal", EngineType: "deepl", Enabled: true,
		TenantID: tenant.ID, TokenID: &tok.ID, Settings: []byte(`{"formality":"more"}`),
	})
	if err != nil {
		t.Fatalf("seed engine: %v", err)
	}

	rec := testutil.DoRequestWithRole(t, router, "DELETE",
		fmt.Sprintf("/api/ai/vendors/deepl/tokens/%d", tok.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	after, err := q.GetMTEngine(context.Background(), db.GetMTEngineParams{ID: engine.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("the engine went with the key: %v", err)
	}
	if after.TokenID != nil {
		t.Errorf("token_id = %v after the key was deleted, want NULL", after.TokenID)
	}
	// Everything the user configured survives — that is why the delete is
	// allowed rather than refused. Decoded, not string-compared: jsonb
	// round-trips with its own spacing.
	var opts map[string]any
	if err := json.Unmarshal(after.Settings, &opts); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if after.Name != "DeepL formal" || opts["formality"] != "more" {
		t.Errorf("the engine lost its configuration: name=%q settings=%v", after.Name, opts)
	}

	// ...and the list says why it cannot run.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/mt-engines", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"unusable":"no_key"`) {
		t.Errorf("MT engine list does not flag the keyless engine: %s", rec.Body.String())
	}
}

// A provisioned engine is the ORG's: it is
// created with the tenant and behaves like any other from then on. The flag on
// it records where it came from and gates nothing — the AI settings grid reads
// it to know whether a built-in vendor exists on this deployment.
func TestIntegrationProvisionedEngineIsTheOrgsToEditAndDelete(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "LibreTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@libre.test", "pass", "owner")
	t.Setenv("LIBRETRANSLATE_URL", "http://libretranslate.internal:5000")
	t.Setenv("ENV", "development")
	if err := mtdefaults.EnsureForTenant(context.Background(), q, tenant.ID); err != nil {
		t.Fatalf("provision engines: %v", err)
	}
	all := listEngines(t, q, tenant.ID)
	engine := engineOfType(t, all, mtdefaults.TypeLibreTranslate)

	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/mt-engines", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listed struct {
		Data []struct {
			ID            int64  `json:"id"`
			ProvisionedBy string `json:"provisioned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode engines: %v", err)
	}
	if len(listed.Data) == 0 {
		t.Fatal("no engines listed — provisioning did nothing")
	}
	for _, e := range listed.Data {
		if e.ProvisionedBy != "deployment" {
			t.Errorf("provisioned_by = %q for engine %d, want the provenance recorded", e.ProvisionedBy, e.ID)
		}
	}

	id := strconv.FormatInt(engine.ID, 10)
	// Renaming and switching off both work — it is an ordinary engine.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", "/api/mt-engines/"+id,
		map[string]any{"name": "Our LibreTranslate"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequestWithRole(t, router, "PATCH", "/api/mt-engines/"+id,
		map[string]any{"enabled": false}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// A rename must not blank the endpoint the deployment supplied — it is not a
	// field on this page, so only keep-current semantics can preserve it.
	after, err := q.GetMTEngine(context.Background(), db.GetMTEngineParams{ID: engine.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("reload engine: %v", err)
	}
	if after.ApiUrl == nil || *after.ApiUrl != "http://libretranslate.internal:5000" {
		t.Errorf("api_url = %v after a rename, want the provisioned endpoint intact", after.ApiUrl)
	}

	// ...and it deletes, for good. What makes the delete stick is that
	// provisioning runs ONCE, when the tenant is created: there is no
	// all-tenants sweep, so nothing walks back over an existing org and
	// re-creates what they removed. `EnsureForTenant` is deliberately not called
	// again here — doing so would test a path production never takes.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", "/api/mt-engines/"+id, nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	all = listEngines(t, q, tenant.ID)
	for _, e := range all {
		if e.EngineType == mtdefaults.TypeLibreTranslate {
			t.Error("the engine survived its delete")
		}
	}
}

// The list endpoint must never return a stored key, or the
// edit form would prefill from it.
func TestIntegrationMTEngineListNeverReturnsKey(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "NoLeakTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@noleak.test", "pass", "owner")

	if _, err := q.CreateMTEngine(context.Background(), db.CreateMTEngineParams{
		Name: "DeepL Pro", EngineType: "deepl",
		ApiKey: ptr.To("super-secret-key"), Enabled: true, TenantID: tenant.ID,
	}); err != nil {
		t.Fatalf("seed engine: %v", err)
	}

	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/mt-engines", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	// `"api_key"` with both quotes, so this does not match the `has_api_key`
	// boolean that is legitimately there.
	if strings.Contains(body, "super-secret-key") || strings.Contains(body, `"api_key"`) {
		t.Fatalf("MT engine list leaked a key: %s", body)
	}
	if !strings.Contains(body, `"has_api_key":true`) {
		t.Errorf("list should still report that a key IS set; got %s", body)
	}
}

// What a deployment supplies depends on its environment, and a tenant gets it
// once, when it is created.
func TestIntegrationDeploymentEngineProvisioning(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "ProvisionTenant")

	engines := listEngines(t, q, tenant.ID)
	if len(engines) != 0 {
		t.Fatalf("a fresh tenant should start with no engines, got %d", len(engines))
	}

	// Production: no LibreTranslate URL and no mock. An engine that invents
	// translations must never be one click from live work.
	t.Setenv("LIBRETRANSLATE_URL", "")
	t.Setenv("ENV", "production")
	if err := mtdefaults.EnsureForTenant(ctx, q, tenant.ID); err != nil {
		t.Fatalf("provision in production: %v", err)
	}
	engines = listEngines(t, q, tenant.ID)
	if len(engines) != 0 {
		t.Fatalf("production provisioned %d engine(s), want 0", len(engines))
	}

	// Outside production the mock appears even with no LibreTranslate.
	t.Setenv("ENV", "development")
	if err := mtdefaults.EnsureForTenant(ctx, q, tenant.ID); err != nil {
		t.Fatalf("provision: %v", err)
	}
	engines = listEngines(t, q, tenant.ID)
	if len(engines) != 1 || engines[0].EngineType != mtdefaults.TypeMock {
		t.Fatalf("expected exactly the mock engine, got %+v", engines)
	}

	const url = "http://libretranslate.internal:5000"
	t.Setenv("LIBRETRANSLATE_URL", url)
	if err := mtdefaults.EnsureForTenant(ctx, q, tenant.ID); err != nil {
		t.Fatalf("provision with a URL: %v", err)
	}
	engines = listEngines(t, q, tenant.ID)
	if len(engines) != 2 {
		t.Fatalf("expected mock + LibreTranslate, got %d", len(engines))
	}
	lt := engineOfType(t, engines, mtdefaults.TypeLibreTranslate)
	if lt.ApiUrl == nil || *lt.ApiUrl != url {
		t.Errorf("api_url = %v, want %q", lt.ApiUrl, url)
	}
	if !lt.Enabled {
		t.Error("provisioned engine is disabled — nothing would translate through it")
	}

	// Idempotent, so a retried signup cannot double up.
	if err := mtdefaults.EnsureForTenant(ctx, q, tenant.ID); err != nil {
		t.Fatalf("second provision: %v", err)
	}
	engines = listEngines(t, q, tenant.ID)
	if len(engines) != 2 {
		t.Fatalf("re-running provisioning made %d engines, want 2", len(engines))
	}

	// A moved host is NOT healed: the row belongs to the org now, and silently
	// rewriting a field they can see is worse than leaving it for them to change.
	t.Setenv("LIBRETRANSLATE_URL", "http://libretranslate.internal:5001")
	if err := mtdefaults.EnsureForTenant(ctx, q, tenant.ID); err != nil {
		t.Fatalf("provision after move: %v", err)
	}
	engines = listEngines(t, q, tenant.ID)
	lt = engineOfType(t, engines, mtdefaults.TypeLibreTranslate)
	if lt.ApiUrl == nil || *lt.ApiUrl != url {
		t.Errorf("api_url = %v after the host moved, want the original %q left alone", lt.ApiUrl, url)
	}
}

func listEngines(t *testing.T, q db.Querier, tenantID int64) []db.MtEngine {
	t.Helper()
	engines, err := q.ListMTEngines(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("list engines: %v", err)
	}
	return engines
}

func engineOfType(t *testing.T, engines []db.MtEngine, engineType string) db.MtEngine {
	t.Helper()
	for _, e := range engines {
		if e.EngineType == engineType {
			return e
		}
	}
	t.Fatalf("no %s engine among %+v", engineType, engines)
	return db.MtEngine{}
}

// Provenance is a property of the ROW, not the engine type: a hand-made mock
// reads as the user's because it is, while the provisioned one is marked as
// coming from the deployment. Both are equally editable.
func TestIntegrationHandMadeBuiltInTypeReadsAsTheUsers(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "HandMadeTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@handmade.test", "pass", "owner")

	// Same engine_type the deployment provisions, but made by hand.
	mine := testutil.SeedMTEngine(t, q, tenant.ID, "My own mock", "mock")

	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/mt-engines", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listed struct {
		Data []struct {
			ID            int64  `json:"id"`
			ProvisionedBy string `json:"provisioned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ProvisionedBy != "" {
		t.Fatalf("provisioned_by = %+v, want the hand-made engine to read as the user's", listed.Data)
	}

	// It is the user's, so it edits and deletes normally.
	id := strconv.FormatInt(mine.ID, 10)
	rec = testutil.DoRequestWithRole(t, router, "PATCH", "/api/mt-engines/"+id,
		map[string]any{"name": "Renamed"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequestWithRole(t, router, "DELETE", "/api/mt-engines/"+id, nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
}

// Provisioning must not duplicate an engine a tenant already has, however that
// row was made. The seed and the server both provision, so a disagreement
// about ownership would give every seeded tenant a SECOND LibreTranslate row on
// the next boot.
func TestIntegrationProvisioningNeverDuplicates(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "DupTenant")
	t.Setenv("LIBRETRANSLATE_URL", "http://libretranslate.internal:5000")
	t.Setenv("ENV", "development")

	// A pre-existing hand-made row of a type the deployment also supplies.
	testutil.SeedMTEngine(t, q, tenant.ID, "Someone's own LibreTranslate", mtdefaults.TypeLibreTranslate)

	for i := 0; i < 3; i++ {
		if err := mtdefaults.EnsureForTenant(ctx, q, tenant.ID); err != nil {
			t.Fatalf("provision run %d: %v", i, err)
		}
	}

	engines := listEngines(t, q, tenant.ID)
	byType := map[string]int{}
	for _, e := range engines {
		byType[e.EngineType]++
	}
	if byType[mtdefaults.TypeLibreTranslate] != 1 {
		t.Errorf("%d LibreTranslate engines, want 1 — the hand-made row already covered it",
			byType[mtdefaults.TypeLibreTranslate])
	}
	if byType[mtdefaults.TypeMock] != 1 {
		t.Errorf("%d mock engines, want exactly 1", byType[mtdefaults.TypeMock])
	}
	// The hand-made row stays the user's: provisioning must not adopt it.
	lt := engineOfType(t, engines, mtdefaults.TypeLibreTranslate)
	if lt.DeploymentManaged {
		t.Error("provisioning adopted a hand-made engine — it would become undeletable")
	}
	if lt.Name != "Someone's own LibreTranslate" {
		t.Errorf("name = %q, want the user's own name left alone", lt.Name)
	}
}

// Pins that chat spends the token marked use_for_chat, and falls back to the
// oldest only when none is marked — so a tenant holding a cheap key and an
// expensive one chooses which one chat costs them.
func TestIntegrationChatTokenIsChosenNotOldest(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ChatTokenTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@chattoken.test", "pass", "owner")

	oldest := testutil.SeedAIVendorToken(t, q, tenant.ID, "claude", "first", "ENC(old)")
	vendor, err := q.GetAIVendor(context.Background(), db.GetAIVendorParams{TenantID: tenant.ID, Provider: "claude"})
	if err != nil {
		t.Fatalf("get vendor: %v", err)
	}
	newer, err := q.CreateVendorToken(context.Background(), db.CreateVendorTokenParams{
		VendorID: vendor.ID, Name: "second", ApiKeyEncrypted: "ENC(new)",
		ConnectionID: oldest.ConnectionID, KeyLast4: "beef",
	})
	if err != nil {
		t.Fatalf("create second token: %v", err)
	}

	chatToken := func(t *testing.T) int64 {
		t.Helper()
		tok, err := q.GetChatVendorToken(context.Background(), db.GetChatVendorTokenParams{
			TenantID: tenant.ID, Provider: "claude",
		})
		if err != nil {
			t.Fatalf("resolve chat token: %v", err)
		}
		return tok.ID
	}

	// Nobody has chosen: the historical fallback still applies, so an existing
	// tenant's chat does not change key under them.
	if got := chatToken(t); got != oldest.ID {
		t.Errorf("with no choice made, chat token = %d, want the oldest (%d)", got, oldest.ID)
	}

	// Choose the newer one through the API.
	rec := testutil.DoRequestWithRole(t, router,
		"PATCH", "/api/ai/vendors/claude/tokens/"+strconv.FormatInt(newer.ID, 10),
		map[string]any{"name": "second", "use_for_chat": true}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	if got := chatToken(t); got != newer.ID {
		t.Errorf("chat token = %d after choosing, want %d", got, newer.ID)
	}

	// Choosing the other one MOVES the flag — two winners would make the
	// resolution order-dependent again.
	rec = testutil.DoRequestWithRole(t, router,
		"PATCH", "/api/ai/vendors/claude/tokens/"+strconv.FormatInt(oldest.ID, 10),
		map[string]any{"name": "first", "use_for_chat": true}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	tokens, err := q.ListVendorTokens(context.Background(), tenant.ID)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	chosen := 0
	for _, tk := range tokens {
		if tk.UseForChat {
			chosen++
		}
	}
	if chosen != 1 {
		t.Errorf("%d tokens marked for chat, want exactly 1", chosen)
	}
	if got := chatToken(t); got != oldest.ID {
		t.Errorf("chat token = %d after re-choosing, want %d", got, oldest.ID)
	}
}

// An MT vendor holds one key per account it has. Nothing is projected from
// them — what proves the keys are usable is that
// an MT engine can name each one, which internal/mtengine covers. Here the
// point is only that the second key is ACCEPTED.
func TestIntegrationMTVendorTakesSeveralKeys(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "MultiKeyTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@multikey.test", "pass", "owner")

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer stub.Close()

	connFor := func(provider string) int64 {
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/ai/vendors/"+provider+"/connections",
			map[string]any{"base_url": stub.URL}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var conn struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &conn); err != nil {
			t.Fatalf("decode %s connection: %v", provider, err)
		}
		return conn.Data.ID
	}
	add := func(provider string, connID int64, name string) *httptest.ResponseRecorder {
		return testutil.DoRequestWithRole(t, router, "POST", "/api/ai/vendors/"+provider+"/tokens",
			map[string]any{"name": name, "api_key": "k-" + name, "connection_id": connID},
			owner.ID, tenant.ID, "owner")
	}

	deepl := connFor("deepl")
	testutil.AssertStatus(t, add("deepl", deepl, "EU"), http.StatusCreated)
	testutil.AssertStatus(t, add("deepl", deepl, "US"), http.StatusCreated)

	// An LLM vendor is unaffected: several keys were always the point there.
	openai := connFor("openai")
	for _, name := range []string{"cheap", "prod"} {
		testutil.AssertStatus(t, add("openai", openai, name), http.StatusCreated)
	}

	tokens, err := q.ListVendorTokens(context.Background(), tenant.ID)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	if len(tokens) != 4 {
		t.Errorf("stored %d keys, want 4: %+v", len(tokens), tokens)
	}
}
