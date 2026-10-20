package mt

import "context"

// TranslateRequest represents a batch of texts to translate.
type TranslateRequest struct {
	SourceLang string
	TargetLang string
	Texts      []string
	// Rows identifies the segments behind Texts, index-aligned (Rows[i] is
	// Texts[i]). Optional and ignored by classic engines: only the AI engine
	// uses it, to render the tenant's segment prompt and fetch that
	// segment's context. Nil when the caller has no segment identity to give.
	Rows []SegmentRef
	// Tagged marks Texts as carrying inline tag chips, so an AI engine can add
	// the tag contract to its prompt. Set by LLMEngine.TranslateTagged on its
	// way to the injected Fn; classic engines signal tag mode through their own
	// API instead (DeepL uses tag_handling=xml) and ignore this.
	Tagged bool
}

// SegmentRef identifies one segment of a TranslateRequest so an AI engine can
// look up its context (comments, TM matches, notes) without this package
// depending on the pipeline or the database.
type SegmentRef struct {
	TranslationID int64
	SegmentID     int64
	// PlainSource is the segment's source without inline tags. A TM lookup
	// matches on it, since the request's text may carry tags.
	PlainSource string
}

// TranslateResult holds the translation for a single text.
type TranslateResult struct {
	Text string
}

// Engine translates text from one language to another.
type Engine interface {
	Name() string
	Translate(ctx context.Context, req TranslateRequest) ([]TranslateResult, error)
}

// TagPreservingEngine is implemented by engines that translate inline XML
// tags natively. Our XLIFF rich text (`<g>`, `<x>`, `<bpt>`,
// `<ept>`, `<ph>`) is valid XML, so an engine with XML/HTML tag handling can
// reposition the tags around the translated content and return tagged output
// — no character/word projection needed.
//
// The pipeline detects this capability via type assertion: engines that
// implement it get the native path for tagged segments; everything else falls
// back to word-index projection (pipeline.TransferTags, Tier 1). Implementing
// the interface is the capability signal — there is no separate boolean.
type TagPreservingEngine interface {
	Engine
	// TranslateTagged translates texts that may contain inline XML tags,
	// returning the translation with those tags repositioned around the
	// corresponding target content. Texts without tags translate normally.
	// Same batching, ordering, and length contract as Translate (one result
	// per input, in order). Returns an error (not a silent plain-text
	// fallback) if the engine's tag mode fails — the caller decides whether
	// to degrade to Tier 1.
	TranslateTagged(ctx context.Context, req TranslateRequest) ([]TranslateResult, error)
}

// LanguageLister is implemented by engines that publish their supported
// languages over their own API, the same optional-capability pattern as
// TagPreservingEngine. An engine without it resolves locales against the
// static table in languages.go.
//
// Implementations do the bare fetch; caching, staleness and failure fallback
// are the caller's job (SupportedLanguages).
type LanguageLister interface {
	Engine
	// LanguageEndpoint identifies where the list comes from, and is the cache
	// key. A list describes an endpoint rather than an account, so two engine
	// rows reaching the same endpoint share one fetched list — and a tenant
	// pointing at its own gateway never reads the public vendor's.
	LanguageEndpoint() string
	// FetchLanguages reads the live catalog. Called on a cache miss or a stale
	// entry, never per read.
	FetchLanguages(ctx context.Context) (Catalog, error)
}

// The cache reaches this interface by type assertion, so an engine that stops
// satisfying it goes quiet rather than failing to build. These pin the three.
var (
	_ LanguageLister = DeepL{}
	_ LanguageLister = Google{}
	_ LanguageLister = LibreTranslate{}
)
