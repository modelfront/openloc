package pipeline

import (
	"strings"
	"testing"

	"openloc/internal/ai"
	"openloc/internal/enginehealth"
)

// MT and AI-step failures name the vendor as users see it and give one next
// step. The provider's own error text never reaches the user.
func TestProviderFailureMessages(t *testing.T) {
	const detail = "The service hit an internal error to process the request. Sorry about that."
	cases := []struct {
		name string
		err  ai.ProviderError
		want string
	}{
		{"auth", ai.ProviderError{Provider: ai.ProviderOpenAI, Status: 401, Kind: ai.ProviderErrAuth},
			"OpenAI rejected the API key. Check the key in AI settings → Providers."},
		{"quota", ai.ProviderError{Provider: ai.ProviderOpenAI, Status: 429, Kind: ai.ProviderErrQuota},
			"The OpenAI account has no credit left. Check your plan and billing."},
		{"rate limit", ai.ProviderError{Provider: ai.ProviderClaude, Status: 429, Kind: ai.ProviderErrRateLimit},
			"Claude is limiting requests. Wait a minute and try again."},
		{"unreachable", ai.ProviderError{Provider: ai.ProviderOpenAI, Kind: ai.ProviderErrUnreachable},
			"Could not reach OpenAI. Try again in a few minutes."},
		{"server error", ai.ProviderError{Provider: ai.ProviderOpenAI, Status: 500, Kind: ai.ProviderErrProvider, Detail: detail},
			"OpenAI is having a problem right now. Try again in a few minutes."},
		{"other status", ai.ProviderError{Provider: ai.ProviderOpenAI, Status: 400, Kind: ai.ProviderErrProvider, Detail: detail},
			"OpenAI did not accept the request (error 400). Check the engine settings in AI settings."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mtProviderMessage(&tc.err); got != "Machine translation failed: "+tc.want {
				t.Errorf("mtProviderMessage = %q\nwant %q", got, "Machine translation failed: "+tc.want)
			}
			if got := aiStepProviderMessage(&tc.err); got != "The AI step could not complete: "+tc.want {
				t.Errorf("aiStepProviderMessage = %q\nwant %q", got, "The AI step could not complete: "+tc.want)
			}
			if strings.Contains(mtProviderMessage(&tc.err), "Sorry about that") {
				t.Error("message quotes the provider's own text")
			}
		})
	}
}

// An engine whose key was deleted names the engine and where to add a key.
func TestEngineUnusableNoKeyMessage(t *testing.T) {
	err := &EngineUnusableError{EngineName: "DeepL formal", Reason: enginehealth.NoKey}
	want := `the engine "DeepL formal" has no API key. Choose a key for it in MT → Engines.`
	if got := mtUnusable(err).Error(); got != "Machine translation failed: "+want {
		t.Errorf("MT message = %q", got)
	}
	if !IsPermanent(mtUnusable(err)) || !IsPermanent(aiStepUnusable(&EngineUnusableError{EngineName: "x", Reason: enginehealth.NoKey})) {
		t.Error("a missing key must be Permanent: no retry fixes it")
	}
}
