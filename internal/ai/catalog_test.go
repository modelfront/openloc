package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubLister serves a canned list per provider and counts calls. `reject` names
// the keys it answers 401 to, so a test can make one credential bad and another
// good.
type stubLister struct {
	mu     sync.Mutex
	calls  int32
	models map[string][]RemoteModel
	err    error
	reject map[string]bool
}

func (s *stubLister) List(_ context.Context, provider, apiKey, _ string) ([]RemoteModel, error) {
	atomic.AddInt32(&s.calls, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	if s.reject[apiKey] {
		return nil, &callFailure{status: 401, msg: "invalid API key (status 401)"}
	}
	return s.models[provider], nil
}

// stubKeys reports credentials for every tenant except those in `missing`.
// `keys` overrides the default single credential when a test needs several.
type stubKeys struct {
	missing map[int64]bool
	keys    []Credential
}

func (k stubKeys) KeysFor(_ context.Context, tenantID int64, _ string) ([]Credential, error) {
	if k.missing[tenantID] {
		return nil, ErrNoCredential
	}
	if len(k.keys) > 0 {
		return k.keys, nil
	}
	return []Credential{{TokenID: 1, APIKey: "sk-test"}}, nil
}

func newTestCatalog(models map[string][]RemoteModel, limit int) (*Catalog, *stubLister) {
	l := &stubLister{models: models}
	return NewCatalog(l, stubKeys{}, limit), l
}

var claudeFive = []RemoteModel{
	{ID: "claude-opus-5"}, {ID: "claude-sonnet-5"}, {ID: "claude-fable-5"},
	{ID: "claude-haiku-4-5-20251001"}, {ID: "claude-opus-4-8"},
}

// A page rendering on a cold process must still show models: the miss fetches
// rather than returning an empty dropdown that only fills after the next tick.
func TestCatalogFetchesOnMiss(t *testing.T) {
	c, lister := newTestCatalog(map[string][]RemoteModel{ProviderClaude: claudeFive}, DefaultModelLimit)
	got := c.Models(context.Background(), 1, ProviderClaude)
	if len(got) != len(claudeFive) {
		t.Fatalf("got %d models, want %d", len(got), len(claudeFive))
	}
	if lister.calls != 1 {
		t.Fatalf("lister called %d times, want 1", lister.calls)
	}
	// Second read is served from cache.
	c.Models(context.Background(), 1, ProviderClaude)
	if lister.calls != 1 {
		t.Errorf("a cached read refetched: %d calls", lister.calls)
	}
}

// The AI settings page renders every vendor at once. Without coalescing, a cold
// cache turns one page load into one provider call per concurrent reader.
func TestCatalogCoalescesConcurrentMisses(t *testing.T) {
	c, lister := newTestCatalog(map[string][]RemoteModel{ProviderClaude: claudeFive}, DefaultModelLimit)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Models(context.Background(), 1, ProviderClaude)
		}()
	}
	wg.Wait()
	if lister.calls != 1 {
		t.Errorf("8 concurrent misses made %d provider calls, want 1", lister.calls)
	}
}

// Display is capped; validation is not. A model that scrolls out of the top N
// is still one an AI engine may have pinned, and re-saving that row must not
// fail because the picker got shorter.
func TestCatalogDisplayCapDoesNotNarrowValidation(t *testing.T) {
	c, _ := newTestCatalog(map[string][]RemoteModel{ProviderClaude: claudeFive}, 2)
	shown := c.Models(context.Background(), 1, ProviderClaude)
	if len(shown) != 2 {
		t.Fatalf("display returned %d models, want the 2-model cap", len(shown))
	}
	last := claudeFive[len(claudeFive)-1].ID
	if !c.Valid(context.Background(), 1, ProviderClaude, last) {
		t.Errorf("%s is outside the display cap but still selectable — Valid must see the full list", last)
	}
}

// A vendor blip must not empty a working dropdown. Fetch is called directly:
// Models would be served from cache and never reach the failing provider, so it
// can't tell "kept the old list" from "never tried".
func TestCatalogKeepsLastGoodListOnFetchFailure(t *testing.T) {
	c, lister := newTestCatalog(map[string][]RemoteModel{ProviderClaude: claudeFive}, DefaultModelLimit)
	c.Models(context.Background(), 1, ProviderClaude)

	lister.mu.Lock()
	lister.err = errors.New("provider is down")
	lister.mu.Unlock()

	// Fetch short-circuits on a cached entry, so evict to make the provider call
	// actually happen.
	c.mu.Lock()
	delete(c.entries, catalogKey{tenantID: 1, provider: ProviderClaude})
	c.mu.Unlock()

	if _, err := c.Fetch(context.Background(), 1, ProviderClaude); err == nil {
		t.Fatal("a provider failure must surface as an error, not an empty list")
	}
	// A failed fetch stores nothing, so a later successful one repopulates. Fetch
	// directly rather than Models: the failure just armed the cooldown, which is
	// exactly what stops Models from retrying for the next minute.
	lister.mu.Lock()
	lister.err = nil
	lister.mu.Unlock()
	if _, err := c.Fetch(context.Background(), 1, ProviderClaude); err != nil {
		t.Fatalf("refetch after recovery failed: %v", err)
	}
	if got := c.Models(context.Background(), 1, ProviderClaude); len(got) != len(claudeFive) {
		t.Errorf("catalog did not recover after the provider came back, got %d models", len(got))
	}
}

