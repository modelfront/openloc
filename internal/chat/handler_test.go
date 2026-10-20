package chat

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"openloc/internal/ai"
)

func TestUserFacingLLMErrorUsesCompleteSentences(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"authentication", &ai.ProviderError{Provider: ai.ProviderClaude, Kind: ai.ProviderErrAuth}, "Your Claude API key was rejected — reconnect it in AI settings → Providers."},
		{"quota", &ai.ProviderError{Provider: ai.ProviderClaude, Kind: ai.ProviderErrQuota}, "Your Claude account is out of credit."},
		{"rate limit", &ai.ProviderError{Provider: ai.ProviderClaude, Kind: ai.ProviderErrRateLimit}, "Claude is rate-limiting requests — try again in a moment."},
		{"unreachable", &ai.ProviderError{Provider: ai.ProviderClaude, Kind: ai.ProviderErrUnreachable}, "Could not reach Claude — check your network and try again."},
		// The message must name the vendor that actually failed, so a GPT-4o
		// user is not told to reconnect a Claude key.
		{"openai auth", &ai.ProviderError{Provider: ai.ProviderOpenAI, Kind: ai.ProviderErrAuth}, "Your OpenAI API key was rejected — reconnect it in AI settings → Providers."},
		{"openai rate limit", &ai.ProviderError{Provider: ai.ProviderOpenAI, Kind: ai.ProviderErrRateLimit}, "OpenAI is rate-limiting requests — try again in a moment."},
		{"provider fallback", &ai.ProviderError{}, "The AI provider returned an error — try again."},
		{"timeout", context.DeadlineExceeded, "This question took too long to answer — try a narrower one."},
		// The shutdown drain cancels the turn context while the client is still
		// connected — the one cancellation a user can see.
		// Wrapped, because Run's error path wraps the context error.
		{"cancelled by drain", fmt.Errorf("llm call: %w", context.Canceled), "This answer was interrupted — please try again."},
		{"unknown", errors.New("boom"), "Something went wrong answering this — try again."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := userFacingLLMError(tt.err); got != tt.want {
				t.Fatalf("userFacingLLMError() = %q, want %q", got, tt.want)
			}
		})
	}
}
