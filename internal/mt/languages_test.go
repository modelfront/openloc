package mt

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// deeplStub serves DeepL's real /v2/languages shape: source and target lists
// that differ, with uppercase codes and regional targets.
func deeplStub(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		if got := r.Header.Get("Authorization"); got != "DeepL-Auth-Key test-key" {
			t.Errorf("authorization header: got %q", got)
		}
		switch r.URL.Query().Get("type") {
		case "source":
			fmt.Fprint(w, `[{"language":"EN","name":"English"},{"language":"DE","name":"German"},{"language":"PT","name":"Portuguese"}]`)
		case "target":
			fmt.Fprint(w, `[{"language":"EN-GB","name":"English (British)"},{"language":"EN-US","name":"English (American)"},
			               {"language":"DE","name":"German"},{"language":"PT-BR","name":"Portuguese (Brazilian)"},
			               {"language":"PT-PT","name":"Portuguese (European)"}]`)
		default:
			t.Errorf("unexpected type=%q", r.URL.Query().Get("type"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// sourceCodes and targetCodes index a catalog's two halves. Tests name the half
// they mean — the shape's whole point is that they are different lists.
func sourceCodes(cat Catalog) map[string]bool { return codeSet(cat.Sources) }
func targetCodes(cat Catalog) map[string]bool { return codeSet(cat.Targets) }

func targetsOf(t *testing.T, cat Catalog, source string) map[string]bool {
	t.Helper()
	set := targetSet(cat, source)
	if set == nil {
		t.Fatalf("source %q missing from catalog", source)
	}
	return set
}

func TestDeepLFetchLanguages(t *testing.T) {
	srv := deeplStub(t, nil)
	cache := NewLanguageCache(languageTTL, languageFetchTimeout)
	cat, _ := cache.Languages(context.Background(), DeepL{APIKey: "test-key", BaseURL: srv.URL})

	if !sourceCodes(cat)["en"] || !sourceCodes(cat)["de"] {
		t.Fatalf("sources: want en and de, got %v", sourceCodes(cat))
	}
	tgt := targetsOf(t, cat, "en")
	for _, want := range []string{"en-gb", "en-us", "pt-br", "pt-pt"} {
		if !tgt[want] {
			t.Errorf("target %q missing from %v", want, tgt)
		}
	}
	if tgt["EN-GB"] {
		t.Error("codes must be normalized to lowercase")
	}
}

// DeepL publishes EN-GB and EN-US but no bare EN as a target. Without the base
// code, a project targeting plain "en" would be rejected as unsupported —
// even though the translate path maps EN onto DeepL's own EN-US default.
func TestDeepLFetchLanguages_AddsBaseCodes(t *testing.T) {
	srv := deeplStub(t, nil)
	cache := NewLanguageCache(languageTTL, languageFetchTimeout)
	cat, _ := cache.Languages(context.Background(), DeepL{APIKey: "test-key", BaseURL: srv.URL})

	tgt := targetsOf(t, cat, "de")
	for _, want := range []string{"en", "pt"} {
		if !tgt[want] {
			t.Errorf("base target %q missing from %v", want, tgt)
		}
	}
}

// DeepL supports British English, so a project targeting en-gb must reach DeepL
// as EN-GB rather than collapsing to EN-US.
func TestResolveLocalePair_DeepLKeepsRegionalTarget(t *testing.T) {
	srv := deeplStub(t, nil)
	src, tgt := ResolveLocalePair(context.Background(), "en", "en-gb", DeepL{APIKey: "test-key", BaseURL: srv.URL})
	if src != "en" || tgt != "en-gb" {
		t.Errorf("got %s→%s, want en→en-gb", src, tgt)
	}
	if got := deeplLangCode(tgt, true); got != "EN-GB" {
		t.Errorf("wire code: got %q, want EN-GB", got)
	}

	// en-gb alone is a weak assertion: passthrough returns the requested code
	// unchanged, so a catalog missing en-gb entirely would answer "en-gb" too.
	// en-ca separates them — DeepL does not offer it, so a catalog that really
	// carries the English variants falls back to the base code, while an empty
	// or wrong one passes "en-ca" straight through.
	_, fallback := ResolveLocalePair(context.Background(), "en", "en-ca", DeepL{APIKey: "test-key", BaseURL: srv.URL})
	if fallback != "en" {
		t.Errorf("en-ca: got %q, want the base code en", fallback)
	}
}

// DeepL's catalog is asymmetric: EN-GB is a target but never a source. The
// source side must resolve against the source list, or we send an
// unsupported source_lang.
func TestResolveLocalePair_DeepLSourceResolvesToBase(t *testing.T) {
	srv := deeplStub(t, nil)
	src, tgt := ResolveLocalePair(context.Background(), "en-gb", "de", DeepL{APIKey: "test-key", BaseURL: srv.URL})
	if src != "en" || tgt != "de" {
		t.Errorf("got %s→%s, want en→de", src, tgt)
	}
}

func TestDeepLFetchLanguages_NoKeyUsesStaticTable(t *testing.T) {
	cache := NewLanguageCache(languageTTL, languageFetchTimeout)
	cat, _ := cache.Languages(context.Background(), DeepL{BaseURL: "http://127.0.0.1:1"})
	if len(cat.Sources) != len(deeplLanguages().Sources) {
		t.Fatalf("got %d languages, want the %d-entry static table", len(cat.Sources), len(deeplLanguages().Sources))
	}
	if sourceCodes(cat)["en-gb"] {
		t.Error("static table must not carry regional targets")
	}
}

// Connecting a key is exactly when a user expects the real list, so a missing
// key is never cached.
func TestDeepLFetchLanguages_KeyAddedAfterwards(t *testing.T) {
	var hits atomic.Int32
	srv := deeplStub(t, &hits)
	cache := NewLanguageCache(languageTTL, languageFetchTimeout)

	cache.Languages(context.Background(), DeepL{BaseURL: srv.URL})
	if hits.Load() != 0 {
		t.Fatalf("keyless engine reached the vendor %d times", hits.Load())
	}
	cat, _ := cache.Languages(context.Background(), DeepL{APIKey: "test-key", BaseURL: srv.URL})
	if !targetsOf(t, cat, "en")["en-gb"] {
		t.Error("adding a key must produce the live list")
	}
}

func TestGoogleFetchLanguages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/language/translate/v2/languages") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "goog-key" {
			t.Errorf("key param: got %q", r.URL.Query().Get("key"))
		}
		fmt.Fprint(w, `{"data":{"languages":[{"language":"en","name":"English"},
		                {"language":"zh-CN","name":"Chinese (Simplified)"},{"language":"de","name":"German"}]}}`)
	}))
	defer srv.Close()

	cache := NewLanguageCache(languageTTL, languageFetchTimeout)
	cat, _ := cache.Languages(context.Background(), Google{APIKey: "goog-key", BaseURL: srv.URL})

	if !sourceCodes(cat)["zh-cn"] {
		t.Errorf("zh-CN must normalize to zh-cn, got %v", sourceCodes(cat))
	}
	if !targetsOf(t, cat, "en")["zh"] {
		t.Error("base code zh must be derived from zh-cn")
	}
}

