package mt

import (
	"context"
	"testing"
)

// staticEngine names an engine type without implementing LanguageLister, so
// SupportedLanguages serves that type's compiled-in table. Locale resolution is
// what these tests are about, and a real engine here would make them depend on
// whether a LibreTranslate instance happens to be running.
type staticEngine struct{ name string }

func (e staticEngine) Name() string { return e.name }

func (e staticEngine) Translate(context.Context, TranslateRequest) ([]TranslateResult, error) {
	panic("staticEngine does not translate")
}

var libre = staticEngine{name: "libretranslate"}

// publishedEngine states its own catalog, the way a vendor that answers a
// languages endpoint does. Only engines that publish one can refuse a pair, so
// tests about refusal need this rather than staticEngine. endpoint merely has
// to be unique per test — FetchLanguages never touches the network.
type publishedEngine struct {
	endpoint string
	catalog  Catalog
}

func (e publishedEngine) Name() string             { return "libretranslate" }
func (e publishedEngine) LanguageEndpoint() string { return e.endpoint }

func (e publishedEngine) FetchLanguages(context.Context) (Catalog, error) {
	return e.catalog, nil
}

func (e publishedEngine) Translate(context.Context, TranslateRequest) ([]TranslateResult, error) {
	panic("publishedEngine does not translate")
}

var _ LanguageLister = publishedEngine{}

func TestResolveLocale_DirectSupport(t *testing.T) {
	// Mock engine supports es-mx natively — no fallback needed.
	got := ResolveLocale(context.Background(), "es-mx", Mock{})
	if got != "es-mx" {
		t.Errorf("expected es-mx (direct support), got %q", got)
	}
}

func TestResolveLocale_DirectSupportBaseCode(t *testing.T) {
	// Mock engine supports "es" as a base code — no fallback needed.
	got := ResolveLocale(context.Background(), "es", Mock{})
	if got != "es" {
		t.Errorf("expected es (direct support), got %q", got)
	}
}

func TestResolveLocale_FallbackToBase(t *testing.T) {
	// LibreTranslate only supports "es" (base code), not "es-mx".
	// ResolveLocale should strip the region and fall back to "es".
	got := ResolveLocale(context.Background(), "es-mx", libre)
	if got != "es" {
		t.Errorf("expected es (fallback from es-mx), got %q", got)
	}
}

func TestResolveLocale_FallbackFrCA_LibreTranslate(t *testing.T) {
	// LibreTranslate has fr but not fr-ca.
	got := ResolveLocale(context.Background(), "fr-ca", libre)
	if got != "fr" {
		t.Errorf("expected fr (fallback from fr-ca), got %q", got)
	}
}

func TestResolveLocale_Unsupported(t *testing.T) {
	// Truly unsupported language — no base code match either.
	// Returns the original code so the engine can produce its own error.
	got := ResolveLocale(context.Background(), "xx-yy", libre)
	if got != "xx-yy" {
		t.Errorf("expected xx-yy (passthrough), got %q", got)
	}
}

func TestResolveLocale_ZhHans(t *testing.T) {
	// Mock supports zh-hans natively — direct match.
	got := ResolveLocale(context.Background(), "zh-hans", Mock{})
	if got != "zh-hans" {
		t.Errorf("expected zh-hans (direct), got %q", got)
	}
}

func TestResolveLocale_DeAtFallback(t *testing.T) {
	// LibreTranslate supports "de" as a base code — de-at falls back to it.
	got := ResolveLocale(context.Background(), "de-at", libre)
	if got != "de" {
		t.Errorf("expected de (fallback from de-at), got %q", got)
	}
}

func TestResolveLocalePair_BothFallToBase_LibreTranslate(t *testing.T) {
	// Resolving only the target leaves "en-us → de", which LibreTranslate
	// refuses on the source side. Both sides fall back.
	src, tgt := ResolveLocalePair(context.Background(), "en-us", "de-de", libre)
	if src != "en" || tgt != "de" {
		t.Errorf("expected en→de, got %s→%s", src, tgt)
	}
}

