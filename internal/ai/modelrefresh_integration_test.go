// The model catalog against a real database: the key lookup
// that decides WHICH credential a tenant's list comes from, per-tenant
// isolation, and the fallback past a dead key. The provider itself is stubbed —
// no external calls.
//
// The periodic refresher barely appears here: it reads the CACHE, not the
// database, so its work-list behaviour is covered by unit tests. Everything that
// touches SQL runs through the catalog's key loader.
package ai_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/secrets"
	"openloc/internal/testutil"
)

// recordingLister answers with a fixed list and remembers which key each call
// carried, so a test can assert WHICH credential a tenant's catalog came from.
//
// The mutex is load-bearing: the catalog fetches concurrently, so an unguarded
// map write is a hard runtime panic rather than a test failure.
type recordingLister struct {
	mu           sync.Mutex
	keysByTenant map[string]string
	models       map[string][]ai.RemoteModel
	// reject names the keys answered with a 401, so a test can leave a dead
	// token beside a working one.
	reject map[string]bool
}

func (l *recordingLister) List(_ context.Context, provider, apiKey, _ string) ([]ai.RemoteModel, error) {
	l.mu.Lock()
	l.keysByTenant[provider+"|"+apiKey] = apiKey
	reject := l.reject[apiKey]
	models := l.models[provider]
	l.mu.Unlock()
	if reject {
		return nil, errors.New("invalid API key (status 401)")
	}
	return models, nil
}

// calls returns a snapshot of the recorded calls.
func (l *recordingLister) calls() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]string, len(l.keysByTenant))
	for k, v := range l.keysByTenant {
		out[k] = v
	}
	return out
}

func newRecordingLister() *recordingLister {
	return &recordingLister{
		keysByTenant: map[string]string{},
		reject:       map[string]bool{},
		models: map[string][]ai.RemoteModel{
			ai.ProviderOpenAI: {{ID: "gpt-5"}, {ID: "gpt-4o"}, {ID: "dall-e-3"}},
			ai.ProviderClaude: {{ID: "claude-opus-5", DisplayName: "Claude Opus 5"}},
		},
	}
}

func newEncryptor(t *testing.T) *secrets.Encryptor {
	t.Helper()
	enc, err := secrets.NewEncryptor(testutil.TestAISecretsKey)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

func encryptKey(t *testing.T, enc *secrets.Encryptor, plain string) string {
	t.Helper()
	out, err := enc.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIntegrationCatalogFetchesPerTenant(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	enc := newEncryptor(t)
	tenantA := testutil.SeedTenant(t, q, "CatalogA")
	tenantB := testutil.SeedTenant(t, q, "CatalogB")
	tenantC := testutil.SeedTenant(t, q, "CatalogC") // no AI vendor at all

	testutil.SeedAIVendorToken(t, q, tenantA.ID, "openai", "prod", encryptKey(t, enc, "sk-a-openai"))
	testutil.SeedAIVendorToken(t, q, tenantA.ID, "claude", "prod", encryptKey(t, enc, "sk-a-claude"))
	testutil.SeedAIVendorToken(t, q, tenantB.ID, "openai", "prod", encryptKey(t, enc, "sk-b-openai"))
	// An MT vendor is a credential too, but it publishes no model list — the
	// catalog must not try to fetch one.
	testutil.SeedAIVendorToken(t, q, tenantB.ID, "deepl", "prod", encryptKey(t, enc, "dl-key"))

	lister := newRecordingLister()
	catalog := ai.NewCatalog(lister, ai.NewDBKeyLoader(q, enc), ai.DefaultModelLimit)
	ctx := context.Background()

	// Non-text models are filtered before anything is cached.
	gotA := modelIDs(catalog.Models(ctx, tenantA.ID, ai.ProviderOpenAI))
	if len(gotA) != 2 || gotA[0] != "gpt-5" || gotA[1] != "gpt-4o" {
		t.Errorf("tenant A openai models = %v, want [gpt-5 gpt-4o]", gotA)
	}
	if got := catalog.Models(ctx, tenantA.ID, ai.ProviderClaude); len(got) != 1 {
		t.Errorf("tenant A claude models = %v, want 1", modelIDs(got))
	}
	catalog.Models(ctx, tenantB.ID, ai.ProviderOpenAI)
	catalog.Models(ctx, tenantB.ID, ai.ProviderDeepL)
	catalog.Models(ctx, tenantC.ID, ai.ProviderOpenAI)

	calls := lister.calls()
	// Each tenant's catalog is fetched with that tenant's own key.
	if _, ok := calls["openai|sk-a-openai"]; !ok {
		t.Error("tenant A's OpenAI catalog was not fetched with tenant A's key")
	}
	if _, ok := calls["openai|sk-b-openai"]; !ok {
		t.Error("tenant B's OpenAI catalog was not fetched with tenant B's key")
	}
	if _, ok := calls["deepl|dl-key"]; ok {
		t.Error("DeepL has no model list — the catalog must not call it")
	}

	// Tenant B never connected Claude, so it gets no Claude models — not
	// tenant A's.
	if got := catalog.Models(ctx, tenantB.ID, ai.ProviderClaude); len(got) != 0 {
		t.Errorf("tenant B sees claude models it has no key for: %v", modelIDs(got))
	}
	// A tenant with no AI vendor at all is a normal state.
	if got := catalog.Models(ctx, tenantC.ID, ai.ProviderOpenAI); len(got) != 0 {
		t.Errorf("tenant C has no key and should see no models, got %v", modelIDs(got))
	}
}

// A tenant can hold several keys for one vendor. The catalog must prefer the one
// the runtime actually spends — the token marked use_for_chat — or the picker
// offers models the chosen key can't reach.
func TestIntegrationCatalogPrefersTheChatKey(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	enc := newEncryptor(t)
	tenant := testutil.SeedTenant(t, q, "ChatKey")
	testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "first", encryptKey(t, enc, "sk-oldest"))
	second := testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "second", encryptKey(t, enc, "sk-chosen"))
	if err := q.SetChatVendorToken(context.Background(), db.SetChatVendorTokenParams{
		ID: second.ID, VendorID: second.VendorID,
	}); err != nil {
		t.Fatal(err)
	}

	lister := newRecordingLister()
	catalog := ai.NewCatalog(lister, ai.NewDBKeyLoader(q, enc), ai.DefaultModelLimit)
	catalog.Models(context.Background(), tenant.ID, ai.ProviderOpenAI)

	calls := lister.calls()
	if _, ok := calls["openai|sk-chosen"]; !ok {
		t.Errorf("catalog fetched with the wrong key; calls: %v", calls)
	}
	// The preferred key answered, so no other key is tried.
	if len(calls) != 1 {
		t.Errorf("a working preferred key should end the search, got %d calls: %v", len(calls), calls)
	}
}