func TestLibreTranslateFetchLanguages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"code":"EN","name":"English","targets":["DE","FR"]},
		                {"code":"DE","name":"German","targets":["EN"]}]`)
	}))
	defer srv.Close()

	cache := NewLanguageCache(languageTTL, languageFetchTimeout)
	cat, _ := cache.Languages(context.Background(), LibreTranslate{APIURL: srv.URL})

	if len(cat.Sources) != 2 {
		t.Fatalf("got %d sources, want the 2 the instance published", len(cat.Sources))
	}
	if !targetsOf(t, cat, "en")["fr"] {
		t.Errorf("live list must win over the %d-entry fallback", len(libreTranslateFallback().Sources))
	}
}

// Two engines of one type pointed at different endpoints — the gateway case —
// must not read each other's list.
func TestLanguageCache_KeyedByEndpoint(t *testing.T) {
	direct := deeplStub(t, nil)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("type") {
		case "source":
			fmt.Fprint(w, `[{"language":"FI","name":"Finnish"}]`)
		default:
			fmt.Fprint(w, `[{"language":"SV","name":"Swedish"}]`)
		}
	}))
	defer gateway.Close()

	cache := NewLanguageCache(languageTTL, languageFetchTimeout)
	ctx := context.Background()
	cache.Languages(ctx, DeepL{APIKey: "test-key", BaseURL: direct.URL})
	viaGateway, _ := cache.Languages(ctx, DeepL{APIKey: "test-key", BaseURL: gateway.URL})

	if !sourceCodes(viaGateway)["fi"] || sourceCodes(viaGateway)["en"] {
		t.Errorf("gateway engine read the wrong entry: %v", sourceCodes(viaGateway))
	}
}

func TestLanguageCache_MockAndLLMNeverFetch(t *testing.T) {
	cache := NewLanguageCache(languageTTL, languageFetchTimeout)
	ctx := context.Background()

	want := len(mockLanguages().Sources)
	if got := len(catalogOnly(cache.Languages(ctx, Mock{})).Sources); got != want {
		t.Errorf("mock: got %d languages, want %d", got, want)
	}
	if got := len(catalogOnly(cache.Languages(ctx, LLMEngine{EngineName: "ai:Acme"})).Sources); got != want {
		t.Errorf("LLM engine: got %d languages, want the mock table's %d", got, want)
	}
}

// Both http.NewRequest and http.Client.Do wrap failures in a *url.Error that
// quotes the whole URL, and Google authenticates with a `key` query parameter.
// Every one of those messages is wrapped into a log line, so none may carry the
// key. The transport case is the one that fires in practice — a timeout, a DNS
// failure, a refused connection.
func TestFetchLanguages_ErrorNeverCarriesTheAPIKey(t *testing.T) {
	const secret = "super-secret-key"
	cases := []struct {
		name    string
		baseURL string
	}{
		{"malformed endpoint (url.Parse fails)", "://malformed"},
		{"unreachable endpoint (transport fails)", "http://127.0.0.1:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, engine := range []LanguageLister{
				Google{APIKey: secret, BaseURL: tc.baseURL},
				DeepL{APIKey: secret, BaseURL: tc.baseURL},
			} {
				_, err := engine.FetchLanguages(context.Background())
				if err == nil {
					t.Fatalf("%s: expected an error", engine.Name())
				}
				if strings.Contains(err.Error(), secret) {
					t.Errorf("%s: API key leaked into %q", engine.Name(), err.Error())
				}
			}
		})
	}
}

// The translate path carries the same key in the same query string.
func TestGoogleTranslate_ErrorNeverCarriesTheAPIKey(t *testing.T) {
	const secret = "super-secret-key"
	engine := Google{APIKey: secret, BaseURL: "http://127.0.0.1:1"}
	_, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en", TargetLang: "de", Texts: []string{"hello"},
	})
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("API key leaked into %q", err.Error())
	}
}

// An owner or admin sets the endpoint and the process is shared by every tenant on the
// replica, so the number of languages a reply may declare is bounded rather
// than trusted.
func TestFetchLanguages_RefusesAnOversizedCatalog(t *testing.T) {
	google := func(n int) string {
		rows := make([]string, n)
		for i := range rows {
			rows[i] = fmt.Sprintf(`{"language":"x%d","name":"X"}`, i)
		}
		return `{"data":{"languages":[` + strings.Join(rows, ",") + `]}}`
	}
	deepl := func(n int) string {
		rows := make([]string, n)
		for i := range rows {
			rows[i] = fmt.Sprintf(`{"language":"X%d","name":"X"}`, i)
		}
		return "[" + strings.Join(rows, ",") + "]"
	}

	for _, tc := range []struct {
		name   string
		body   func(int) string
		engine func(url string) LanguageLister
	}{
		{"google", google, func(u string) LanguageLister { return Google{APIKey: "k", BaseURL: u} }},
		{"deepl", deepl, func(u string) LanguageLister { return DeepL{APIKey: "k", BaseURL: u} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body(n))
			}))
			defer srv.Close()

			n = maxLanguageEntries + 1
			if _, err := tc.engine(srv.URL).FetchLanguages(context.Background()); err == nil {
				t.Errorf("accepted %d languages, cap is %d", n, maxLanguageEntries)
			}

			// The cap must not refuse a catalog the size a real vendor publishes.
			n = maxLanguageEntries
			if _, err := tc.engine(srv.URL).FetchLanguages(context.Background()); err != nil {
				t.Errorf("refused a catalog at the cap: %v", err)
			}
		})
	}
}

// The entry cap counts codes; it does not bound their length. 500 codes of 4 KB
// sit inside the body cap, and each is held twice and echoed by the endpoint —
// so length needs its own bound. langcode.Normalize checks neither.
func TestFetchLanguages_RefusesAnOversizedCode(t *testing.T) {
	long := strings.Repeat("a", maxLanguageCodeLen+1)
	for _, tc := range []struct {
		name   string
		body   string
		engine func(url string) LanguageLister
	}{
		{"google", fmt.Sprintf(`{"data":{"languages":[{"language":%q,"name":"X"}]}}`, long),
			func(u string) LanguageLister { return Google{APIKey: "k", BaseURL: u} }},
		{"deepl", fmt.Sprintf(`[{"language":%q,"name":"X"}]`, long),
			func(u string) LanguageLister { return DeepL{APIKey: "k", BaseURL: u} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			if _, err := tc.engine(srv.URL).FetchLanguages(context.Background()); err == nil {
				t.Errorf("accepted a %d-character language code, cap is %d", len(long), maxLanguageCodeLen)
			}
		})
	}
}

// The control the cap test needs: a code AT the limit is accepted, so tightening
// the bound cannot leave the package green. Every other acceptance test uses
// codes of 2–5 characters.
func TestFetchLanguages_AcceptsACodeAtTheLimit(t *testing.T) {
	atLimit := strings.Repeat("a", maxLanguageCodeLen)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":{"languages":[{"language":%q,"name":"X"},{"language":"de","name":"German"}]}}`, atLimit)
	}))
	defer srv.Close()

	cat, err := Google{APIKey: "k", BaseURL: srv.URL}.FetchLanguages(context.Background())
	if err != nil {
		t.Fatalf("refused a code at the cap: %v", err)
	}
	if !sourceCodes(cat)[atLimit] {
		t.Errorf("a %d-character code should survive: %v", len(atLimit), sourceCodes(cat))
	}
}

