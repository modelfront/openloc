// An MT engine is a configuration: a key from AI settings plus the request
// options that key's provider supports. These cover what a
// user can build through the HTTP surface, and the two ways a form can be wrong
// that only the server can catch.
package mtengine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/mtengine"
	"openloc/internal/testutil"
)

type engineResp struct {
	Data struct {
		ID         int64          `json:"id"`
		Name       string         `json:"name"`
		EngineType string         `json:"engine_type"`
		TokenID       *int64         `json:"token_id"`
		Settings      map[string]any `json:"settings"`
		HasAPIKey     bool           `json:"has_api_key"`
		ProvisionedBy string         `json:"provisioned_by"`
	} `json:"data"`
}

// Pins that one key can back two engines that translate differently. If the second create were refused, or the two shared a settings
// row, the formal/informal split a router exists to express would be impossible.
func TestIntegrationTwoEnginesOnOneKey(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TwoEngineTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@twoengine.test", "pass", "owner")
	tok := testutil.SeedAIVendorToken(t, q, tenant.ID, "deepl", "EU", "ENC(dk-eu)")

	create := func(name, formality string) engineResp {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-engines", map[string]any{
			"name": name, "engine_type": "deepl", "token_id": tok.ID,
			"settings": map[string]any{"formality": formality},
		}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var out engineResp
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode engine: %v", err)
		}
		return out
	}

	formal := create("DeepL formal", "more")
	casual := create("DeepL casual", "less")

	if formal.Data.ID == casual.Data.ID {
		t.Fatal("both creates returned the same engine")
	}
	for _, e := range []engineResp{formal, casual} {
		if e.Data.TokenID == nil || *e.Data.TokenID != tok.ID {
			t.Errorf("%s does not spend the key it was given: %v", e.Data.Name, e.Data.TokenID)
		}
		// The credential belongs to the key. A copy on the engine would be a
		// second thing to rotate, and the list endpoint would have to hide it.
		if e.Data.HasAPIKey {
			t.Errorf("%s carries its own key — it should authenticate through the AI settings key", e.Data.Name)
		}
		if e.Data.ProvisionedBy != "" {
			t.Errorf("%s reads as managed_by %q — a configured engine is the user's", e.Data.Name, e.Data.ProvisionedBy)
		}
	}
	if formal.Data.Settings["formality"] != "more" || casual.Data.Settings["formality"] != "less" {
		t.Errorf("the two engines share options: %v vs %v", formal.Data.Settings, casual.Data.Settings)
	}

	// Both must be listed — a router picks between them by name.
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/mt-engines", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listed struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listed)
	if len(listed.Data) != 2 {
		t.Errorf("listed %d engines, want 2", len(listed.Data))
	}
}

// A key from the wrong provider passes every other check and then fails on
// every translation with an authentication error nobody can trace back to the
// form. Same for a key belonging to another tenant, which must not even be
// nameable.
func TestIntegrationEngineRejectsTheWrongKey(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "WrongKeyTenant")
	other := testutil.SeedTenant(t, q, "OtherKeyTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@wrongkey.test", "pass", "owner")
	googleTok := testutil.SeedAIVendorToken(t, q, tenant.ID, "google", "GCP", "ENC(g-key)")
	foreignTok := testutil.SeedAIVendorToken(t, q, other.ID, "deepl", "Theirs", "ENC(dk-theirs)")

	post := func(body map[string]any) *string {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-engines", body, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		s := rec.Body.String()
		return &s
	}

	// A Google key on a DeepL engine.
	if msg := post(map[string]any{
		"name": "Mismatched", "engine_type": "deepl", "token_id": googleTok.ID,
	}); !strings.Contains(*msg, "Google") {
		t.Errorf("refusal %q should name the provider the key actually belongs to", *msg)
	}

	// Another tenant's key is indistinguishable from one that does not exist —
	// deliberately, so this cannot be used to probe for ids.
	if msg := post(map[string]any{
		"name": "Cross tenant", "engine_type": "deepl", "token_id": foreignTok.ID,
	}); !strings.Contains(*msg, "does not exist") {
		t.Errorf("cross-tenant key refused with %q, want the same answer as a missing key", *msg)
	}

	// No key at all.
	if msg := post(map[string]any{
		"name": "Keyless", "engine_type": "deepl",
	}); !strings.Contains(*msg, "key") {
		t.Errorf("refusal %q should say a key is needed", *msg)
	}

	if engines, err := q.ListMTEngines(context.Background(), tenant.ID); err != nil || len(engines) != 0 {
		t.Errorf("a rejected create left %v behind (err %v)", engines, err)
	}
}