// Pins that the catalog falls back past a key that 401s: a
// tenant whose seeded placeholder key is OLDER than the real key they just
// added. The preferred-key rule picks the placeholder, it 401s, and the working
// key next to it must still list the vendor's models.
func TestIntegrationCatalogFallsBackPastAStaleKey(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	enc := newEncryptor(t)
	tenant := testutil.SeedTenant(t, q, "StaleSeed")
	// Order matters: the placeholder is created first, so it wins the "oldest"
	// tie-break that decides the preferred key.
	testutil.SeedAIVendorToken(t, q, tenant.ID, "claude", "Default", encryptKey(t, enc, "sk-placeholder"))
	testutil.SeedAIVendorToken(t, q, tenant.ID, "claude", "CLD", encryptKey(t, enc, "sk-real"))

	lister := newRecordingLister()
	lister.reject["sk-placeholder"] = true
	catalog := ai.NewCatalog(lister, ai.NewDBKeyLoader(q, enc), ai.DefaultModelLimit)

	got := modelIDs(catalog.Models(context.Background(), tenant.ID, ai.ProviderClaude))
	if len(got) != 1 || got[0] != "claude-opus-5" {
		t.Fatalf("the working key was never tried; models = %v, calls = %v", got, lister.calls())
	}
	calls := lister.calls()
	if _, ok := calls["claude|sk-placeholder"]; !ok {
		t.Error("the preferred key should be tried first")
	}
	if _, ok := calls["claude|sk-real"]; !ok {
		t.Error("the fallback key was never tried")
	}
	// A pair that recovered reports no failure to the UI.
	if reason := catalog.FailureReason(tenant.ID, ai.ProviderClaude); reason != "" {
		t.Errorf("FailureReason = %q, want empty after a successful fallback", reason)
	}
}

// End to end over a real database: a warm entry is renewed in place, so a model
// the vendor ships between passes appears without anyone reloading.
func TestIntegrationRefreshRenewsWarmEntries(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	enc := newEncryptor(t)
	tenant := testutil.SeedTenant(t, q, "Warm")
	testutil.SeedAIVendorToken(t, q, tenant.ID, "claude", "prod", encryptKey(t, enc, "sk-warm"))

	lister := newRecordingLister()
	catalog := ai.NewCatalog(lister, ai.NewDBKeyLoader(q, enc), ai.DefaultModelLimit)
	ctx := context.Background()
	if got := catalog.Models(ctx, tenant.ID, ai.ProviderClaude); len(got) != 1 {
		t.Fatalf("setup: got %v", modelIDs(got))
	}

	// The vendor ships a model.
	lister.mu.Lock()
	lister.models[ai.ProviderClaude] = append(lister.models[ai.ProviderClaude],
		ai.RemoteModel{ID: "claude-opus-6", DisplayName: "Claude Opus 6"})
	lister.mu.Unlock()

	ai.NewRefresher(catalog, time.Hour, time.Hour).RefreshAll(ctx)

	if got := modelIDs(catalog.Models(ctx, tenant.ID, ai.ProviderClaude)); len(got) != 2 {
		t.Errorf("refresh did not pick up the new model: %v", got)
	}
}

func modelIDs(models []ai.Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.ID
	}
	return out
}
