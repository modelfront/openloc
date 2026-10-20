// Package aiusage records what provider calls COST in tokens.
//
// Deliberately separate from internal/llmdebug, which captures the same calls
// for forensics. That store drops on overflow, truncates bodies and carries no
// tenant — correct for debugging, useless for accounting, where a number that
// is quietly 4% low is worse than no number.
//
// Volume (words, segments, characters) and tokens answer different questions: a
// thousand words may be one call or thirty, depending on batching and retries.
// So tokens live here, per CALL, rather than as more columns on
// translation_history.
package aiusage

import "context"

// Usage is what one provider call reported. Providers name these differently
// (OpenAI prompt/completion, Anthropic input/output); the call sites normalize.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

// Empty reports a call that told us nothing — a provider that omits usage, or a
// failed request. Recording zeros would silently understate spend, so callers
// skip these rather than writing a row of noise.
func (u Usage) Empty() bool { return u.InputTokens == 0 && u.OutputTokens == 0 }

// Meta is the attribution a call inherits from its context: who to bill and
// what the call was for. Set by the pipeline before it reaches the AI client,
// the same shape llmdebug already uses for its own metadata.
type Meta struct {
	TenantID     int64
	AIEngineID   int64
	AIEngineName string
	// Purpose mirrors ai_engines.purpose — "mt" or "qe_ape" — so AI-MT spend
	// charts separately from post-editing spend.
	Purpose   string
	ProjectID int64
	JobID     int64
}

type metaKey struct{}

// WithMeta attaches attribution for every provider call made under ctx.
func WithMeta(ctx context.Context, m Meta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

// MetaFromContext returns the attribution, or a zero Meta when nothing set it.
func MetaFromContext(ctx context.Context) Meta {
	m, _ := ctx.Value(metaKey{}).(Meta)
	return m
}

// Recorder persists one call's usage. Implementations must not block the
// caller and must not return an error that could fail a translation: accounting
// is never a reason to lose work.
type Recorder interface {
	Record(ctx context.Context, provider, model string, u Usage)
}