// The refresher is what holds a working list across a vendor blip: it only
// stores on success, so a failed pass leaves the previous entry untouched.
func TestRefreshFailureLeavesPreviousList(t *testing.T) {
	lister := &stubLister{models: map[string][]RemoteModel{ProviderClaude: claudeFive}}
	c := NewCatalog(lister, stubKeys{}, DefaultModelLimit)
	c.Models(context.Background(), 1, ProviderClaude)

	lister.mu.Lock()
	lister.err = errors.New("provider is down")
	lister.mu.Unlock()
	NewRefresher(c, time.Hour, time.Hour).RefreshAll(context.Background())

	if got := c.Models(context.Background(), 1, ProviderClaude); len(got) != len(claudeFive) {
		t.Errorf("a failed refresh emptied a working list, got %d models", len(got))
	}
}

// The refresher's work list is the CACHE, not the database: it renews what
// someone is using and touches nothing else. This is what keeps refresh cost
// proportional to active organizations rather than to every org that ever
// stored a key.
func TestRefreshOnlyTouchesWarmEntries(t *testing.T) {
	lister := &stubLister{models: map[string][]RemoteModel{
		ProviderClaude: claudeFive,
		ProviderOpenAI: {{ID: "gpt-4o"}},
	}}
	c := NewCatalog(lister, stubKeys{}, DefaultModelLimit)

	// Tenant 1 uses Claude. Tenant 2 has keys but has never opened anything.
	c.Models(context.Background(), 1, ProviderClaude)
	before := lister.calls

	NewRefresher(c, time.Hour, time.Hour).RefreshAll(context.Background())

	if got := lister.calls - before; got != 1 {
		t.Errorf("refresh made %d provider calls, want 1 (only the warm pair)", got)
	}
	if c.Len() != 1 {
		t.Errorf("refresh cached %d pairs, want only the warm one", c.Len())
	}
}

// An entry nobody reads is dropped, so an org that looked once in March stops
// costing a call every 30 minutes forever. The next read fills it again.
func TestRefreshEvictsIdleEntries(t *testing.T) {
	lister := &stubLister{models: map[string][]RemoteModel{ProviderClaude: claudeFive}}
	c := NewCatalog(lister, stubKeys{}, DefaultModelLimit)
	c.Models(context.Background(), 1, ProviderClaude)
	if c.Len() != 1 {
		t.Fatalf("setup: cached %d pairs, want 1", c.Len())
	}

	// idleTTL of 0 makes every entry idle.
	NewRefresher(c, time.Hour, time.Nanosecond).RefreshAll(context.Background())

	if c.Len() != 0 {
		t.Errorf("idle entry survived eviction: %d cached", c.Len())
	}
	// And it comes back on the next read.
	if got := c.Models(context.Background(), 1, ProviderClaude); len(got) != len(claudeFive) {
		t.Errorf("an evicted pair did not refill on read, got %d models", len(got))
	}
}

// A refresh is not a read. If a refetch reset lastUsed, an entry nobody opens
// would be kept alive by the very job meant to age it out, and eviction would
// never fire for anything once cached.
//
// The ages are chosen so the two behaviours disagree: the entry is 50 minutes
// idle, a 1h pass keeps and refreshes it, and a later 40m pass must still see it
// as 50 minutes idle — not as freshly used.
func TestRefreshDoesNotCountAsUse(t *testing.T) {
	lister := &stubLister{models: map[string][]RemoteModel{ProviderClaude: claudeFive}}
	c := NewCatalog(lister, stubKeys{}, DefaultModelLimit)
	c.Models(context.Background(), 1, ProviderClaude)

	key := catalogKey{tenantID: 1, provider: ProviderClaude}
	c.mu.RLock()
	e := c.entries[key]
	c.mu.RUnlock()
	e.lastUsed.Store(time.Now().Add(-50 * time.Minute).UnixNano())

	// Within the TTL: kept and refreshed.
	NewRefresher(c, time.Hour, time.Hour).RefreshAll(context.Background())
	if c.Len() != 1 {
		t.Fatalf("refresh dropped an entry that is still inside the TTL: %d cached", c.Len())
	}
	// Still 50 minutes since anyone READ it, so a 40-minute TTL evicts it.
	NewRefresher(c, time.Hour, 40*time.Minute).RefreshAll(context.Background())
	if c.Len() != 0 {
		t.Error("the refresh reset lastUsed — an unread entry would stay warm forever")
	}
}