// Options are a per-provider set, so the server has to reject one provider's
// knob on another's engine and a value outside the enum. Neither is catchable
// on the client alone, and both are a 400 from the provider at translate time.
func TestIntegrationEngineRejectsBadOptions(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "BadOptTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@badopt.test", "pass", "owner")
	tok := testutil.SeedAIVendorToken(t, q, tenant.ID, "deepl", "EU", "ENC(dk-eu)")

	cases := []struct {
		name     string
		settings map[string]any
		want     string
	}{
		// `model` is Google's; DeepL calls the same idea `model_type`.
		{"another provider's option", map[string]any{"model": "nmt"}, "not a setting"},
		{"a value outside the enum", map[string]any{"formality": "very"}, "invalid value"},
		{"the wrong type", map[string]any{"preserve_formatting": "yes"}, "true or false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-engines", map[string]any{
				"name": "Engine", "engine_type": "deepl", "token_id": tok.ID, "settings": c.settings,
			}, owner.ID, tenant.ID, "owner")
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
			if !strings.Contains(rec.Body.String(), c.want) {
				t.Errorf("refused with %q, want it to mention %q", rec.Body.String(), c.want)
			}
		})
	}

	// ...and a valid set goes through, so the checks above are a filter rather
	// than a wall.
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-engines", map[string]any{
		"name": "Engine", "engine_type": "deepl", "token_id": tok.ID,
		"settings": map[string]any{"formality": "prefer_more", "glossary_id": "gl-1", "preserve_formatting": true},
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
}

// An update REPLACES the options, so clearing one is sending the object without
// it. A merge would make a cleared option unclearable through the form.
func TestIntegrationEngineUpdateReplacesOptions(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "UpdateOptTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@updateopt.test", "pass", "owner")
	tok := testutil.SeedAIVendorToken(t, q, tenant.ID, "deepl", "EU", "ENC(dk-eu)")

	engine, err := q.CreateMTEngine(context.Background(), db.CreateMTEngineParams{
		Name: "DeepL", EngineType: "deepl", Enabled: true, TenantID: tenant.ID,
		TokenID: &tok.ID, Settings: []byte(`{"formality":"more","glossary_id":"gl-1"}`),
	})
	if err != nil {
		t.Fatalf("seed engine: %v", err)
	}

	rec := testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/mt-engines/%d", engine.ID),
		map[string]any{"settings": map[string]any{"formality": "less"}}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var out engineResp
	testutil.DecodeJSON(t, rec, &out)
	if out.Data.Settings["formality"] != "less" {
		t.Errorf("formality = %v, want the new value", out.Data.Settings["formality"])
	}
	if _, still := out.Data.Settings["glossary_id"]; still {
		t.Error("glossary_id survived an update that omitted it — the form could never clear it")
	}

	// An update with no settings at all keeps what is stored, so a rename does
	// not silently wipe the options.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/mt-engines/%d", engine.ID),
		map[string]any{"name": "Renamed"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &out)
	if out.Data.Settings["formality"] != "less" {
		t.Errorf("a rename lost the options: %v", out.Data.Settings)
	}
}

