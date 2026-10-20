package ai

import "testing"

// The classifier is the only thing between a raw provider id and a request we
// build for it. Two ways it can be wrong, both covered here:
//
//   - Listing a model that isn't a text generator (OpenAI's catalog carries
//     speech, image and embedding models under the same gpt- prefix). The user
//     picks it and every segment fails.
//   - Picking the wrong parameter family. Sending `temperature` to Opus 5 or
//     `max_tokens` to a GPT-5 model is a hard 400, not a soft degradation.
func TestClassifyRemoteOpenAI(t *testing.T) {
	tests := []struct {
		id     string
		want   paramFamily
		listed bool
	}{
		{"gpt-5", familyOpenAIReasoning, true},
		{"gpt-5-mini", familyOpenAIReasoning, true},
		{"gpt-4o", familyOpenAIChat, true},
		{"gpt-4o-mini", familyOpenAIChat, true},
		{"gpt-4-turbo", familyOpenAIChat, true},
		{"o3", familyOpenAIReasoning, true},
		{"o4-mini", familyOpenAIReasoning, true},
		{"chatgpt-4o-latest", familyOpenAIChat, true},
		{"gpt-4.1-mini", familyOpenAIChat, true},
		// GPT-5 and up reason; a generation that doesn't exist yet classifies
		// correctly without a deploy, which is the point of fetching the list.
		{"gpt-5.6", familyOpenAIReasoning, true},
		{"gpt-6-mini", familyOpenAIReasoning, true},
		// Pro models serve on the Responses API, which the chat uses.
		{"gpt-5.5-pro", familyOpenAIReasoning, true},
		{"o1-preview", familyOpenAIReasoning, true},
		// Specialized models sharing an allowed prefix.
		{"gpt-4o-audio-preview", familyNone, false},
		{"gpt-4o-realtime-preview", familyNone, false},
		{"gpt-live-1", familyNone, false},
		{"gpt-4o-transcribe", familyNone, false},
		{"gpt-4o-mini-tts", familyNone, false},
		{"gpt-image-1", familyNone, false},
		{"gpt-4o-search-preview", familyNone, false},
		{"gpt-3.5-turbo-instruct", familyNone, false},
		// Other product lines entirely.
		{"text-embedding-3-large", familyNone, false},
		{"whisper-1", familyNone, false},
		{"tts-1-hd", familyNone, false},
		{"dall-e-3", familyNone, false},
		{"sora-2", familyNone, false},
		{"omni-moderation-latest", familyNone, false},
		// A fine-tune is selectable, and takes its base model's parameters.
		{"ft:gpt-4o:acme::abc123", familyOpenAIChat, true},
		{"ft:gpt-5-mini:acme::xyz789", familyOpenAIReasoning, true},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			m, ok := classifyRemote(ProviderOpenAI, RemoteModel{ID: tc.id})
			if ok != tc.listed {
				t.Fatalf("classifyRemote(%q) listed = %v, want %v", tc.id, ok, tc.listed)
			}
			if ok && m.family != tc.want {
				t.Errorf("classifyRemote(%q) family = %q, want %q", tc.id, m.family, tc.want)
			}
			if got := familyFromID(ProviderOpenAI, tc.id); tc.listed && got != tc.want {
				t.Errorf("familyFromID(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestClassifyRemoteClaude(t *testing.T) {
	tests := []struct {
		id     string
		want   paramFamily
		listed bool
	}{
		{"claude-opus-5", familyClaudeEffort, true},
		{"claude-sonnet-5", familyClaudeEffort, true},
		{"claude-opus-4-8", familyClaudeEffort, true},
		{"claude-fable-5", familyClaudeEffortAlwaysThinking, true},
		// Pre-adaptive generations take the sampling knobs + a fixed thinking
		// budget; `effort` errors on them.
		{"claude-haiku-4-5-20251001", familyClaudeBudget, true},
		{"claude-3-5-sonnet-20241022", familyClaudeBudget, true},
		{"claude-sonnet-4-0", familyClaudeBudget, true},
		{"claude-sonnet-4-5", familyClaudeBudget, true},
		{"claude-opus-4-1-20250805", familyClaudeBudget, true},
		// Above the adaptive-thinking boundary, including a generation that
		// doesn't exist yet — the point of fetching is that it works without a
		// deploy.
		{"claude-opus-6", familyClaudeEffort, true},
		{"claude-sonnet-5-2", familyClaudeEffort, true},
		{"gpt-4o", familyNone, false},
		{"", familyNone, false},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			m, ok := classifyRemote(ProviderClaude, RemoteModel{ID: tc.id})
			if ok != tc.listed {
				t.Fatalf("classifyRemote(%q) listed = %v, want %v", tc.id, ok, tc.listed)
			}
			if ok && m.family != tc.want {
				t.Errorf("classifyRemote(%q) family = %q, want %q", tc.id, m.family, tc.want)
			}
		})
	}
}

// The params a family resolves to are what the settings form renders and what
// save-time validation accepts, so a family that maps to nothing is a model
// offered with no working knobs.
func TestClassifiedModelsResolveParams(t *testing.T) {
	for _, tc := range []struct{ provider, id string }{
		{ProviderOpenAI, "gpt-5"},
		{ProviderOpenAI, "gpt-4o"},
		{ProviderClaude, "claude-opus-5"},
		{ProviderClaude, "claude-fable-5"},
		{ProviderClaude, "claude-haiku-4-5-20251001"},
	} {
		if got := ParamsFor(tc.provider, tc.id); len(got) == 0 {
			t.Errorf("ParamsFor(%s, %s) is empty — the model would offer no settings", tc.provider, tc.id)
		}
	}
}

// A dated snapshot and the alias that addresses the same model both appear in a
// provider response. Listing both fills the picker with one model family.
func TestDropDatedDuplicates(t *testing.T) {
	in := []Model{
		{ID: "gpt-4o"},
		{ID: "gpt-4o-2024-08-06"},
		{ID: "gpt-4o-2024-05-13"},
		{ID: "claude-haiku-4-5-20251001"}, // no alias present: kept
		{ID: "gpt-5"},
	}
	got := dropDatedDuplicates(append([]Model{}, in...))
	want := []string{"gpt-4o", "claude-haiku-4-5-20251001", "gpt-5"}
	if len(got) != len(want) {
		t.Fatalf("got %d models %v, want %v", len(got), ids(got), want)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("model %d = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestProviderForModelID(t *testing.T) {
	for id, want := range map[string]string{
		"gpt-4o":                    ProviderOpenAI,
		"o3":                        ProviderOpenAI,
		"ft:gpt-4o:acme::abc":       ProviderOpenAI,
		"claude-opus-5":             ProviderClaude,
		"claude-haiku-4-5-20251001": ProviderClaude,
		"tts-1":                     "",
		"":                          "",
	} {
		if got := ProviderForModelID(id); got != want {
			t.Errorf("ProviderForModelID(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestPrettyLabel(t *testing.T) {
	for id, want := range map[string]string{
		"gpt-4o":              "GPT-4o",
		"gpt-4o-mini":         "GPT-4o mini",
		"gpt-5":               "GPT-5",
		"gpt-4-turbo":         "GPT-4 Turbo",
		"o3":                  "O3",
		"ft:gpt-4o:acme::abc": "Fine-tune: GPT-4o",
	} {
		if got := prettyLabel(id); got != want {
			t.Errorf("prettyLabel(%q) = %q, want %q", id, got, want)
		}
	}
}

// Anthropic publishes a display name; it beats anything derived from the id.
func TestLabelPrefersDisplayName(t *testing.T) {
	m, ok := classifyRemote(ProviderClaude, RemoteModel{ID: "claude-opus-5", DisplayName: "Claude Opus 5"})
	if !ok {
		t.Fatal("claude-opus-5 must be listed")
	}
	if m.Label != "Claude Opus 5" {
		t.Errorf("label = %q, want the provider's display_name", m.Label)
	}
}

func ids(models []Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.ID
	}
	return out
}
