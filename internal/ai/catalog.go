package ai

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"openloc/internal/logging"
)

// Per-tenant model catalog: what each tenant's own key can
// reach at each vendor, fetched from the vendor's API and held in memory. Entries
// arrive on first use and Refresher renews the ones still being read.
//
// Scope: the catalog decides which model ids are OFFERED. It never decides how a
// request is built — that comes from familyFromID, a pure function of the id, so
// a stale or failed refresh can't change the shape of an outbound call.

// DefaultModelLimit is how many models a vendor shows in a picker. The full
// fetched list is kept for validation; only display is capped.
const DefaultModelLimit = 8

// DefaultRefreshInterval is how often Refresher re-reads the cached models.
const DefaultRefreshInterval = 30 * time.Minute

// lazyFetchTimeout bounds the on-demand fetch a cache miss triggers. Tight,
// because it runs INSIDE an HTTP request and the AI settings page fetches two
// vendors in sequence — the page's worst case is twice this. A healthy vendor
// answers /v1/models in well under a second; an unreachable one should give the
// page an empty dropdown rather than hold it open. The periodic refresh runs in
// the background and is not bound by this.
const lazyFetchTimeout = 3 * time.Second

// failCooldown is how long a failed fetch suppresses the next on-demand retry.
// Without it, a key the vendor rejects makes every page load pay the full
// lazyFetchTimeout for each vendor — a dev seed with a placeholder key, or a
// revoked key in production, would put a multi-second stall on every open of the
// AI settings page. The periodic refresh retries on its own schedule regardless.
const failCooldown = time.Minute

// Credential is one usable key for a vendor, with the endpoint it authenticates
// against.
type Credential struct {
	TokenID int64
	APIKey  string
	BaseURL string
}

// KeyLoader resolves the credentials a tenant reaches one vendor with, best
// first — the explicit chat key, then oldest. Returns ErrNoCredential when the
// tenant has no key for that provider.
//
// A list rather than one key: a tenant can hold several, and the first is not
// always the one that works. A stale or revoked token must not leave the vendor
// with no model list while a working key sits beside it.
type KeyLoader interface {
	KeysFor(ctx context.Context, tenantID int64, provider string) ([]Credential, error)
}

// ErrNoCredential means the tenant hasn't connected this vendor. Distinct from a
// fetch failure: no key is a normal state (an empty model list), a failed fetch
// is a problem worth keeping stale data for.
var ErrNoCredential = errors.New("no credential for provider")

type catalogKey struct {
	tenantID int64
	provider string
}

// entry is one tenant+vendor's fetched catalog. models is the FULL classified
// list; display caps it, validation does not — a model that scrolls out of the
// top N must not make an already-pinned ai_engine_rows.model unsavable.
//
// lastUsed is what makes the cache a working set rather than a directory: the
// refresher only keeps warm what someone actually reads, and drops the rest.
// Atomic so a cache HIT stays a read-lock operation.
type entry struct {
	models    []Model
	fetchedAt time.Time
	lastUsed  atomic.Int64 // unix nanos
}

func (e *entry) touch() { e.lastUsed.Store(time.Now().UnixNano()) }
func (e *entry) idle() time.Duration {
	return time.Since(time.Unix(0, e.lastUsed.Load()))
}

// failure is the last fetch failure for a pair. reason is client-safe (see
// callFailure.Reason) and is what the AI settings page shows instead of an
// unexplained empty dropdown; at gates the retry cooldown.
type failure struct {
	at     time.Time
	reason string
}

// Catalog caches per-tenant model lists.
type Catalog struct {
	lister ModelLister
	keys   KeyLoader
	limit  int

	mu      sync.RWMutex
	entries map[catalogKey]*entry
	// failures records why a pair last failed, for the cooldown and for the
	// message the UI shows in place of an empty list.
	failures map[catalogKey]failure

	// group coalesces concurrent misses for the same tenant+vendor into one
	// provider call. Without it, a page that renders five vendor sections on a
	// cold cache fires five identical requests.
	group singleflight.Group
}

func NewCatalog(lister ModelLister, keys KeyLoader, limit int) *Catalog {
	if limit <= 0 {
		limit = DefaultModelLimit
	}
	return &Catalog{
		lister:   lister,
		keys:     keys,
		limit:    limit,
		entries:  make(map[catalogKey]*entry),
		failures: make(map[catalogKey]failure),
	}
}

