package mt

import "fmt"

// CatalogEngine builds an engine from stored configuration for reading its
// language catalog. Unlike NewFromDB it never fails on a missing key: an engine
// row saved before its key still has a static catalog worth showing, and the
// fetcher reports the missing key itself. Returns nil for an unknown type.
//
// settings is the engine's mt_engines.settings object — the request options its
// provider accepts, validated on save against the catalog in
// internal/mtengine. Unknown keys are ignored here rather than rejected: the
// save path is where a bad option is caught, and failing every translation
// because a row carries a key this build no longer knows is the worse outcome.
// nil is valid and means "the provider's own defaults".
func CatalogEngine(engineType, apiKey, apiURL string, settings map[string]any) Engine {
	switch engineType {
	case "mock":
		return Mock{}
	case "deepl":
		return DeepL{APIKey: apiKey, BaseURL: apiURL, Options: DeepLOptions{
			Formality:          settingStr(settings, "formality"),
			GlossaryID:         settingStr(settings, "glossary_id"),
			PreserveFormatting: settingBool(settings, "preserve_formatting"),
			SplitSentences:     settingStr(settings, "split_sentences"),
			ModelType:          settingStr(settings, "model_type"),
		}}
	case "google":
		return Google{APIKey: apiKey, BaseURL: apiURL, Options: GoogleOptions{
			Model: settingStr(settings, "model"),
		}}
	case "libretranslate":
		return LibreTranslate{APIURL: apiURL, APIKey: apiKey}
	default:
		return nil
	}
}

// NewFromDB creates an Engine from database configuration.
func NewFromDB(engineType, apiKey, apiURL string, settings map[string]any) (Engine, error) {
	engine := CatalogEngine(engineType, apiKey, apiURL, settings)
	if engine == nil {
		return nil, fmt.Errorf("unknown engine type: %s", engineType)
	}
	// LibreTranslate authenticates per instance, and the mock takes nothing.
	if apiKey == "" {
		switch engineType {
		case "deepl":
			return nil, fmt.Errorf("DeepL requires an API key")
		case "google":
			return nil, fmt.Errorf("Google Translate requires an API key")
		}
	}
	return engine, nil
}

func settingStr(settings map[string]any, key string) string {
	s, _ := settings[key].(string)
	return s
}

// settingBool returns nil when the key is absent, so "not chosen" stays
// distinct from "chosen false" all the way to the provider's request body.
func settingBool(settings map[string]any, key string) *bool {
	b, ok := settings[key].(bool)
	if !ok {
		return nil
	}
	return &b
}
