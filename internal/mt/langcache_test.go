package mt

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ltStub serves a LibreTranslate catalog and counts requests. served is sent on
// after every response, so a test can wait for a background refetch instead of
// sleeping for one.
func ltStub(t *testing.T, hits *atomic.Int32, body func() string) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	served := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, body())
		select {
		case served <- struct{}{}:
		default:
		}
	}))
	t.Cleanup(srv.Close)
	return srv, served
}

// catalogOnly drops the provenance flag for tests that only assert on the list.
func catalogOnly(cat Catalog, _ bool) Catalog { return cat }

func oneLang(code string) string {
	return fmt.Sprintf(`[{"code":%q,"name":%q,"targets":["de"]},{"code":"de","name":"German","targets":[%q]}]`, code, code, code)
}

func TestLanguageCache_SecondReadDoesNotRefetch(t *testing.T) {
	var hits atomic.Int32
	srv, _ := ltStub(t, &hits, func() string { return oneLang("en") })

	cache := NewLanguageCache(time.Hour, languageFetchTimeout)
	engine := LibreTranslate{APIURL: srv.URL}
	for range 5 {
		cache.Languages(context.Background(), engine)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("five reads made %d requests, want 1", got)
	}
}

func TestLanguageCache_ConcurrentMissesShareOneFetch(t *testing.T) {
	var hits atomic.Int32
	srv, _ := ltStub(t, &hits, func() string {
		time.Sleep(20 * time.Millisecond) // widen the window a second caller could slip into
		return oneLang("en")
	})

	cache := NewLanguageCache(time.Hour, languageFetchTimeout)
	engine := LibreTranslate{APIURL: srv.URL}

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Languages(context.Background(), engine)
		}()
	}
	wg.Wait()

	if got := hits.Load(); got != 1 {
		t.Errorf("20 concurrent misses made %d requests, want 1", got)
	}
}

// A stale entry is served immediately and replaced behind the caller, so a read
// never waits on the vendor once an endpoint has been fetched.
func TestLanguageCache_StaleEntryServedWhileRefetching(t *testing.T) {
	var hits atomic.Int32
	code := "en"
	var mu sync.Mutex
	srv, served := ltStub(t, &hits, func() string {
		mu.Lock()
		defer mu.Unlock()
		return oneLang(code)
	})

	cache := NewLanguageCache(time.Millisecond, languageFetchTimeout)
	engine := LibreTranslate{APIURL: srv.URL}
	ctx := context.Background()

	if !targetCodes(catalogOnly(cache.Languages(ctx, engine)))["en"] {
		t.Fatal("first read must fetch the live list")
	}
	<-served

	mu.Lock()
	code = "fi" // the vendor's list changes
	mu.Unlock()
	time.Sleep(5 * time.Millisecond) // entry is now past its TTL

	if !targetCodes(catalogOnly(cache.Languages(ctx, engine)))["en"] {
		t.Error("stale read must serve the old list, not block on the vendor")
	}
	<-served // the background refetch

	deadline := time.After(2 * time.Second)
	for {
		if targetCodes(catalogOnly(cache.Languages(ctx, engine)))["fi"] {
			return
		}
		select {
		case <-deadline:
			t.Fatal("background refresh never replaced the entry")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// A vendor blip must not empty a working catalog. The read that finds the entry
// stale is served the old list and kicks off a refetch; when that refetch fails,
// nothing is written, so the NEXT read must still see the old list rather than
// the static table.
func TestLanguageCache_StaleSurvivesAFailedRefetch(t *testing.T) {
	var hits atomic.Int32
	served := make(chan struct{}, 4)
	fail := false
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		mu.Lock()
		shouldFail := fail
		mu.Unlock()
		if shouldFail {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			fmt.Fprint(w, oneLang("en"))
		}
		served <- struct{}{}
	}))
	defer srv.Close()

	cache := NewLanguageCache(time.Millisecond, languageFetchTimeout)
	engine := LibreTranslate{APIURL: srv.URL}
	ctx := context.Background()

	cache.Languages(ctx, engine)
	<-served // the first, successful fetch

	mu.Lock()
	fail = true
	mu.Unlock()
	time.Sleep(5 * time.Millisecond) // the entry is now past its TTL

	// The refetch runs synchronously here. Going through Languages would spawn
	// it in a goroutine, and there is no signal for "the refetch finished" —
	// `served` fires inside the handler, before the response is even flushed.
	if _, err := cache.fetch(ctx, "libretranslate|"+srv.URL, engine); err == nil {
		t.Fatal("expected the refetch to fail")
	}

	// The assertion that matters: a read AFTER the failed refetch.
	cat, fromEngine := cache.Languages(ctx, engine)
	if !fromEngine {
		t.Fatal("a failed refetch dropped the entry back to the static table")
	}
	if len(cat.Sources) != 2 {
		t.Errorf("got %d languages, want the 2 from the surviving entry", len(cat.Sources))
	}
}

