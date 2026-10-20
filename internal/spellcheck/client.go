package spellcheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"openloc/internal/langcode"
)

// Match represents a single spelling/grammar issue from LanguageTool.
type Match struct {
	Message      string        `json:"message"`
	Offset       int           `json:"offset"`
	Length       int           `json:"length"`
	Replacements []Replacement `json:"replacements"`
	Rule         Rule          `json:"rule"`
}

type Replacement struct {
	Value string `json:"value"`
}

type Rule struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Category    struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"category"`
}

type ltResponse struct {
	Matches []Match `json:"matches"`
}

// Rule categories suppressed before results reach the UI.
//
// CAT segments are fragments, not prose: leading/trailing spaces are often
// deliberate (they matter for concatenated output), and typographic rules fire
// on every straight quote and hyphen in source-derived text. Left in, these
// two categories bury the actual misspellings a linguist opened the dialog to
// find. Everything else LanguageTool reports — TYPOS, GRAMMAR, CASING,
// CONFUSED_WORDS and friends — is kept.
var suppressedCategories = map[string]bool{
	"WHITESPACE": true,
	"TYPOGRAPHY": true,
}

// Sent as `disabledCategories` on every request so LanguageTool skips
// evaluating these rules entirely; filterMatches stays as the net in case a
// match arrives under a differently-spelled category id.
var disabledCategoriesParam = strings.Join(slices.Sorted(maps.Keys(suppressedCategories)), ",")

const (
	cacheTTL        = 10 * time.Minute
	cacheMaxEntries = 4096
)

// Client talks to a LanguageTool /v2/check endpoint.
type Client struct {
	url   string
	http  *http.Client
	cache *ttlCache
}

func NewClient(ltURL string) *Client {
	return &Client{
		url: ltURL,
		// Sized for a joined request (up to maxGroupUnits) on a busy server, not
		// a single segment.
		http:  &http.Client{Timeout: 60 * time.Second},
		cache: newTTLCache(),
	}
}

// Check runs one text through LanguageTool. Results are cached by
// (language, text) — repeated segments and re-runs after a partial fix are
// common enough in a document-wide check that this measurably cuts the work.
func (c *Client) Check(ctx context.Context, text, language string) ([]Match, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	lang := toLTLanguage(language)
	if lang == "" {
		return nil, fmt.Errorf("spellcheck: empty language code")
	}

	key := cacheKey(lang, text)
	if cached, ok := c.cache.get(key); ok {
		return cached, nil
	}

	raw, err := c.doCheck(ctx, text, lang)
	if err != nil {
		return nil, err
	}

	matches := filterMatches(raw)
	c.cache.set(key, matches)
	return matches, nil
}

// doCheck is the raw LanguageTool round trip: no cache, no category filter,
// `lang` already in LT form.
func (c *Client) doCheck(ctx context.Context, text, lang string) ([]Match, error) {
	form := url.Values{
		"text":     {text},
		"language": {lang},
		// Also suppressed client-side in filterMatches; disabling them here means
		// LT skips evaluating rules whose matches we'd discard anyway.
		"disabledCategories": {disabledCategoriesParam},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spellcheck: reaching LanguageTool: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("spellcheck: reading LanguageTool response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// An unrecognised language makes LanguageTool echo the full request
		// back. statusError keeps the body out of logs; the truncation bounds
		// its Error() text.
		return nil, &statusError{status: resp.StatusCode, body: truncate(string(body), 200)}
	}

	var parsed ltResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("spellcheck: parsing LanguageTool response: %w", err)
	}
	return parsed.Matches, nil
}

func filterMatches(matches []Match) []Match {
	out := make([]Match, 0, len(matches))
	for _, m := range matches {
		if suppressedCategories[strings.ToUpper(m.Rule.Category.ID)] {
			continue
		}
		out = append(out, m)
	}
	return out
}

// ltDefaultVariants maps a base language code to the LanguageTool code that
// actually has a spell checker behind it.
//
// Two separate traps live here, and the first one is silent:
//
//  1. For English, German, Portuguese and Catalan, LanguageTool wants the
//     variant named. Passing the bare code is *accepted* — HTTP 200,
//     `warnings.incompleteResults: false`, and an EMPTY match list. No error to
//     log, no warning to surface: spell check simply finds nothing
//     (`de` → 0 matches, `de-DE` → the typo).
//
//  2. For ~20 other languages LanguageTool ships only regional forms (pl-PL,
//     ru-RU, zh-CN …) and rejects the bare code outright. That one at least
//     fails loudly and lands in the `failed` count.
//
// Languages deliberately absent: es, fr, nl, it, ar, eo, fa, sv all have a
// working speller under the bare code, so remapping them would silently pick a
// region the user never asked for (es → es-AR rather than generic Spanish).
//
// Regenerate after a LanguageTool upgrade:
//
//	docker compose exec -T languagetool wget -qO- http://localhost:8010/v2/languages
//
// then map each base whose only entries carry a region, plus the four
// variant-required languages above.
var ltDefaultVariants = map[string]string{
	// Variant required despite the bare code being accepted (trap 1).
	"en": "en-US",
	"de": "de-DE",
	"pt": "pt-PT",
	// Only regional forms exist (trap 2).
	"ca":  "ca-ES",
	"ast": "ast-ES",
	"be":  "be-BY",
	"br":  "br-FR",
	"crh": "crh-UA",
	"da":  "da-DK",
	"el":  "el-GR",
	"ga":  "ga-IE",
	"gl":  "gl-ES",
	"ja":  "ja-JP",
	"km":  "km-KH",
	"pl":  "pl-PL",
	"ro":  "ro-RO",
	"ru":  "ru-RU",
	"sk":  "sk-SK",
	"sl":  "sl-SI",
	"ta":  "ta-IN",
	"tl":  "tl-PH",
	"uk":  "uk-UA",
	"zh":  "zh-CN",
}

// toLTLanguage converts a stored language code to the form LanguageTool expects.
//
// Codes are canonically lowercase internally (langcode.Normalize) but
// LanguageTool matches variants case-sensitively — "pt-BR", not "pt-br" — so a
// region subtag is re-uppercased. A base-only code is resolved through
// ltDefaultVariants; see that map for why passing the base through unchanged
// silently disables spell checking for some languages.
func toLTLanguage(code string) string {
	normalized := langcode.Normalize(code)
	if normalized == "" {
		return ""
	}

	parts := strings.Split(normalized, "-")
	base := parts[0]

	// Base-only code ("de") → the variant that actually spell-checks.
	if len(parts) == 1 {
		if variant, ok := ltDefaultVariants[base]; ok {
			return variant
		}
		return base
	}

	// A script subtag ("zh-hans") is not a LanguageTool code at all — it would
	// 400. Drop to the base language's default variant, which is closer to what
	// the user meant than a guaranteed failure.
	if len(parts[1]) == 4 {
		if variant, ok := ltDefaultVariants[base]; ok {
			return variant
		}
		return base
	}

	for i := 1; i < len(parts); i++ {
		if len(parts[i]) == 2 {
			parts[i] = strings.ToUpper(parts[i])
		}
	}
	return strings.Join(parts, "-")
}

func cacheKey(language, text string) string {
	sum := sha256.Sum256([]byte(text))
	return language + ":" + hex.EncodeToString(sum[:])
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

type cacheEntry struct {
	matches  []Match
	storedAt time.Time
}

// ttlCache is a small bounded TTL cache. Deliberately not an LRU: entries are
// cheap, the access pattern in a document check is "each segment once", and an
// LRU's bookkeeping would buy nothing here.
type ttlCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}

func newTTLCache() *ttlCache {
	return &ttlCache{entries: make(map[string]cacheEntry)}
}

func (c *ttlCache) get(key string) ([]Match, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Since(entry.storedAt) > cacheTTL {
		delete(c.entries, key)
		return nil, false
	}
	return entry.matches, true
}

func (c *ttlCache) set(key string, matches []Match) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= cacheMaxEntries {
		// Drop expired entries first; if that doesn't free anything the cache is
		// genuinely hot, so reset it rather than growing without bound.
		for k, v := range c.entries {
			if time.Since(v.storedAt) > cacheTTL {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= cacheMaxEntries {
			c.entries = make(map[string]cacheEntry)
		}
	}

	c.entries[key] = cacheEntry{matches: matches, storedAt: time.Now()}
}
