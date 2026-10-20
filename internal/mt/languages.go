package mt

import (
	"context"
	"sort"
)

// LanguageInfo is one language an engine names.
type LanguageInfo struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Catalog is what an engine says it translates: the languages it reads, and the
// languages it writes. Two lists, not a per-source map — a catalog can be
// asymmetric (DeepL offers EN as a source and EN-GB as a target, never the
// reverse) but the asymmetry is between the two lists, not between one source
// and the next.
//
// PerSource is the exception, and only LibreTranslate fills it: its API really
// does publish a different target list per source. nil means every source
// reaches every target, which is how DeepL and Google work.
type Catalog struct {
	Sources   []LanguageInfo      `json:"sources"`
	Targets   []LanguageInfo      `json:"targets"`
	PerSource map[string][]string `json:"per_source,omitempty"`
}

// Empty reports a catalog with nothing in it, which is never served: a read
// falls back to the static table rather than refuse every pair.
func (c Catalog) Empty() bool { return len(c.Sources) == 0 || len(c.Targets) == 0 }

// symmetric builds the common case, where every language reads and writes.
func symmetric(langs []LanguageInfo) Catalog {
	langs = withBaseCodes(langs)
	return Catalog{Sources: langs, Targets: langs}
}

// SupportedLanguages returns the languages an engine supports, live from the
// engine's own API where it publishes one (DeepL, Google, LibreTranslate) and
// cached in process. Engines with no catalog API, no key, or no reachable
// endpoint fall back to the static table for their type.
//
// Never returns an empty list — an empty catalog would reject every language
// pair rather than the ones the engine actually lacks.
func SupportedLanguages(ctx context.Context, engine Engine) Catalog {
	cat, _ := supportedCatalog(ctx, engine)
	return cat
}

// supportedCatalog adds whether the list is the engine's own word or a
// compiled-in stand-in. Refusing a pair is only legitimate on the former.
func supportedCatalog(ctx context.Context, engine Engine) (cat Catalog, fromEngine bool) {
	if engine == nil {
		return staticLanguages(""), false
	}
	return defaultLanguageCache.Languages(ctx, engine)
}

// staticLanguages is the compiled-in catalog for an engine type. It backs the
// mock engine, which has no API to ask, and stands in for the live list when a
// real engine cannot be reached.
//
// `supportedCatalog` reports every static table as not-from-the-engine, so no
// pair is ever refused on one. LLM translation engines publish no catalog and
// skip locale resolution altogether (see anyLanguage): an LLM takes any pair.
func staticLanguages(engineType string) Catalog {
	switch engineType {
	case "libretranslate":
		return libreTranslateFallback()
	case "deepl":
		return deeplLanguages()
	case "google":
		return googleLanguages()
	default:
		return mockLanguages()
	}
}

// languageEntry is one row of a compiled-in static table, before it becomes a
// Catalog.
type languageEntry struct{ code, name string }

// languagePairCatalog builds a symmetric catalog from a flat list — every
// language both reads and writes.
func languagePairCatalog(codes []languageEntry) Catalog {
	langs := make([]LanguageInfo, len(codes))
	for i, c := range codes {
		langs[i] = LanguageInfo{Code: c.code, Name: c.name}
	}
	return symmetric(langs)
}

func mockLanguages() Catalog {
	codes := []languageEntry{
		// Base languages
		{"en", "English"}, {"de", "German"}, {"es", "Spanish"}, {"fr", "French"},
		{"it", "Italian"}, {"pt", "Portuguese"}, {"nl", "Dutch"}, {"pl", "Polish"},
		{"ru", "Russian"}, {"ja", "Japanese"}, {"zh", "Chinese"}, {"ko", "Korean"},
		{"ar", "Arabic"}, {"tr", "Turkish"}, {"sv", "Swedish"}, {"da", "Danish"},
		{"fi", "Finnish"}, {"nb", "Norwegian"}, {"cs", "Czech"}, {"ro", "Romanian"},
		{"uk", "Ukrainian"}, {"el", "Greek"}, {"hu", "Hungarian"}, {"bg", "Bulgarian"},
		// Regional locale variants — the mock engine supports these natively so
		// journey tests can verify locales work end-to-end without MT fallback.
		// Real engines vary: Google supports es-mx, DeepL has es-419, LibreTranslate
		// only supports base codes.
		{"es-mx", "Spanish (Mexico)"}, {"es-419", "Spanish (Latin America)"},
		{"pt-br", "Portuguese (Brazil)"}, {"pt-pt", "Portuguese (Portugal)"},
		{"zh-hans", "Chinese (Simplified)"}, {"zh-hant", "Chinese (Traditional)"},
		{"en-us", "English (US)"}, {"en-gb", "English (UK)"},
		{"fr-ca", "French (Canada)"},
		// J5 exercises Austrian German end-to-end (term base in 'de', project in
		// 'de-at') to prove locale-aware term lookup. Without de-at in the mock
		// catalog, ResolveLocale falls back to 'de' and the mock prefix becomes
		// "[DE] " — the journey then mis-matches "[DE-AT] " in TM write-back
		// assertions. Adding the variant here keeps the journey honest.
		{"de-at", "German (Austria)"}, {"de-de", "German (Germany)"},
	}
	return languagePairCatalog(codes)
}