func TestLanguageCache_FailedFetchFallsBackToStatic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cache := NewLanguageCache(time.Hour, languageFetchTimeout)
	cat, _ := cache.Languages(context.Background(), LibreTranslate{APIURL: srv.URL})
	if len(cat.Sources) != len(libreTranslateFallback().Sources) {
		t.Errorf("got %d languages, want the %d-entry static table", len(cat.Sources), len(libreTranslateFallback().Sources))
	}
}

func TestWithBaseCodes(t *testing.T) {
	out := withBaseCodes([]LanguageInfo{
		{Code: "en-gb", Name: "British English"},
		{Code: "pt-br", Name: "Brazilian Portuguese"},
	})
	got := codeSet(out)
	for _, want := range []string{"en-gb", "pt-br", "en", "pt"} {
		if !got[want] {
			t.Errorf("%q missing from %v", want, got)
		}
	}

	// A list that already carries its base codes must come back unchanged.
	base := []LanguageInfo{{Code: "en"}, {Code: "de"}}
	if out := withBaseCodes(base); len(out) != 2 {
		t.Errorf("base-code pass added %d entries to a base-only list", len(out)-2)
	}
}

// The language endpoint is tenant-settable, so an unbounded read would let a
// hostile or broken host exhaust memory on a request any manager can trigger.
func TestReadLanguageList_CapsTheBody(t *testing.T) {
	body, err := readLanguageList(strings.NewReader(strings.Repeat("a", maxLanguageListBytes*2)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(body) != maxLanguageListBytes {
		t.Errorf("buffered %d bytes, want the %d cap", len(body), maxLanguageListBytes)
	}
}

// A failure is not remembered. The next caller tries again and caches its own
// success — so one tenant's rejected key cannot hold another tenant's working
// key on the static table.
func TestLanguageCache_FailureLeavesNothingBehind(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "DeepL-Auth-Key good" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Query().Get("type") == "source" {
			fmt.Fprint(w, `[{"language":"EN","name":"English"}]`)
			return
		}
		fmt.Fprint(w, `[{"language":"DE","name":"German"}]`)
	}))
	defer srv.Close()

	cache := NewLanguageCache(time.Hour, languageFetchTimeout)
	ctx := context.Background()

	if _, fromEngine := cache.Languages(ctx, DeepL{APIKey: "bad", BaseURL: srv.URL}); fromEngine {
		t.Fatal("a rejected key must not produce a live catalog")
	}
	cat, fromEngine := cache.Languages(ctx, DeepL{APIKey: "good", BaseURL: srv.URL})
	if !fromEngine {
		t.Fatal("the next caller must reach the engine, not inherit the failure")
	}
	if !sourceCodes(cat)["en"] {
		t.Errorf("expected the live catalog, got %v", sourceCodes(cat))
	}
	if hits.Load() < 2 {
		t.Errorf("the second caller never reached the vendor (%d requests)", hits.Load())
	}
}