func TestResolveLocalePair_DirectSupportSkipsFallback(t *testing.T) {
	// Mock supports en-us and de-de natively — pair must pass through.
	src, tgt := ResolveLocalePair(context.Background(), "en-us", "de-de", Mock{})
	if src != "en-us" || tgt != "de-de" {
		t.Errorf("expected en-us→de-de (direct), got %s→%s", src, tgt)
	}
}

func TestResolveLocalePair_MixedSupport(t *testing.T) {
	// Mock supports en-us natively but not "xx-yy" — passthrough on the
	// unsupported side, normal resolution on the other.
	src, tgt := ResolveLocalePair(context.Background(), "en-us", "xx-yy", Mock{})
	if src != "en-us" || tgt != "xx-yy" {
		t.Errorf("expected en-us→xx-yy (passthrough on unsupported), got %s→%s", src, tgt)
	}
}

// An LLM translation engine takes any pair, so regional and script codes reach
// its prompt exactly as the project names them.
func TestResolveLocalePair_LLMEngineKeepsEveryLocale(t *testing.T) {
	llm := LLMEngine{EngineName: "ai:Acme Translator"}
	for _, pair := range [][2]string{{"en-us", "de-de"}, {"en", "es-ar"}, {"en", "zh-tw"}, {"pt-br", "de-ch"}} {
		src, tgt := ResolveLocalePair(context.Background(), pair[0], pair[1], llm)
		if src != pair[0] || tgt != pair[1] {
			t.Errorf("%s→%s resolved to %s→%s", pair[0], pair[1], src, tgt)
		}
		if got := ResolveLocale(context.Background(), pair[1], llm); got != pair[1] {
			t.Errorf("ResolveLocale(%s) = %s", pair[1], got)
		}
	}
}

func zhCatalog(codes ...string) Catalog {
	langs := []LanguageInfo{{Code: "en"}}
	for _, c := range codes {
		langs = append(langs, LanguageInfo{Code: c})
	}
	return symmetric(langs)
}

// A Chinese region names a script. It resolves to the catalog's code for that
// script, and Traditional never falls back to the bare "zh", which vendors
// read as Simplified.
func TestResolveLocalePair_ChineseRegionsKeepTheirScript(t *testing.T) {
	deepl := publishedEngine{endpoint: "zh-deepl", catalog: zhCatalog("zh-hans", "zh-hant")}
	google := publishedEngine{endpoint: "zh-google", catalog: zhCatalog("zh-cn", "zh-tw")}
	bare := publishedEngine{endpoint: "zh-bare", catalog: zhCatalog("zh")}
	// LibreTranslate names Traditional Chinese "zt".
	libre := publishedEngine{endpoint: "zh-libre", catalog: zhCatalog("zh", "zt")}
	cases := []struct {
		engine       publishedEngine
		target, want string
	}{
		{deepl, "zh-tw", "zh-hant"}, {deepl, "zh-hk", "zh-hant"}, {deepl, "zh-mo", "zh-hant"},
		{deepl, "zh-cn", "zh-hans"}, {deepl, "zh-sg", "zh-hans"}, {deepl, "zh", "zh"},
		{google, "zh-hant", "zh-tw"}, {google, "zh-hk", "zh-tw"}, {google, "zh-hans", "zh-cn"},
		{google, "zh-sg", "zh-cn"}, {google, "zh-tw", "zh-tw"},
		{bare, "zh-cn", "zh"}, {bare, "zh-hans", "zh"}, {bare, "zh-tw", "zh-tw"}, {bare, "zh-hant", "zh-hant"},
		{libre, "zh-tw", "zt"}, {libre, "zh-hant", "zt"}, {libre, "zh-hk", "zt"}, {libre, "zh-cn", "zh"},
	}
	for _, c := range cases {
		if _, got := ResolveLocalePair(context.Background(), "en", c.target, c.engine); got != c.want {
			t.Errorf("%s: en→%s resolved to %s, want %s", c.engine.endpoint, c.target, got, c.want)
		}
	}
}
