package ai_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pglisten"
	"openloc/internal/testutil"
)

// listerByURL lists models only for keys on the good endpoint; every other
// endpoint fails, as a rejected key does.
type listerByURL struct{ good string }

func (l listerByURL) List(ctx context.Context, provider, apiKey, baseURL string) ([]ai.RemoteModel, error) {
	if baseURL != l.good {
		return nil, fmt.Errorf("invalid API key (status 401)")
	}
	return testutil.FakeModelLister{}.List(ctx, provider, apiKey, baseURL)
}

// keyFixture is a tenant whose OpenAI model list comes from listerByURL: keys
// on good.URL list models, every other endpoint is rejected.
type keyFixture struct {
	pool   *pgxpool.Pool
	q      db.Querier
	router chi.Router
	good   *httptest.Server
	tenant db.Tenant
	owner  db.User
}

const badEndpoint = "http://bad.example.com"

func newKeyFixture(t *testing.T) *keyFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(good.Close)
	f := &keyFixture{pool: pool, q: q, good: good}
	f.router = testutil.NewTestRouterWithCatalog(pool, nil, "", f.catalog())
	f.tenant = testutil.SeedTenant(t, q, "KeyTenant")
	f.owner = testutil.SeedUserWithRole(t, q, f.tenant.ID, "owner@keys.test", "pass", "owner")
	return f
}

// catalog is a fresh catalog over the fixture's lister, as another server
// process holds.
func (f *keyFixture) catalog() *ai.Catalog {
	return testutil.TestAICatalogWith(f.pool, listerByURL{good: f.good.URL})
}

func (f *keyFixture) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, method, path, body, f.owner.ID, f.tenant.ID, "owner")
}