// The two-list shape exists for this: DeepL's asymmetry is between the lists,
// not between one source and the next. EN is a source and never a target; EN-GB
// the reverse.
func TestDeepLCatalog_IsAsymmetric(t *testing.T) {
	srv := deeplStub(t, nil)
	cache := NewLanguageCache(languageTTL, languageFetchTimeout)
	cat, _ := cache.Languages(context.Background(), DeepL{APIKey: "test-key", BaseURL: srv.URL})

	src, tgt := sourceCodes(cat), targetCodes(cat)
	if !src["en"] {
		t.Error("en must be a source")
	}
	if !tgt["en-gb"] || !tgt["en-us"] {
		t.Errorf("en-gb and en-us must be targets: %v", tgt)
	}
	if src["en-gb"] {
		t.Error("en-gb is target-only — it must not appear as a source")
	}
	// The base-code pass adds bare en as a target (DeepL publishes none), which
	// is what keeps a plain en project from being refused.
	if !tgt["en"] {
		t.Error("the base code en must be derived as a target")
	}
	if cat.PerSource != nil {
		t.Error("DeepL has no per-source lists; PerSource must stay nil")
	}
}

// LibreTranslate is the one engine whose targets really do differ per source,
// so it is the one that fills PerSource.
func TestLibreTranslateCatalog_FillsPerSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"code":"en","name":"English","targets":["de","fr"]},
		                {"code":"de","name":"German","targets":["en"]}]`)
	}))
	defer srv.Close()

	cache := NewLanguageCache(languageTTL, languageFetchTimeout)
	cat, _ := cache.Languages(context.Background(), LibreTranslate{APIURL: srv.URL})

	if cat.PerSource == nil {
		t.Fatal("LibreTranslate must publish per-source targets")
	}
	if !targetsOf(t, cat, "en")["fr"] {
		t.Error("en reaches fr")
	}
	// The point: de does NOT reach fr, though the catalog knows fr exists. A
	// shared target list would say it does.
	if targetsOf(t, cat, "de")["fr"] {
		t.Error("de must not reach fr — its own target list has only en")
	}
}

// withBaseCodes derives a base source from a regional one, so `en` appears in
// Sources even when the instance only published `en-gb`. PerSource is keyed on
// what the instance sent, so the derived source has no entry — and targetSet
// returns nil on a key miss rather than falling through. The language is then
// offered in the picker and refused at translate time, on a word the engine
// never said.
func TestLibreTranslateCatalog_DerivedBaseSourceKeepsItsTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"code":"en-gb","name":"British English","targets":["de","fr"]}]`)
	}))
	defer srv.Close()

	cache := NewLanguageCache(languageTTL, languageFetchTimeout)
	engine := LibreTranslate{APIURL: srv.URL}
	cat, _ := cache.Languages(context.Background(), engine)

	if !sourceCodes(cat)["en"] {
		t.Fatal("en should be derived as a source from en-gb")
	}
	if !targetsOf(t, cat, "en")["de"] {
		t.Error("the derived source must reach what its regional form reaches")
	}
	if v := ValidateAssignment(context.Background(), "en", "de", engine); v.Rejected {
		t.Errorf("en→de refused on a catalog that covers it: %s", v.Reason)
	}
}