// The catalog is what the create form renders its fields from, and now also
// what a create is validated against. It must offer the key-backed providers
// plus whatever this deployment supplies — no more, because an entry a user
// cannot actually build is a form that fails on save.
func TestCatalogFollowsWhatTheDeploymentSupplies(t *testing.T) {
	t.Setenv("LIBRETRANSLATE_URL", "http://libretranslate.internal:5000")
	t.Setenv("ENV", "development")

	byType := map[string]mtengine.CatalogEntry{}
	for _, c := range mtengine.Catalog() {
		byType[c.EngineType] = c
	}
	for _, want := range []string{"deepl", "google", "libretranslate", "mock"} {
		if _, ok := byType[want]; !ok {
			t.Errorf("%s missing from the catalog on a dev deployment that supplies it", want)
		}
	}
	// The key-backed ones ask for a key and offer options; the supplied ones do
	// neither — that pairing is what the form renders from.
	for _, c := range byType {
		if c.Name == "" {
			t.Errorf("%s has no name to render", c.EngineType)
		}
		switch c.EngineType {
		case "deepl", "google":
			if !c.NeedsToken {
				t.Errorf("%s is offered without a key — nothing would authenticate it", c.EngineType)
			}
			if len(c.Params) == 0 {
				t.Errorf("%s offers no options, so the form has nothing to render", c.EngineType)
			}
			for _, p := range c.Params {
				if p.Label == "" || p.Description == "" {
					t.Errorf("%s option %q has no label or description to render", c.EngineType, p.Key)
				}
			}
		default:
			if c.NeedsToken {
				t.Errorf("%s asks for a key it cannot use", c.EngineType)
			}
		}
	}
}

// A deployment that supplies neither must offer neither. Production is the case
// that matters: an engine which fabricates translations must not be one request
// from live work, and the create path checks the catalog.
func TestCatalogOffersNoBuiltInsInProduction(t *testing.T) {
	t.Setenv("LIBRETRANSLATE_URL", "")
	t.Setenv("ENV", "production")

	for _, c := range mtengine.Catalog() {
		if c.EngineType == "mock" || c.EngineType == "libretranslate" {
			t.Errorf("%s is offered in production, where the deployment supplies neither", c.EngineType)
		}
	}
}

// ...and the create path refuses it, not just the form. A UI constraint is not
// a constraint.
func TestIntegrationCreateRefusesATypeTheDeploymentDoesNotSupply(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "NoMockTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@nomock.test", "pass", "owner")
	t.Setenv("LIBRETRANSLATE_URL", "")
	t.Setenv("ENV", "production")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-engines",
		map[string]any{"name": "Sneaky mock", "engine_type": "mock"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "not available") {
		t.Errorf("refused with %q — it should say the deployment does not supply it", rec.Body.String())
	}
}

// A supplied engine built by hand gets its endpoint from the environment. The
// request cannot carry one — LibreTranslate's URL must never be a field — so
// without this the engine would be created unable to reach anything.
func TestIntegrationCreatedBuiltInTakesTheDeploymentEndpoint(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "RebuildTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@rebuild.test", "pass", "owner")
	const url = "http://libretranslate.internal:5000"
	t.Setenv("LIBRETRANSLATE_URL", url)
	t.Setenv("ENV", "development")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-engines", map[string]any{
		"name": "Our LibreTranslate", "engine_type": "libretranslate",
		// Ignored on purpose: an endpoint a user supplies is exactly what the
		// standing rule forbids.
		"api_url": "http://evil.example.com",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var out engineResp
	testutil.DecodeJSON(t, rec, &out)

	made, err := q.GetMTEngine(context.Background(), db.GetMTEngineParams{ID: out.Data.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("reload engine: %v", err)
	}
	if made.ApiUrl == nil || *made.ApiUrl != url {
		t.Errorf("api_url = %v, want the deployment's %q", made.ApiUrl, url)
	}
	if made.TokenID != nil {
		t.Errorf("a built-in took a key (%v) — it needs none", made.TokenID)
	}
	// Built by hand, so it reads as the user's rather than as provisioned.
	if out.Data.ProvisionedBy != "" {
		t.Errorf("provisioned_by = %q, want the hand-made engine to read as the user's", out.Data.ProvisionedBy)
	}
}

// The update path owes the same two guarantees the create path gives, and for
// the same reason: a UI constraint is not a constraint, and an endpoint or a
// type that arrives in a request is one PATCH away from live work.
//
// A deployment engine is the org's to rename, switch off and delete.
// Its TYPE and its ENDPOINT are not the org's —
// both come from the deployment.