// libreTranslateFallback returns common LibreTranslate languages when the instance isn't reachable.
func libreTranslateFallback() Catalog {
	codes := []languageEntry{
		{"en", "English"}, {"ar", "Arabic"}, {"zh", "Chinese"},
		{"cs", "Czech"}, {"da", "Danish"}, {"nl", "Dutch"}, {"fi", "Finnish"},
		{"fr", "French"}, {"de", "German"}, {"el", "Greek"}, {"he", "Hebrew"},
		{"hi", "Hindi"}, {"hu", "Hungarian"}, {"id", "Indonesian"}, {"ga", "Irish"},
		{"it", "Italian"}, {"ja", "Japanese"}, {"ko", "Korean"}, {"pl", "Polish"},
		{"pt", "Portuguese"}, {"ru", "Russian"}, {"es", "Spanish"}, {"sv", "Swedish"},
		{"tr", "Turkish"}, {"uk", "Ukrainian"}, {"vi", "Vietnamese"},
	}
	return languagePairCatalog(codes)
}

// deeplLanguages is the base-code catalog used when DeepL cannot be reached.
// The live list also carries regional targets (EN-GB, PT-BR, ZH-HANT); this one
// deliberately does not, because the translate path maps a base code onto
// DeepL's own regional default.
func deeplLanguages() Catalog {
	codes := []languageEntry{
		{"ar", "Arabic"}, {"bg", "Bulgarian"}, {"cs", "Czech"}, {"da", "Danish"},
		{"de", "German"}, {"el", "Greek"}, {"en", "English"}, {"es", "Spanish"},
		{"et", "Estonian"}, {"fi", "Finnish"}, {"fr", "French"}, {"hu", "Hungarian"},
		{"id", "Indonesian"}, {"it", "Italian"}, {"ja", "Japanese"}, {"ko", "Korean"},
		{"lt", "Lithuanian"}, {"lv", "Latvian"}, {"nb", "Norwegian"}, {"nl", "Dutch"},
		{"pl", "Polish"}, {"pt", "Portuguese"}, {"ro", "Romanian"}, {"ru", "Russian"},
		{"sk", "Slovak"}, {"sl", "Slovenian"}, {"sv", "Swedish"}, {"tr", "Turkish"},
		{"uk", "Ukrainian"}, {"zh", "Chinese"},
	}
	return languagePairCatalog(codes)
}

func googleLanguages() Catalog {
	// Major Google Translate languages (subset — full list is 130+)
	codes := []languageEntry{
		{"af", "Afrikaans"}, {"am", "Amharic"}, {"ar", "Arabic"},
		{"be", "Belarusian"}, {"bg", "Bulgarian"}, {"bn", "Bengali"}, {"bs", "Bosnian"},
		{"ca", "Catalan"}, {"cs", "Czech"}, {"cy", "Welsh"}, {"da", "Danish"},
		{"de", "German"}, {"el", "Greek"}, {"en", "English"}, {"es", "Spanish"},
		{"et", "Estonian"}, {"eu", "Basque"}, {"fa", "Persian"}, {"fi", "Finnish"},
		{"fr", "French"}, {"ga", "Irish"}, {"gl", "Galician"}, {"gu", "Gujarati"},
		{"ha", "Hausa"}, {"he", "Hebrew"}, {"hi", "Hindi"}, {"hr", "Croatian"},
		{"hu", "Hungarian"}, {"hy", "Armenian"}, {"id", "Indonesian"}, {"is", "Icelandic"},
		{"it", "Italian"}, {"ja", "Japanese"}, {"ka", "Georgian"}, {"kk", "Kazakh"},
		{"km", "Khmer"}, {"kn", "Kannada"}, {"ko", "Korean"}, {"ku", "Kurdish"},
		{"ky", "Kyrgyz"}, {"la", "Latin"}, {"lo", "Lao"}, {"lt", "Lithuanian"},
		{"lv", "Latvian"}, {"mg", "Malagasy"}, {"mk", "Macedonian"}, {"ml", "Malayalam"},
		{"mn", "Mongolian"}, {"mr", "Marathi"}, {"ms", "Malay"}, {"mt", "Maltese"},
		{"my", "Myanmar"}, {"ne", "Nepali"}, {"nl", "Dutch"}, {"nb", "Norwegian"},
		{"pa", "Punjabi"}, {"pl", "Polish"}, {"ps", "Pashto"}, {"pt", "Portuguese"},
		{"ro", "Romanian"}, {"ru", "Russian"}, {"sd", "Sindhi"}, {"si", "Sinhala"},
		{"sk", "Slovak"}, {"sl", "Slovenian"}, {"so", "Somali"}, {"sq", "Albanian"},
		{"sr", "Serbian"}, {"sv", "Swedish"}, {"sw", "Swahili"}, {"ta", "Tamil"},
		{"te", "Telugu"}, {"tg", "Tajik"}, {"th", "Thai"}, {"tl", "Filipino"},
		{"tr", "Turkish"}, {"uk", "Ukrainian"}, {"ur", "Urdu"}, {"uz", "Uzbek"},
		{"vi", "Vietnamese"}, {"yo", "Yoruba"}, {"zh", "Chinese"}, {"zu", "Zulu"},
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i].code < codes[j].code })

	return languagePairCatalog(codes)
}
