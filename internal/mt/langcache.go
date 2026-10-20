package mt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"openloc/internal/errtrack"
	"openloc/internal/logging"
)

// In-process cache of engine language catalogs, keyed by engine type +
// endpoint. Entries fill on first read and a stale one is served while a
// refetch runs behind it, so only the very first read of an endpoint waits.

// languageTTL is how long a fetched list is served before a refetch. Vendors
// change their language lists a few times a year, and a stale entry is served
// meanwhile, so there is nothing to gain from a shorter window.
const languageTTL = 24 * time.Hour

// languageFetchTimeout bounds one fetch. A cold read happens inside an HTTP
// request or an MT phase, so it has to be tight — mt.HTTPClient's 5-minute
// timeout is sized for translation batches, not for a list.
const languageFetchTimeout = 5 * time.Second

// maxCacheEntries bounds the cache. One entry per endpoint, and a provider
// engine is read from its vendor token's base URL — free text, set by an owner
// or admin on the AI settings page — so the key space grows at their hand. An
// entry costs them a host that answers with a real catalog.
//
// One entry's size is bounded by the input caps in client.go, so this is a
// cardinality backstop rather than a memory budget. It sits far above the
// distinct endpoints a real tenant set holds, and eviction costs a refetch
// rather than correctness.
const maxCacheEntries = 128

// languageFetchAttempts is how many times one fetch tries the endpoint. A
// transient 5xx while /translate still answers would otherwise cost the live
// catalog for a whole TTL, and the static DeepL table has no regional targets —
// the phase that inherits it sends en-gb as EN-US.
//
// The attempts share one languageFetchTimeout budget, so a slow endpoint spends
// it on the first try and never reaches the second. Only a fast failure retries,
// which is why this does not lengthen a cold read.
const languageFetchAttempts = 2

// languageRetryDelay spaces two attempts far enough apart to outlast a restart
// blip without eating the budget.
const languageRetryDelay = 150 * time.Millisecond

// errNoCredential means the engine has no API key yet. No request is made, and
// the warning log skips it: "not connected yet" is a normal state, not an
// incident.
var errNoCredential = errors.New("mt: no API key for this engine")

type langEntry struct {
	catalog   Catalog
	fetchedAt time.Time
}

// LanguageCache caches engine language catalogs. The zero value is not usable;
// call NewLanguageCache.
type LanguageCache struct {
	ttl          time.Duration
	fetchTimeout time.Duration

	mu      sync.RWMutex
	entries map[string]*langEntry

	// group collapses concurrent misses for one endpoint into a single fetch.
	// Without it, a batch of jobs starting together would each open their own
	// request to the same vendor.
	group singleflight.Group
}

func NewLanguageCache(ttl, fetchTimeout time.Duration) *LanguageCache {
	return &LanguageCache{
		ttl:          ttl,
		fetchTimeout: fetchTimeout,
		entries:      make(map[string]*langEntry),
	}
}

// defaultLanguageCache backs the package-level SupportedLanguages. Tests build
// their own instance with a short TTL rather than reaching into this one.
var defaultLanguageCache = NewLanguageCache(languageTTL, languageFetchTimeout)

// Languages returns the languages engine supports, and whether the list came
// from the engine itself. It never returns an empty list: an engine that
// publishes no catalog, has no key, or cannot be reached falls back to the
// static table for its type, because an empty list would reject every language
// pair rather than the ones the engine really lacks.
//
// fromEngine is false for every fallback. Callers that resolve a locale can
// treat the two alike; callers that REFUSE a pair must not, or a compiled-in
// guess starts overruling an engine that never said anything.
func (c *LanguageCache) Languages(ctx context.Context, engine Engine) (cat Catalog, fromEngine bool) {
	lister, ok := engine.(LanguageLister)
	if !ok {
		return staticLanguages(engine.Name()), false
	}
	key := engine.Name() + "|" + lister.LanguageEndpoint()

	c.mu.RLock()
	entry := c.entries[key]
	c.mu.RUnlock()

	if entry != nil {
		if time.Since(entry.fetchedAt) > c.ttl {
			c.refreshAsync(ctx, key, lister)
		}
		return entry.catalog, true
	}

	fetched, err := c.fetch(ctx, key, lister)
	if err != nil {
		if !errors.Is(err, errNoCredential) {
			// A log, not an errtrack notice: the error may quote the vendor's
			// response body.
			logging.Warn(ctx, err, "engine language list fetch failed", "mt-languages-fetch-failed",
				logging.Engine(engine.Name()), logging.Label("endpoint", errtrack.ScrubURL(lister.LanguageEndpoint())))
		}
		return staticLanguages(engine.Name()), false
	}
	return fetched, true
}

