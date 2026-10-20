package spellcheck

import (
	"context"
	"os"
	"testing"
)

// TestToLTLanguage pins the exact LanguageTool code for each language.
// LanguageTool accepts a bare "de" with HTTP 200 and an empty match list, so a
// wrong code here produces no error anywhere — no failed count, no log line, no
// toast. Only a test that pins the exact string catches a wrong mapping.
func TestToLTLanguage(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// A bare "de" returns zero matches from LanguageTool.
		{"german base resolves to de-DE", "de", "de-DE"},
		{"english base resolves to en-US", "en", "en-US"},
		{"portuguese base resolves to pt-PT", "pt", "pt-PT"},
		{"catalan base resolves to ca-ES", "ca", "ca-ES"},

		// Languages whose bare code has a working speller must NOT be remapped —
		// picking es-AR for "es" would silently impose a region.
		{"spanish base passes through", "es", "es"},
		{"french base passes through", "fr", "fr"},
		{"dutch base passes through", "nl", "nl"},
		{"italian base passes through", "it", "it"},

		// Languages LanguageTool only ships regional forms for.
		{"polish gets its region", "pl", "pl-PL"},
		{"russian gets its region", "ru", "ru-RU"},
		{"chinese gets its region", "zh", "zh-CN"},
		{"japanese gets its region", "ja", "ja-JP"},
		{"ukrainian gets its region", "uk", "uk-UA"},

		// An explicit region is honoured and re-uppercased (LanguageTool matches
		// variant codes case-sensitively; our storage is lowercase).
		{"explicit region uppercased", "pt-br", "pt-BR"},
		{"explicit region already upper", "en-GB", "en-GB"},
		{"austrian german kept", "de-at", "de-AT"},
		{"underscore form normalized", "en_us", "en-US"},

		// A script subtag is not a LanguageTool code — fall back to the base's
		// default variant rather than sending a request guaranteed to 400.
		{"script subtag falls back to variant", "zh-hans", "zh-CN"},

		// An unknown language passes through so LanguageTool can reject it
		// loudly (which surfaces in the `failed` count) rather than us guessing.
		{"unknown language passes through", "xx", "xx"},

		{"empty stays empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toLTLanguage(tt.in); got != tt.want {
				t.Errorf("toLTLanguage(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Every mapped variant must carry a region, or the map isn't doing its job.
func TestLTDefaultVariantsAreRegional(t *testing.T) {
	for base, variant := range ltDefaultVariants {
		if variant == base {
			t.Errorf("%s maps to itself — pointless entry", base)
		}
		if len(variant) <= len(base)+1 {
			t.Errorf("%s maps to %q, which carries no region subtag", base, variant)
		}
	}
}

func TestFilterMatchesDropsNoisyCategories(t *testing.T) {
	mk := func(categoryID string) Match {
		m := Match{}
		m.Rule.Category.ID = categoryID
		return m
	}
	in := []Match{mk("TYPOS"), mk("WHITESPACE"), mk("GRAMMAR"), mk("TYPOGRAPHY"), mk("CASING")}

	got := filterMatches(in)
	if len(got) != 3 {
		t.Fatalf("kept %d matches, want 3 (TYPOS, GRAMMAR, CASING)", len(got))
	}
	for _, m := range got {
		if m.Rule.Category.ID == "WHITESPACE" || m.Rule.Category.ID == "TYPOGRAPHY" {
			t.Errorf("category %s should be suppressed", m.Rule.Category.ID)
		}
	}
}

// TestLiveLanguageTool checks a real LanguageTool server, opt-in.
//
// Skipped by default so CI never depends on the container. It exists because the
// bug this file mostly guards against — a language code that returns HTTP 200
// with zero matches — is invisible to stubbed tests by construction: a stub
// returns whatever we tell it to, including for a code the real server would
// silently ignore.
//
//	SPELLCHECK_LIVE_TEST=1 SPELLCHECK_LT_URL=http://localhost:9008/v2/check \
//	  go test ./internal/spellcheck/ -run TestLiveLanguageTool -v
func TestLiveLanguageTool(t *testing.T) {
	if os.Getenv("SPELLCHECK_LIVE_TEST") == "" {
		t.Skip("set SPELLCHECK_LIVE_TEST=1 to run against a real LanguageTool")
	}
	url := os.Getenv("SPELLCHECK_LT_URL")
	if url == "" {
		url = "http://localhost:9008/v2/check"
	}

	client := NewClient(url)
	// The seeded demo segment. "adresse" must be Adresse in German.
	const text = "Bitte bestätigen Sie die adresse für die Lieferung."

	// "de" is what jobs actually store; it must resolve to a variant that spell
	// checks.
	matches, err := client.Check(context.Background(), text, "de")
	if err != nil {
		t.Fatalf("live check failed: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no matches for %q with language \"de\" — the base code is not spell checking", text)
	}

	var found bool
	for _, m := range matches {
		word, ok := flaggedText(text, m.Offset, m.Length)
		if ok && word == "adresse" {
			found = true
			if len(m.Replacements) == 0 || m.Replacements[0].Value != "Adresse" {
				t.Errorf("top suggestion for %q = %+v, want Adresse", word, m.Replacements)
			}
		}
	}
	if !found {
		t.Errorf("no match pointed at \"adresse\"; got %d matches", len(matches))
	}
}

// TestLiveLanguageToolBatch runs the joined-request path against a real
// server — the offset split is exactly the kind of math a stub can't falsify,
// because the stub and the splitter could share the same wrong assumption
// about LanguageTool's offset units.
func TestLiveLanguageToolBatch(t *testing.T) {
	if os.Getenv("SPELLCHECK_LIVE_TEST") == "" {
		t.Skip("set SPELLCHECK_LIVE_TEST=1 to run against a real LanguageTool")
	}
	url := os.Getenv("SPELLCHECK_LT_URL")
	if url == "" {
		url = "http://localhost:9008/v2/check"
	}

	client := NewClient(url)
	// Umlauts before each typo so byte offsets and UTF-16 offsets diverge, and
	// a clean segment between the dirty ones so attribution errors can't hide.
	items := []BatchItem{
		{ID: 1, Text: "Bitte bestätigen Sie die adresse für die Lieferung."},
		{ID: 2, Text: "Die Prüfung war völlig in Ordnung."},
		{ID: 3, Text: "Wir prüfen die lieferadresse morgen früh."},
	}

	results := client.CheckBatch(context.Background(), items, "de")

	for i, res := range results {
		if res.Err != nil {
			t.Fatalf("item %d: %v", i, res.Err)
		}
	}
	assertFlags := func(i int, want string) {
		t.Helper()
		for _, m := range results[i].Matches {
			word, ok := flaggedText(items[i].Text, m.Offset, m.Length)
			if ok && word == want {
				return
			}
		}
		t.Errorf("item %d: no match points at %q; got %+v", i, want, results[i].Matches)
	}
	assertFlags(0, "adresse")
	assertFlags(2, "lieferadresse")
	if len(results[1].Matches) != 0 {
		t.Errorf("clean segment got matches — misattributed offsets? %+v", results[1].Matches)
	}
}