// Models returns the models tenantID can pick at provider, newest first, capped
// at the display limit. Never nil — the frontend reads `.models.length`.
//
// A miss fetches synchronously so a cold process (or a tenant who connected a
// key seconds ago) still renders a populated dropdown. A fetch failure with a
// previous entry serves that entry: stale beats empty.
func (c *Catalog) Models(ctx context.Context, tenantID int64, provider string) []Model {
	out := make([]Model, 0, c.limit)
	for _, m := range c.full(ctx, tenantID, provider) {
		// A retired model is not offered, but Valid still accepts it: hiding it
		// from the picker should not block saving a system already pinned to it.
		if m.deprecated {
			continue
		}
		if len(out) == c.limit {
			break
		}
		m.Params = ParamsFor(provider, m.ID)
		out = append(out, m)
	}
	return out
}

// Valid reports whether modelID is one tenantID can select at provider.
//
// With no cached entry it returns true. A vendor outage, a revoked key or a
// process that hasn't fetched yet leaves us unable to prove an id wrong, and
// blocking every AI-engine save in that window is worse than accepting an id
// that would fail later with a clear provider error. Once a list is known,
// validation is strict.
func (c *Catalog) Valid(ctx context.Context, tenantID int64, provider, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || !HasModelList(provider) {
		// A vendor with no model list has no valid model either — an MT vendor
		// can't back a model-driven AI engine.
		return false
	}
	models := c.full(ctx, tenantID, provider)
	if len(models) == 0 {
		return true
	}
	for _, m := range models {
		if m.ID == modelID {
			return true
		}
	}
	return false
}

// HasModelList reports whether a vendor publishes a model list worth fetching.
// Extensions have no models, and the MT vendors are reached as engines, not
// as an LLM with a chosen model.
func HasModelList(provider string) bool {
	return provider == ProviderOpenAI || provider == ProviderClaude
}

// full returns the uncapped cached list, fetching on a miss.
func (c *Catalog) full(ctx context.Context, tenantID int64, provider string) []Model {
	if !HasModelList(provider) {
		return nil
	}
	key := catalogKey{tenantID: tenantID, provider: provider}
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if ok {
		e.touch()
		return e.models
	}
	if c.lister == nil || c.keys == nil || c.cooling(key) {
		return nil
	}
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lazyFetchTimeout)
	defer cancel()
	models, err := c.Fetch(fetchCtx, tenantID, provider)
	if err != nil {
		// An on-demand failure is the one a user is watching: they opened the
		// page and got an empty dropdown, so it has to leave a trace.
		logging.Warn(ctx, err, "model catalog fetch failed", "ai-model-fetch-failed",
			logging.TenantID(tenantID), logging.Provider(provider))
		return nil
	}
	return models
}

// FailureReason is the client-safe explanation for an empty model list, or ""
// when nothing failed. Surfaced on the vendor status so the AI settings page can
// say WHY there are no models instead of rendering a bare empty dropdown.
func (c *Catalog) FailureReason(tenantID int64, provider string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if _, ok := c.entries[catalogKey{tenantID: tenantID, provider: provider}]; ok {
		// A successful fetch clears the failure, so a live entry means the list
		// is real — even an empty one (a key with access to nothing).
		return ""
	}
	return c.failures[catalogKey{tenantID: tenantID, provider: provider}].reason
}

// Fetch reads one tenant+vendor's catalog from the provider and stores it,
// serving the cached entry if one appeared meanwhile. Concurrent calls for the
// same pair share one request. Returns the uncapped list.
func (c *Catalog) Fetch(ctx context.Context, tenantID int64, provider string) ([]Model, error) {
	return c.fetch(ctx, tenantID, provider, true)
}

// Refresh refetches a pair that is ALREADY cached, ignoring that entry. Used by
// the periodic refresher, which exists precisely to replace warm entries — going
// through Fetch would short-circuit on the entry it means to renew.
func (c *Catalog) Refresh(ctx context.Context, tenantID int64, provider string) error {
	_, err := c.fetch(ctx, tenantID, provider, false)
	return err
}