func seedBuiltIn(t *testing.T, router http.Handler, tenantID, userID int64, url string) int64 {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/mt-engines", map[string]any{
		"name": "Our LibreTranslate", "engine_type": "libretranslate",
	}, userID, tenantID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var out engineResp
	testutil.DecodeJSON(t, rec, &out)
	return out.Data.ID
}

// Retyping an engine to something the deployment does not supply is the create
// refusal's twin. Production supplies no mock, and a mock fabricates its
// translations — reaching one by PATCH would put canned text into live jobs.
func TestIntegrationUpdateRefusesATypeTheDeploymentDoesNotSupply(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "RetypeTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@retype.test", "pass", "owner")
	engine := testutil.SeedMTEngine(t, q, tenant.ID, "Our LibreTranslate", "libretranslate")

	t.Setenv("LIBRETRANSLATE_URL", "")
	t.Setenv("ENV", "production")

	rec := testutil.DoRequestWithRole(t, router, "PATCH",
		fmt.Sprintf("/api/mt-engines/%d", engine.ID),
		map[string]any{"engine_type": "mock"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "not available") {
		t.Errorf("refused with %q — it should say the deployment does not supply it", rec.Body.String())
	}

	after, err := q.GetMTEngine(context.Background(), db.GetMTEngineParams{ID: engine.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("reload engine: %v", err)
	}
	if after.EngineType != "libretranslate" {
		t.Errorf("engine_type = %q — a refused update must change nothing", after.EngineType)
	}
}

// An endpoint in an update request is ignored, exactly as it is on create.
// Honouring one would let a manager aim the deployment's engine at a host of
// their choosing: the pipeline POSTs the tenant's source text to it, and
// GET /{id}/languages fetches it from the server and hands back what it finds.
func TestIntegrationUpdateIgnoresAnApiUrlFromTheRequest(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "EndpointTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@endpoint.test", "pass", "owner")
	const url = "http://libretranslate.internal:5000"
	t.Setenv("LIBRETRANSLATE_URL", url)
	t.Setenv("ENV", "development")
	engineID := seedBuiltIn(t, router, tenant.ID, owner.ID, url)

	rec := testutil.DoRequestWithRole(t, router, "PATCH",
		fmt.Sprintf("/api/mt-engines/%d", engineID),
		map[string]any{"name": "Renamed", "api_url": "http://evil.example.com"},
		owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	after, err := q.GetMTEngine(context.Background(), db.GetMTEngineParams{ID: engineID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("reload engine: %v", err)
	}
	if after.ApiUrl == nil || *after.ApiUrl != url {
		t.Errorf("api_url = %v, want the deployment's %q — a request may not set it", after.ApiUrl, url)
	}
	// The rename still lands: the name IS the org's.
	if after.Name != "Renamed" {
		t.Errorf("name = %q, want %q — renaming a deployment engine is allowed", after.Name, "Renamed")
	}
}

// Retyping to a type the deployment DOES supply moves the endpoint with it.
// Keeping the old one would leave a mock row pointing at LibreTranslate's
// address, or the reverse.
func TestIntegrationUpdateRetypeTakesTheNewTypesEndpoint(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "RetypeOKTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@retypeok.test", "pass", "owner")
	t.Setenv("LIBRETRANSLATE_URL", "http://libretranslate.internal:5000")
	t.Setenv("ENV", "development")
	engineID := seedBuiltIn(t, router, tenant.ID, owner.ID, "http://libretranslate.internal:5000")

	rec := testutil.DoRequestWithRole(t, router, "PATCH",
		fmt.Sprintf("/api/mt-engines/%d", engineID),
		map[string]any{"engine_type": "mock"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	after, err := q.GetMTEngine(context.Background(), db.GetMTEngineParams{ID: engineID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("reload engine: %v", err)
	}
	if after.EngineType != "mock" {
		t.Fatalf("engine_type = %q, want mock", after.EngineType)
	}
	if after.ApiUrl != nil {
		t.Errorf("api_url = %q — mock takes no endpoint, so the old one must not survive", *after.ApiUrl)
	}
}