// The cache is bounded, and it drops the OLDEST entry — an inverted comparison
// would evict the one just fetched, so the bound has to be checked by which
// entry survives, not only by the count.
func TestLanguageCache_EvictsTheOldestEntry(t *testing.T) {
	cache := NewLanguageCache(time.Hour, languageFetchTimeout)
	at := time.Now()
	for i := range maxCacheEntries {
		cache.entries[fmt.Sprintf("libretranslate|http://host-%d", i)] = &langEntry{fetchedAt: at.Add(time.Duration(i) * time.Second)}
	}
	oldest, newest := "libretranslate|http://host-0", fmt.Sprintf("libretranslate|http://host-%d", maxCacheEntries-1)

	cache.mu.Lock()
	cache.evictOldestLocked()
	cache.mu.Unlock()

	if len(cache.entries) != maxCacheEntries-1 {
		t.Errorf("evicted %d entries, want 1", maxCacheEntries-len(cache.entries))
	}
	if _, still := cache.entries[oldest]; still {
		t.Error("the oldest entry survived")
	}
	if _, still := cache.entries[newest]; !still {
		t.Error("the newest entry was evicted instead of the oldest")
	}
}

// The bound holds through the real fetch path, not just the helper.
func TestLanguageCache_EntriesMapIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, oneLang("en"))
	}))
	defer srv.Close()

	cache := NewLanguageCache(time.Hour, languageFetchTimeout)
	ctx := context.Background()
	// Distinct endpoints, all answering: only a real server can grow this map.
	for i := range maxCacheEntries + 50 {
		cache.Languages(ctx, LibreTranslate{APIURL: fmt.Sprintf("%s/?n=%d", srv.URL, i)})
	}
	if got := len(cache.entries); got > maxCacheEntries {
		t.Errorf("entries grew to %d, cap is %d", got, maxCacheEntries)
	}
}

// The background refetch outlives the request that noticed the entry was stale.
// Without context.WithoutCancel in refreshAsync, the caller returning would
// cancel the refetch and the entry would stay stale forever.
func TestLanguageCache_RefreshOutlivesTheTriggeringRequest(t *testing.T) {
	var hits atomic.Int32
	code := "en"
	var mu sync.Mutex
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) > 1 {
			<-release // hold the refetch open until the caller has gone
		}
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprint(w, oneLang(code))
	}))
	defer srv.Close()

	cache := NewLanguageCache(time.Millisecond, languageFetchTimeout)
	engine := LibreTranslate{APIURL: srv.URL}

	cache.Languages(context.Background(), engine)
	mu.Lock()
	code = "fi"
	mu.Unlock()
	time.Sleep(5 * time.Millisecond) // past the TTL

	// A request-scoped context, cancelled the moment the caller is done — which
	// is exactly what a finished HTTP handler does.
	ctx, cancel := context.WithCancel(context.Background())
	cache.Languages(ctx, engine) // serves stale, spawns the refetch
	cancel()
	close(release)

	// Read the entry directly. Polling through Languages would spawn a FRESH
	// refetch on every iteration — with a background context — and those would
	// land the new list whether or not refreshAsync detaches from its caller.
	key := "libretranslate|" + srv.URL
	deadline := time.After(2 * time.Second)
	for {
		cache.mu.RLock()
		entry := cache.entries[key]
		got := entry != nil && codeSet(entry.catalog.Targets)["fi"]
		cache.mu.RUnlock()
		if got {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the refetch died with the request that triggered it")
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
}

// A Go substring shares the backing array of the string it came from, so
// truncating by slicing would keep the whole original alive for as long as the
// catalog is cached — the cap would bound what you can read and nothing else.
// 200 names of 100 KB retained 21 MB that way, for 25 KB of visible text.
func TestTruncateName_DoesNotRetainTheOriginal(t *testing.T) {
	const count, size = 200, 100_000

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	kept := make([]string, 0, count)
	for range count {
		kept = append(kept, truncateName(strings.Repeat("x", size)))
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(kept)

	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if retained > 1<<20 {
		t.Errorf("retained %.1f MB to show %d bytes of names — truncateName is slicing, not cloning",
			float64(retained)/1e6, count*maxLanguageNameLen)
	}
}

// A cold read is a fetch its caller waits on, and singleflight hands the
// leader's error to every waiter. The HTTP server and the asynq worker share
// one process and one package-level cache, so a browser request that goes away
// would otherwise drop the fetch a worker's MT phase is queued behind — and the
// static DeepL table carries no regional targets, so the phase that inherits it
// sends en-gb as EN-US.
func TestLanguageCache_ColdFetchOutlivesTheTriggeringRequest(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		fmt.Fprint(w, `[{"code":"en","name":"English","targets":["de"]}]`)
	}))
	defer srv.Close()

	cache := NewLanguageCache(time.Hour, 5*time.Second)
	engine := LibreTranslate{APIURL: srv.URL}

	type result struct {
		cat        Catalog
		fromEngine bool
	}
	done := make(chan result, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		cat, fromEngine := cache.Languages(ctx, engine)
		done <- result{cat, fromEngine}
	}()

	<-entered
	cancel()       // the request that triggered the fetch goes away
	close(release) // the vendor answers anyway

	got := <-done
	if !got.fromEngine {
		t.Fatal("a cancelled caller dropped the fetch — the catalog fell back to the static table")
	}
	// The live list has one source; the static fallback has 26 including fr.
	// Naming a language only the fallback carries keeps this from passing on a
	// catalog that merely happened to be non-empty.
	if codeSet(got.cat.Sources)["fr"] {
		t.Errorf("sources came from the static table: %+v", got.cat.Sources)
	}
}

