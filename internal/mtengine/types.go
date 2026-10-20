package mtengine

// The MT engine types this deployment can build (internal/mt/factory.go).
// Named here because three places need the same list — the create/update
// whitelist, the options catalog, and the rule about which types need a key.
const (
	EngineTypeMock           = "mock"
	EngineTypeLibreTranslate = "libretranslate"
	EngineTypeDeepL          = "deepl"
	EngineTypeGoogle         = "google"
)

// displayType is the type as users see it, for error copy.
var displayType = map[string]string{
	EngineTypeMock:           "Mock",
	EngineTypeLibreTranslate: "LibreTranslate",
	EngineTypeDeepL:          "DeepL",
	EngineTypeGoogle:         "Google Translate",
}

// DisplayType names an engine type in the user's vocabulary, falling back to
// the raw type for one this build does not know.
func DisplayType(engineType string) string {
	if s, ok := displayType[engineType]; ok {
		return s
	}
	return engineType
}

// ValidType reports whether engineType is one this deployment can build.
func ValidType(engineType string) bool {
	_, ok := displayType[engineType]
	return ok
}

// NeedsToken reports whether an engine of this type is authenticated by a key
// from AI settings. The built-ins are not: the deployment supplies their
// endpoint and credential, which is exactly why they must never offer a key
// field (LibreTranslate's standing rule).
func NeedsToken(engineType string) bool {
	return engineType == EngineTypeDeepL || engineType == EngineTypeGoogle
}
