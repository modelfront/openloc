// Package langcode provides language code normalization for the TMS.
//
// All language codes stored in the database use a canonical format:
// lowercase BCP 47 with hyphens (e.g., "en", "fr-fr", "pt-br", "zh-hans").
//
// Every language code entering the system through API handlers must be
// normalized before storage. This ensures exact-match SQL queries work
// consistently regardless of how users or MT engines format their codes.
package langcode

import "strings"

// Wildcard is the sentinel language code meaning "any language" in routing rules
// (AI engine rows). A row with Wildcard as its source and/or a target matches any
// source/target when resolving a language pair. It is intentionally not a real
// BCP 47 code, so Normalize leaves it untouched ("*" → "*").
const Wildcard = "*"

// Known legacy aliases — map obsolete codes to current ones.
//
// NOTE: `nb` (Norwegian Bokmål) is intentionally NOT mapped to `no`. Per
// CLDR/ICU 39+, `nb` (Bokmål) and `nn` (Nynorsk) are distinct from `no` (the
// Norwegian macrolanguage parent). Mapping `nb → no` breaks pipelines: the
// MT engines we integrate (DeepL, Google, mock) advertise `nb` natively
// but not `no`, so a normalized `no-no` code fails resolution.
var aliases = map[string]string{
	"iw":  "he",  // Hebrew (old ISO 639 code)
	"jw":  "jv",  // Javanese (old ISO 639 code)
	"in":  "id",  // Indonesian (old ISO 639 code)
	"mo":  "ro",  // Moldavian → Romanian
	"fil": "tl",  // Filipino → Tagalog (ISO 639-1)
}

// Normalize converts any language code variant to canonical lowercase BCP 47 format.
//
// Examples:
//
//	"EN-US"   → "en-us"
//	"fr-FR"   → "fr-fr"
//	"pt_BR"   → "pt-br"
//	"zh-Hans" → "zh-hans"
//	"FR"      → "fr"
//	"iw"      → "he"
//	""        → ""
func Normalize(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}

	// Lowercase and replace underscores with hyphens.
	code = strings.ToLower(code)
	code = strings.ReplaceAll(code, "_", "-")

	// Apply legacy aliases to the primary subtag.
	parts := strings.SplitN(code, "-", 2)
	if replacement, ok := aliases[parts[0]]; ok {
		parts[0] = replacement
	}

	return strings.Join(parts, "-")
}

// NormalizeAll normalizes a slice of language codes.
func NormalizeAll(codes []string) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = Normalize(c)
	}
	return out
}

// BaseLang returns the primary language subtag (everything before the first hyphen).
//
// Examples:
//
//	"fr-fr" → "fr"
//	"pt-br" → "pt"
//	"en"    → "en"
//	"zh-hans" → "zh"
func BaseLang(code string) string {
	if i := strings.IndexByte(code, '-'); i >= 0 {
		return code[:i]
	}
	return code
}

// SameBase reports whether two codes share a primary language subtag — i.e.
// they're the same language, possibly in different regions or scripts.
// Codes differing only this way are "sublanguage" variants of each other.
//
// Expects normalized input: SameBase("en_us", "en") is false, because the
// un-normalized code has no parseable base subtag.
//
//	"en-us", "en"      → true
//	"en-us", "en-gb"   → true
//	"zh-hans", "zh-hant" → true
//	"en", "fr"         → false
func SameBase(a, b string) bool {
	return BaseLang(a) == BaseLang(b)
}