// A catalog with one empty half refuses every pair and reaches the wire as
// `"targets": null`. The guard sits in the cache so no fetcher can forget it:
// this instance publishes a source whose every target is unusable.
func TestLanguageCache_EmptyHalfFallsBackToStatic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"code":"en","name":"English","targets":["","  "]}]`)
	}))
	defer srv.Close()

	cache := NewLanguageCache(time.Hour, 5*time.Second)
	cat, fromEngine := cache.Languages(context.Background(), LibreTranslate{APIURL: srv.URL})

	if fromEngine {
		t.Error("a half-empty catalog must not count as the engine's own word — it would refuse every pair")
	}
	if len(cat.Targets) == 0 {
		t.Fatal("the fallback must still fill both halves")
	}
	if !codeSet(cat.Targets)["fr"] {
		t.Errorf("expected the static LibreTranslate table, got %d targets", len(cat.Targets))
	}
}

// A 5xx on the languages endpoint while /translate still answers is a blip, not
// a verdict. Falling to the static table on the first one costs the live catalog
// for a whole TTL, and the static DeepL table has no regional targets — the
// phase that inherits it sends en-gb as EN-US.
func TestLanguageCache_RetriesATransientFailure(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `[{"code":"en","name":"English","targets":["de"]}]`)
	}))
	defer srv.Close()

	cache := NewLanguageCache(time.Hour, 5*time.Second)
	cat, fromEngine := cache.Languages(context.Background(), LibreTranslate{APIURL: srv.URL})

	if !fromEngine {
		t.Fatalf("one 502 dropped the catalog to the static table after %d attempt(s)", hits.Load())
	}
	// fr is in the static fallback and not in what this instance published, so
	// it separates the live list from the compiled-in one.
	if codeSet(cat.Sources)["fr"] {
		t.Errorf("served the static table: %+v", cat.Sources)
	}
	if hits.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", hits.Load())
	}
}

// The retry shares the fetch budget rather than adding to it, so an endpoint
// that hangs is not dialled twice — a cold read on a dead host must not cost a
// manager two timeouts on a settings page.
func TestLanguageCache_RetryDoesNotOutlastTheFetchBudget(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-r.Context().Done()
	}))
	defer srv.Close()

	cache := NewLanguageCache(time.Hour, 300*time.Millisecond)
	start := time.Now()
	_, fromEngine := cache.Languages(context.Background(), LibreTranslate{APIURL: srv.URL})
	elapsed := time.Since(start)

	if fromEngine {
		t.Fatal("a hanging endpoint must fall back to the static table")
	}
	if hits.Load() != 1 {
		t.Errorf("a hang spends the whole budget, so it must be dialled once; got %d", hits.Load())
	}
	if elapsed > time.Second {
		t.Errorf("cold read took %v — the retry is extending the budget instead of sharing it", elapsed)
	}
}