// refreshAsync refetches a stale entry behind the caller. The read that
// triggered it has already been served the stale list, so a failure here only
// means the entry stays stale until the next read tries again.
func (c *LanguageCache) refreshAsync(ctx context.Context, key string, lister LanguageLister) {
	go func() {
		if _, err := c.fetch(ctx, key, lister); err != nil && !errors.Is(err, errNoCredential) {
			logging.Warn(ctx, err, "engine language list refresh failed", "mt-languages-refresh-failed",
				logging.Engine(lister.Name()), logging.Label("endpoint", errtrack.ScrubURL(lister.LanguageEndpoint())))
		}
	}()
}

// fetch reads the live list and stores it, coalescing concurrent calls for the
// same endpoint.
func (c *LanguageCache) fetch(ctx context.Context, key string, lister LanguageLister) (Catalog, error) {
	res, err, _ := c.group.Do(key, func() (any, error) {
		// WithoutCancel: singleflight hands the leader's error to every waiter,
		// so a browser request that goes away must not drop the fetch a worker
		// MT phase is waiting on behind it.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.fetchTimeout)
		defer cancel()

		cat, err := fetchCatalog(fetchCtx, lister)
		if err != nil {
			// Nothing is remembered about a failure. The next caller tries again
			// and caches its own success; until one does, every reader gets the
			// static table. A rejected key comes straight back, so retrying costs
			// almost nothing, and an unreachable endpoint was going to fail the
			// translation anyway.
			return nil, err
		}

		c.mu.Lock()
		if _, exists := c.entries[key]; !exists && len(c.entries) >= maxCacheEntries {
			c.evictOldestLocked()
		}
		c.entries[key] = &langEntry{catalog: cat, fetchedAt: time.Now()}
		c.mu.Unlock()
		return cat, nil
	})
	if err != nil {
		return Catalog{}, err
	}
	return res.(Catalog), nil
}

// fetchCatalog reads one engine's catalog, retrying a failed attempt while the
// budget allows.
func fetchCatalog(ctx context.Context, lister LanguageLister) (Catalog, error) {
	var cat Catalog
	var err error
	for attempt := range languageFetchAttempts {
		if attempt > 0 && !waitFor(ctx, languageRetryDelay) {
			break
		}
		cat, err = lister.FetchLanguages(ctx)
		// A missing key is a state, not a blip: retrying it just burns the
		// budget for an answer that cannot change within one fetch.
		if err == nil || errors.Is(err, errNoCredential) {
			break
		}
	}
	if err != nil {
		return Catalog{}, err
	}
	// Checked here so no fetcher can forget it, and outside the retry loop
	// because an empty catalog is an answer rather than a failure. An empty half
	// reaches the wire as `"targets": null` and refuses every pair downstream;
	// the static table is the honest answer instead.
	if cat.Empty() {
		return Catalog{}, fmt.Errorf("mt: %s published an empty catalog (sources=%d targets=%d)",
			lister.Name(), len(cat.Sources), len(cat.Targets))
	}
	return cat, nil
}

// waitFor sleeps unless the budget runs out first, reporting whether there is
// still time to try again.
func waitFor(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// evictOldestLocked drops the least recently fetched entry. An entry only exists
// for an endpoint that answered with a real catalog, so filling the map is
// expensive to do on purpose and this is a backstop. Dropping one costs a
// refetch, never correctness.
func (c *LanguageCache) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for k, e := range c.entries {
		if oldestKey == "" || e.fetchedAt.Before(oldest) {
			oldestKey, oldest = k, e.fetchedAt
		}
	}
	delete(c.entries, oldestKey)
}

// withBaseCodes adds, for every regional code in a list, its base code when the
// list lacks one.
//
// DeepL is why this exists: it offers EN-GB and EN-US as targets but no bare EN,
// so without this a project targeting plain "en" resolves to nothing the engine
// admits to supporting. Safe because the translate path maps a base code onto
// the vendor's own regional default (deeplLangCode), and Google accepts base
// codes directly.
func withBaseCodes(langs []LanguageInfo) []LanguageInfo {
	seen := make(map[string]bool, len(langs))
	for _, l := range langs {
		seen[l.Code] = true
	}
	out := langs
	for _, l := range langs {
		base := baseCode(l.Code)
		if base == "" || seen[base] {
			continue
		}
		seen[base] = true
		out = append(out, LanguageInfo{Code: base, Name: l.Name})
	}
	return out
}

// baseCode returns the primary subtag of a regional code, or "" when the code
// has no region.
func baseCode(code string) string {
	for i := 0; i < len(code); i++ {
		if code[i] == '-' {
			return code[:i]
		}
	}
	return ""
}
