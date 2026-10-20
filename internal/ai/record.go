package ai

import (
	"context"
	"time"

	"openloc/internal/llmdebug"
)

// record forwards one completed provider call to the dev debug recorder
// (internal/llmdebug). Headers are deliberately NOT passed — the API key
// lives only in headers (Authorization / x-api-key), so it can never enter
// the buffer. No-op when no recorder is wired (production).
func (t *HTTPTranslator) record(ctx context.Context, cc callConfig, url string,
	reqBody, respBody []byte, status int, callErr error, d time.Duration, system, user string) {
	if t.rec == nil {
		return
	}
	e := llmdebug.Entry{
		Kind:         cc.kind,
		Provider:     cc.provider,
		Model:        orDefault(cc.model, defaultModelFor(cc.provider)),
		URL:          url,
		Status:       status,
		DurationMs:   d.Milliseconds(),
		RequestBody:  string(reqBody),
		ResponseBody: string(respBody),
		System:       system,
		User:         user,
		Segments:     cc.segments,
		Retry:        cc.retry,
	}
	if callErr != nil {
		// ProviderError text carries the provider's own message, never the key.
		e.Error = callErr.Error()
	}
	t.rec.Record(ctx, e)
}

// defaultModelFor returns the effective default model so the debug viewer
// shows what was actually sent when the preset left the model empty.
func defaultModelFor(provider string) string {
	switch provider {
	case ProviderOpenAI:
		return defaultOpenAIModel
	case ProviderClaude:
		return defaultClaudeModel
	default:
		return ""
	}
}

// PreviewModel resolves a provider-only preview to the runtime default model.
func PreviewModel(provider, model string) string {
	if model != "" {
		return model
	}
	return defaultModelFor(provider)
}