// A key the vendor rejects must not make every page load pay the fetch timeout.
// The dev seed ships a placeholder key, so this is the default local experience.
func TestCatalogCoolsDownAfterAFailedFetch(t *testing.T) {
	lister := &stubLister{err: errors.New("invalid API key (status 401)")}
	c := NewCatalog(lister, stubKeys{}, DefaultModelLimit)
	for i := 0; i < 5; i++ {
		if got := c.Models(context.Background(), 1, ProviderClaude); len(got) != 0 {
			t.Fatalf("a failing provider must yield no models, got %v", ids(got))
		}
	}
	if lister.calls != 1 {
		t.Errorf("5 page loads made %d provider calls after a failure, want 1", lister.calls)
	}
}

// With no list to check against, blocking every AI-engine save is worse than
// accepting an id that would fail later with a clear provider error.
func TestCatalogValidIsPermissiveWithoutAList(t *testing.T) {
	c := NewCatalog(nil, nil, DefaultModelLimit)
	if !c.Valid(context.Background(), 1, ProviderClaude, "claude-opus-5") {
		t.Error("an unfetched catalog must not reject a model it cannot check")
	}
	// A populated one is strict.
	c.Set(1, ProviderClaude, classifyAll(ProviderClaude, claudeFive))
	if c.Valid(context.Background(), 1, ProviderClaude, "claude-nonexistent") {
		t.Error("a populated catalog must reject a model the tenant cannot reach")
	}
	if !c.Valid(context.Background(), 1, ProviderClaude, "claude-opus-5") {
		t.Error("a model in the list must stay valid")
	}
	// MT vendors have no models at all, so nothing is valid for them.
	if c.Valid(context.Background(), 1, ProviderDeepL, "anything") {
		t.Error("an MT vendor has no selectable model")
	}
}

// The catalog is what one tenant's key can reach. Leaking another tenant's list
// would advertise models the caller's key cannot use — and reveal what that
// other org pays for.
func TestCatalogIsPerTenant(t *testing.T) {
	c := NewCatalog(nil, nil, DefaultModelLimit)
	c.Set(1, ProviderClaude, classifyAll(ProviderClaude, claudeFive))
	if got := c.Models(context.Background(), 2, ProviderClaude); len(got) != 0 {
		t.Errorf("tenant 2 sees tenant 1's models: %v", ids(got))
	}
	if c.Valid(context.Background(), 2, ProviderClaude, "claude-opus-5") == false {
		// Tenant 2 has no list, so validation is permissive — that is the
		// documented behaviour, asserted here so a change to it is deliberate.
		t.Error("tenant 2 has no list of its own, so validation stays permissive")
	}
}

// A tenant that never connected a vendor is a normal state, not an error: no
// models, and no provider call either.
func TestCatalogNoCredentialMeansNoModelsAndNoCall(t *testing.T) {
	keys := stubKeys{missing: map[int64]bool{7: true}}
	lister := &stubLister{models: map[string][]RemoteModel{ProviderClaude: claudeFive}}
	c := NewCatalog(lister, keys, DefaultModelLimit)
	for i := 0; i < 3; i++ {
		if got := c.Models(context.Background(), 7, ProviderClaude); len(got) != 0 {
			t.Fatalf("a tenant with no key must see no models, got %v", ids(got))
		}
	}
	if lister.calls != 0 {
		t.Errorf("no credential means no provider call, got %d", lister.calls)
	}

	// Connecting a key is exactly when a user expects models to appear. Caching
	// the empty result would hide them until the next refresh tick — up to 30
	// minutes of an empty dropdown right after a successful "Connect".
	delete(keys.missing, 7)
	if got := c.Models(context.Background(), 7, ProviderClaude); len(got) != len(claudeFive) {
		t.Errorf("models did not appear right after a key was added, got %v", ids(got))
	}
}