// PerSource is the only field that grows with sources × targets, and the other
// bounds all cap one list at a time. 500 sources naming 500 targets each fit
// inside the body cap, so the product needs a bound of its own.
func TestLibreTranslateCatalog_RefusesAnOversizedPerSourceMap(t *testing.T) {
	// Rows of 100 targets each, drawn from one shared pool of target codes —
	// what a fully-connected instance looks like, and the cheapest way to spend
	// the body on per-source entries.
	body := func(total int) string {
		var rows []string
		for src := 0; total > 0; src++ {
			n := min(100, total)
			total -= n
			targets := make([]string, n)
			for i := range targets {
				targets[i] = fmt.Sprintf(`"z%c%c"`, 'a'+byte(i/26), 'a'+byte(i%26))
			}
			rows = append(rows, fmt.Sprintf(`{"code":"q%c%c","name":"X","targets":[%s]}`,
				'a'+byte(src/26), 'a'+byte(src%26), strings.Join(targets, ",")))
		}
		return "[" + strings.Join(rows, ",") + "]"
	}

	var total int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body(total))
	}))
	defer srv.Close()
	engine := LibreTranslate{APIURL: srv.URL}

	total = maxPerSourceTargets + 100
	if _, err := engine.FetchLanguages(context.Background()); err == nil {
		t.Errorf("accepted %d per-source targets, cap is %d", total, maxPerSourceTargets)
	}

	// The cap must not refuse a catalog the size a real instance publishes.
	total = maxPerSourceTargets
	cat, err := engine.FetchLanguages(context.Background())
	if err != nil {
		t.Fatalf("refused a catalog at the cap: %v", err)
	}
	got := 0
	for _, targets := range cat.PerSource {
		got += len(targets)
	}
	if got != total {
		t.Errorf("kept %d per-source targets, sent %d", got, total)
	}
}