func (c *Catalog) fetch(ctx context.Context, tenantID int64, provider string, useCached bool) ([]Model, error) {
	key := catalogKey{tenantID: tenantID, provider: provider}
	res, err, _ := c.group.Do(fmt.Sprintf("%d|%s|%t", tenantID, provider, useCached), func() (any, error) {
		// Re-check inside the flight: a caller that missed the cache just before
		// another caller's fetch stored it would otherwise start a second,
		// identical request the moment the first one finished.
		if useCached {
			c.mu.RLock()
			e, cached := c.entries[key]
			c.mu.RUnlock()
			if cached {
				e.touch()
				return e.models, nil
			}
		}
		creds, err := c.keys.KeysFor(ctx, tenantID, provider)
		if err != nil || len(creds) == 0 {
			if err == nil || errors.Is(err, ErrNoCredential) {
				// Deliberately NOT cached. Connecting a key is the moment a user
				// expects models to appear, and a cached empty list would hide
				// them until the next refresh tick. The cost of re-asking is one
				// indexed lookup — no provider call happens without a key.
				return []Model{}, nil
			}
			return nil, err
		}
		// Try each key, best first, and stop at the first that answers. Only
		// every key failing is a failure: one dead token must not hide a vendor
		// the tenant can genuinely reach with another.
		var firstErr error
		for _, cred := range creds {
			remote, err := c.lister.List(ctx, provider, cred.APIKey, cred.BaseURL)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			models := classifyAll(provider, remote)
			c.store(key, models)
			return models, nil
		}
		// Report the PREFERRED key's failure, not the last one tried: that is the
		// key the runtime spends, so it is the one worth fixing.
		c.recordFailure(key, failureReason(firstErr))
		return nil, firstErr
	})
	if err != nil {
		return nil, err
	}
	return res.([]Model), nil
}

// Set replaces one tenant+vendor's entry. Used by Refresher and by tests.
func (c *Catalog) Set(tenantID int64, provider string, models []Model) {
	c.store(catalogKey{tenantID: tenantID, provider: provider}, models)
}

// cooling reports whether a recent failure should suppress another on-demand
// fetch for this pair.
func (c *Catalog) cooling(key catalogKey) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return time.Since(c.failures[key].at) < failCooldown
}

// recordFailure stores why a pair last failed, for the cooldown and for the UI.
func (c *Catalog) recordFailure(key catalogKey, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures[key] = failure{at: time.Now(), reason: reason}
}

func (c *Catalog) store(key catalogKey, models []Model) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Preserve lastUsed across a refresh — a refetch is not a read, and treating
	// it as one would keep an abandoned entry warm forever.
	e := &entry{models: models, fetchedAt: time.Now()}
	if prev, ok := c.entries[key]; ok {
		e.lastUsed.Store(prev.lastUsed.Load())
	} else {
		e.touch()
	}
	c.entries[key] = e
	delete(c.failures, key)
}

// warmKeys returns the pairs worth refreshing, and drops the ones nobody has
// read for idleTTL. This is the refresher's whole work list: the cache is a
// working set, so refresh cost tracks ACTIVE organizations rather than every
// organization that ever stored a key.
func (c *Catalog) warmKeys(idleTTL time.Duration) []catalogKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]catalogKey, 0, len(c.entries))
	for key, e := range c.entries {
		if e.idle() > idleTTL {
			delete(c.entries, key)
			continue
		}
		out = append(out, key)
	}
	// A failure record outlives its cooldown by a lot; expire it on the same
	// schedule so a long-gone tenant can't hold one forever.
	for key, f := range c.failures {
		if time.Since(f.at) > idleTTL {
			delete(c.failures, key)
		}
	}
	return out
}

// Len reports how many pairs are cached (tests + the refresh log).
func (c *Catalog) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// classifyAll turns a provider response into the selectable catalog: drop
// non-text models, collapse dated snapshots onto their alias, newest first with
// fine-tunes last (a tenant's own fine-tune is niche, and its creation date
// would otherwise push the vendor's real models out of the display cap).
func classifyAll(provider string, remote []RemoteModel) []Model {
	out := make([]Model, 0, len(remote))
	for _, r := range remote {
		if m, ok := classifyRemote(provider, r); ok {
			out = append(out, m)
		}
	}
	out = dropDatedDuplicates(out)
	sort.SliceStable(out, func(i, j int) bool {
		iFT := strings.HasPrefix(out[i].ID, ftPrefix)
		jFT := strings.HasPrefix(out[j].ID, ftPrefix)
		return !iFT && jFT
	})
	return out
}