// Vendors without a model list must never trigger a fetch — the AI page renders
// every vendor, and extensions, DeepL and Google have nothing to list.
func TestCatalogSkipsVendorsWithoutAModelList(t *testing.T) {
	c, lister := newTestCatalog(map[string][]RemoteModel{}, DefaultModelLimit)
	for _, p := range []string{testExtProvider, ProviderDeepL, ProviderGoogle} {
		if got := c.Models(context.Background(), 1, p); len(got) != 0 {
			t.Errorf("%s should expose no models, got %v", p, ids(got))
		}
	}
	if lister.calls != 0 {
		t.Errorf("made %d provider calls for vendors with no model list", lister.calls)
	}
}

// Each model carries the parameters its family accepts — that payload is what
// the settings form renders.
func TestCatalogModelsCarryParams(t *testing.T) {
	c, _ := newTestCatalog(map[string][]RemoteModel{ProviderClaude: claudeFive}, DefaultModelLimit)
	for _, m := range c.Models(context.Background(), 1, ProviderClaude) {
		if len(m.Params) == 0 {
			t.Errorf("%s carries no params", m.ID)
		}
	}
}

func ExampleCatalog_Models() {
	c := NewCatalog(nil, nil, DefaultModelLimit)
	c.Set(42, ProviderClaude, classifyAll(ProviderClaude, []RemoteModel{
		{ID: "claude-opus-5", DisplayName: "Claude Opus 5"},
	}))
	for _, m := range c.Models(context.Background(), 42, ProviderClaude) {
		fmt.Println(m.ID, "-", m.Label)
	}
	// Output: claude-opus-5 - Claude Opus 5
}

// A tenant can hold several keys for a vendor. If the preferred one is dead —
// a stale seed, a revoked token — the vendor must still list the models a
// working key can reach, instead of going silently empty.
func TestCatalogFallsBackToAWorkingKey(t *testing.T) {
	lister := &stubLister{
		models: map[string][]RemoteModel{ProviderClaude: claudeFive},
		reject: map[string]bool{"sk-stale": true},
	}
	keys := stubKeys{keys: []Credential{
		{TokenID: 1, APIKey: "sk-stale"}, // preferred, and dead
		{TokenID: 2, APIKey: "sk-good"},
	}}
	c := NewCatalog(lister, keys, DefaultModelLimit)

	got := c.Models(context.Background(), 1, ProviderClaude)
	if len(got) != len(claudeFive) {
		t.Fatalf("a dead preferred key hid the working one: got %v", ids(got))
	}
	if lister.calls != 2 {
		t.Errorf("expected the stale key then the good one (2 calls), got %d", lister.calls)
	}
	// A recovered pair reports no failure.
	if reason := c.FailureReason(1, ProviderClaude); reason != "" {
		t.Errorf("FailureReason = %q, want empty after a successful fallback", reason)
	}
}

// Only every key failing is a failure, and then the user gets a reason instead
// of an unexplained empty dropdown.
func TestCatalogReportsWhyTheListIsEmpty(t *testing.T) {
	lister := &stubLister{
		models: map[string][]RemoteModel{ProviderClaude: claudeFive},
		reject: map[string]bool{"sk-stale": true, "sk-also-stale": true},
	}
	keys := stubKeys{keys: []Credential{
		{TokenID: 1, APIKey: "sk-stale"},
		{TokenID: 2, APIKey: "sk-also-stale"},
	}}
	c := NewCatalog(lister, keys, DefaultModelLimit)

	if got := c.Models(context.Background(), 1, ProviderClaude); len(got) != 0 {
		t.Fatalf("every key is rejected, so there are no models: got %v", ids(got))
	}
	if got := c.FailureReason(1, ProviderClaude); got != "The API key was rejected." {
		t.Errorf("FailureReason = %q, want the rejected-key sentence", got)
	}
}

// The reason is rendered in the UI, so it must never carry the provider's
// response body — that can quote account details.
func TestFailureReasonDoesNotLeakTheProviderBody(t *testing.T) {
	err := &callFailure{status: 500, msg: `provider returned status 500: {"org":"acme","secret_hint":"sk-live-123"}`}
	got := failureReason(err)
	if strings.Contains(got, "acme") || strings.Contains(got, "sk-live") {
		t.Fatalf("reason leaked the provider body: %q", got)
	}
	if got != "The provider returned an error (status 500)." {
		t.Errorf("reason = %q", got)
	}
}

// A pair with no credential at all is not a failure — nothing to explain.
func TestNoCredentialIsNotAFailureReason(t *testing.T) {
	c := NewCatalog(&stubLister{}, stubKeys{missing: map[int64]bool{7: true}}, DefaultModelLimit)
	c.Models(context.Background(), 7, ProviderClaude)
	if got := c.FailureReason(7, ProviderClaude); got != "" {
		t.Errorf("FailureReason = %q, want empty for a vendor with no key", got)
	}
}