// selectable counts the OpenAI models the chat picker offers.
func (f *keyFixture) selectable(t *testing.T) int {
	t.Helper()
	rec := f.do(t, "GET", "/api/chat/models", nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data []struct {
			Provider   string `json:"provider"`
			Selectable bool   `json:"selectable"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	n := 0
	for _, m := range resp.Data {
		if m.Provider == "openai" && m.Selectable {
			n++
		}
	}
	return n
}

// goodConnection creates an OpenAI connection on the good endpoint.
func (f *keyFixture) goodConnection(t *testing.T) int64 {
	t.Helper()
	rec := f.do(t, "POST", "/api/ai/vendors/openai/connections", map[string]any{"base_url": f.good.URL})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var conn struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &conn)
	return conn.Data.ID
}

// A failed model fetch holds the list empty for a cooldown. Adding a working
// key must end that at once: the chat picker is filled on the next read.
func TestIntegrationNewKeyListsChatModelsAtOnce(t *testing.T) {
	f := newKeyFixture(t)
	testutil.SeedAIVendorTokenBase(t, f.q, f.tenant.ID, "openai", "old", "ENC(whatever)", badEndpoint)
	if n := f.selectable(t); n != 0 {
		t.Fatalf("a rejected key lists %d models, want 0", n)
	}

	rec := f.do(t, "POST", "/api/ai/vendors/openai/tokens",
		map[string]any{"name": "new", "api_key": "sk-new", "connection_id": f.goodConnection(t)})
	testutil.AssertStatus(t, rec, http.StatusCreated)

	if n := f.selectable(t); n == 0 {
		t.Error("the new key's models are hidden until the failure cooldown ends")
	}
}

// Moving a rejected key to a working connection lists models at once.
func TestIntegrationMovedKeyListsChatModelsAtOnce(t *testing.T) {
	f := newKeyFixture(t)
	tok := testutil.SeedAIVendorTokenBase(t, f.q, f.tenant.ID, "openai", "old", "ENC(whatever)", badEndpoint)
	if n := f.selectable(t); n != 0 {
		t.Fatalf("a rejected key lists %d models, want 0", n)
	}

	rec := f.do(t, "PATCH", "/api/ai/vendors/openai/tokens/"+strconv.FormatInt(tok.ID, 10),
		map[string]any{"name": "old", "connection_id": f.goodConnection(t)})
	testutil.AssertStatus(t, rec, http.StatusOK)

	if n := f.selectable(t); n == 0 {
		t.Error("the moved key's models are hidden until the failure cooldown ends")
	}
}

// Pointing a connection at a working endpoint lists models at once.
func TestIntegrationNewEndpointListsChatModelsAtOnce(t *testing.T) {
	f := newKeyFixture(t)
	tok := testutil.SeedAIVendorTokenBase(t, f.q, f.tenant.ID, "openai", "old", "ENC(whatever)", badEndpoint)
	if n := f.selectable(t); n != 0 {
		t.Fatalf("a rejected key lists %d models, want 0", n)
	}

	rec := f.do(t, "PATCH", "/api/ai/vendors/openai/connections/"+strconv.FormatInt(tok.ConnectionID, 10),
		map[string]any{"base_url": f.good.URL})
	testutil.AssertStatus(t, rec, http.StatusOK)

	if n := f.selectable(t); n == 0 {
		t.Error("the new endpoint's models are hidden until the failure cooldown ends")
	}
}

// Removing the key a list was fetched with drops that list at once, rather
// than offering its models until the next refresh.
func TestIntegrationRemovedKeyDropsItsModelsAtOnce(t *testing.T) {
	f := newKeyFixture(t)
	working := testutil.SeedAIVendorTokenBase(t, f.q, f.tenant.ID, "openai", "working", "ENC(whatever)", f.good.URL)
	testutil.SeedAIVendorTokenBase(t, f.q, f.tenant.ID, "openai", "rejected", "ENC(whatever)", badEndpoint)
	if n := f.selectable(t); n == 0 {
		t.Fatal("the working key lists no models")
	}

	rec := f.do(t, "DELETE", "/api/ai/vendors/openai/tokens/"+strconv.FormatInt(working.ID, 10), nil)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	if n := f.selectable(t); n != 0 {
		t.Errorf("the removed key's %d models are still offered", n)
	}
}

// listening is a catalog as another server process holds it, receiving the
// key-change notices.
type listening struct {
	*ai.Catalog
	f *keyFixture
}

func (f *keyFixture) listening(t *testing.T) listening {
	t.Helper()
	c := f.catalog()
	l := pglisten.New()
	c.ListenOn(l)
	testutil.RunListener(t, l, testutil.ListenerConfig(f.pool.Config().ConnConfig, "ai-catalog-test"))
	o := listening{c, f}
	o.sync(t)
	return o
}

// sync returns once every notice committed so far has been handled: notices
// arrive in commit order, so it sends a probe and waits until the probe's
// pair is forgotten. The probe tenant holds no key, so a forgotten probe
// reads as an empty list.
func (o listening) sync(t *testing.T) {
	t.Helper()
	const probeTenant = -1
	o.Set(probeTenant, ai.ProviderClaude, []ai.Model{{ID: "claude-opus-5"}})
	testutil.WaitFor(t, func() bool {
		if _, err := o.f.pool.Exec(context.Background(), `SELECT pg_notify($1, $2)`,
			ai.CatalogNotifyChannel, fmt.Sprintf("%d|%s", probeTenant, ai.ProviderClaude)); err != nil {
			t.Fatal(err)
		}
		return len(o.Models(context.Background(), probeTenant, ai.ProviderClaude)) == 0
	}, "the other process receiving notices")
}

// A key added through one server process ends the failure cooldown on every
// other process too.
func TestIntegrationNewKeyReachesEveryProcess(t *testing.T) {
	f := newKeyFixture(t)
	other := f.listening(t)
	testutil.SeedAIVendorTokenBase(t, f.q, f.tenant.ID, "openai", "old", "ENC(whatever)", badEndpoint)
	other.sync(t)

	ctx := context.Background()
	if got := other.Models(ctx, f.tenant.ID, ai.ProviderOpenAI); len(got) != 0 {
		t.Fatalf("a rejected key lists %d models on the other process, want 0", len(got))
	}

	rec := f.do(t, "POST", "/api/ai/vendors/openai/tokens",
		map[string]any{"name": "new", "api_key": "sk-new", "connection_id": f.goodConnection(t)})
	testutil.AssertStatus(t, rec, http.StatusCreated)

	testutil.WaitFor(t, func() bool {
		return len(other.Models(ctx, f.tenant.ID, ai.ProviderOpenAI)) > 0
	}, "the other process listing the new key's models")
}

// Every write that changes which keys or endpoint a list is fetched with
// reaches the other processes; a rename changes nothing they cache.
func TestIntegrationKeyChangesReachEveryProcess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sql    string
		forget bool
	}{
		{"a new key value", `UPDATE ai_vendor_tokens SET api_key_encrypted = 'ENC(new)' WHERE id = $1`, true},
		{"a key moved to another connection", `UPDATE ai_vendor_tokens t SET connection_id = c.id FROM ai_connections c WHERE c.vendor_id = t.vendor_id AND c.name = 'spare' AND t.id = $1`, true},
		{"a key chosen for chat", `UPDATE ai_vendor_tokens SET use_for_chat = true WHERE id = $1`, true},
		{"a key removed", `DELETE FROM ai_vendor_tokens WHERE id = $1`, true},
		{"a new endpoint", `UPDATE ai_connections SET base_url = 'https://acme.openai.azure.com/openai' WHERE id = (SELECT connection_id FROM ai_vendor_tokens WHERE id = $1)`, true},
		{"a key renamed", `UPDATE ai_vendor_tokens SET name = 'renamed' WHERE id = $1`, false},
		{"a connection renamed", `UPDATE ai_connections SET name = 'renamed' WHERE id = (SELECT connection_id FROM ai_vendor_tokens WHERE id = $1)`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newKeyFixture(t)
			tok := testutil.SeedAIVendorTokenBase(t, f.q, f.tenant.ID, "openai", "key", "ENC(old)", f.good.URL)
			if _, err := f.q.CreateAIConnection(context.Background(), db.CreateAIConnectionParams{
				VendorID: tok.VendorID, Name: "spare", BaseUrl: f.good.URL,
			}); err != nil {
				t.Fatal(err)
			}
			other := f.listening(t)
			// A list no provider returns, so a refetch cannot bring it back.
			other.Set(f.tenant.ID, ai.ProviderOpenAI, []ai.Model{{ID: "gpt-cached"}})

			if _, err := f.pool.Exec(context.Background(), tc.sql, tok.ID); err != nil {
				t.Fatal(err)
			}
			other.sync(t)

			cached := false
			for _, m := range other.Models(context.Background(), f.tenant.ID, ai.ProviderOpenAI) {
				cached = cached || m.ID == "gpt-cached"
			}
			if cached == tc.forget {
				t.Errorf("cached list kept = %v, want %v", cached, !tc.forget)
			}
		})
	}
}
