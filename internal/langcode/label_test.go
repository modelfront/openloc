package langcode

import "testing"

// A prompt names its language pair, and the model acts on what it reads there.
// These cases are the ones where a bare code and a bare name each mislead.
func TestLabel(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		// The pair the whole feature is for: "German" would lose the variant,
		// "de-at" would lose the language.
		{"regional variant keeps both halves", "de-at", "Austrian German (de-AT)"},
		{"plain language", "en", "English (en)"},
		// The database stores lowercase; the prompt shows canonical BCP 47.
		{"code is recased to canonical BCP 47", "pt-br", "Brazilian Portuguese (pt-BR)"},
		{"script subtag", "zh-hans", "Simplified Chinese (zh-Hans)"},
		{"region is a UN M.49 number", "es-419", "Latin American Spanish (es-419)"},
		{"already canonical", "en-GB", "British English (en-GB)"},
		{"underscore separator", "fr_CA", "Canadian French (fr-CA)"},

		// Pass-through. A label is a nicety; the code is the fact, so anything
		// unnameable must survive intact rather than be dropped or guessed at.
		{"wildcard sentinel", Wildcard, Wildcard},
		{"unknown subtag", "xx-YY", "xx-YY"},
		{"not a code at all", "klingon", "klingon"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},

		// The preview substitutes the tokens with themselves when no job is in
		// hand, so the marker must not be mangled into a fake language.
		{"preview marker", "<source_lang>", "<source_lang>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Label(tt.code); got != tt.want {
				t.Errorf("Label(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

// Office files write language tags in canonical casing; the database stores
// lowercase. A file stamped "es-mx" still works in PowerPoint, but every other
// run in the deck says "es-MX", so the export must match.
func TestBCP47(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"es-mx", "es-MX"},
		{"pt-br", "pt-BR"},
		{"zh-hans", "zh-Hans"},
		{"es-419", "es-419"},
		{"de", "de"},
		{"fr_CA", "fr-CA"},
		{"en-GB", "en-GB"},
		{" de-at ", "de-AT"},
		{"not a code", "not a code"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := BCP47(tt.code); got != tt.want {
			t.Errorf("BCP47(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

// A spreadsheet heading counts as a language code only when it names one.
func TestKnown(t *testing.T) {
	for _, code := range []string{"en", "fr", "id", "pt-br", "zh-hans", "de-at", "en-us", "es-mx", "sr-latn", "fil", "yue", "ckb"} {
		if !Known(code) {
			t.Errorf("Known(%q) = false, want a language", code)
		}
	}
	for _, code := range []string{"sku", "qty", "url", "key", "tag", "row", "tax", "xx", "xx-yy", "klingon", "", Wildcard} {
		if Known(code) {
			t.Errorf("Known(%q) = true, want no language", code)
		}
	}
}
