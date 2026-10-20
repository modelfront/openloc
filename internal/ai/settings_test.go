package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The provider config's Model settings must reach the actual
// request, so these tests assert on the request BODY the provider received —
// the only place that is observable.

// captureBody stands up a provider stub that records the request body and
// answers the batch contract so the call completes.
func captureBody(t *testing.T, claude bool) (*httptest.Server, *map[string]any) {
	t.Helper()
	got := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, segs := decodeRequest(t, raw)
		objects := make([]string, len(segs))
		for i, s := range segs {
			objects[i] = `{"id":"` + s.ID + `","target":"x"}`
		}
		if claude {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]string{{"type": "text", "text": resultsReply(objects...)}},
			})
			return
		}
		writeOpenAIContent(w, resultsReply(objects...))
	}))
	return srv, &got
}

func TestSettings_OpenAIParamsReachTheRequest(t *testing.T) {
	srv, got := captureBody(t, false)
	defer srv.Close()

	tr := NewHTTPTranslator()
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, Model: testOpenAIModel, APIKey: "sk-test", BaseURL: srv.URL,
		Settings: Settings{
			"temperature":           0.4,
			"top_p":                 0.9,
			"seed":                  float64(7),
			"max_completion_tokens": float64(2048),
			"presence_penalty":      0.5,
		},
	}, texts("hello")); err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string]float64{
		"temperature": 0.4, "top_p": 0.9, "seed": 7, "max_completion_tokens": 2048, "presence_penalty": 0.5,
	} {
		if v, ok := (*got)[key].(float64); !ok || v != want {
			t.Errorf("%s: want %v in the request body, got %v", key, want, (*got)[key])
		}
	}
}

// Temperature 0 stays the default when unset — deterministic output is what
// translation wants, and changing it silently would shift every tenant's results.
func TestSettings_OpenAIKeepsZeroTemperatureDefault(t *testing.T) {
	srv, got := captureBody(t, false)
	defer srv.Close()

	tr := NewHTTPTranslator()
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, Model: testOpenAIModel, APIKey: "sk-test", BaseURL: srv.URL,
	}, texts("hello")); err != nil {
		t.Fatal(err)
	}
	if v, ok := (*got)["temperature"].(float64); !ok || v != 0 {
		t.Errorf("want the temperature 0 default, got %v", (*got)["temperature"])
	}
}

// …but a GPT-5 reasoning model is the exception: Microsoft lists `temperature`
// among the parameters "currently unsupported with reasoning models", so the
// long-standing default must NOT be sent — it 400s every request on Azure
// OpenAI + GPT-5.
func TestSettings_OpenAIReasoningModelOmitsTemperature(t *testing.T) {
	srv, got := captureBody(t, false)
	defer srv.Close()

	tr := NewHTTPTranslator()
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, Model: "gpt-5", APIKey: "sk-test", BaseURL: srv.URL,
	}, texts("hello")); err != nil {
		t.Fatal(err)
	}
	if v, present := (*got)["temperature"]; present {
		t.Errorf("gpt-5 must not receive temperature (the provider rejects it), got %v", v)
	}
	for _, banned := range []string{"top_p", "presence_penalty", "frequency_penalty", "max_tokens"} {
		if v, present := (*got)[banned]; present {
			t.Errorf("gpt-5 must not receive %s, got %v", banned, v)
		}
	}
}

// The reasoning knobs that REPLACE temperature have to reach the wire, or the
// settings panel is decorative again.
func TestSettings_OpenAIReasoningParamsReachTheRequest(t *testing.T) {
	srv, got := captureBody(t, false)
	defer srv.Close()

	tr := NewHTTPTranslator()
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, Model: "gpt-5", APIKey: "sk-test", BaseURL: srv.URL,
		Settings: Settings{
			"reasoning_effort":      "low",
			"verbosity":             "low",
			"max_completion_tokens": float64(4096),
		},
	}, texts("hello")); err != nil {
		t.Fatal(err)
	}
	if v, _ := (*got)["reasoning_effort"].(string); v != "low" {
		t.Errorf("reasoning_effort: want low, got %v", (*got)["reasoning_effort"])
	}
	if v, _ := (*got)["verbosity"].(string); v != "low" {
		t.Errorf("verbosity: want low, got %v", (*got)["verbosity"])
	}
	if v, _ := (*got)["max_completion_tokens"].(float64); v != 4096 {
		t.Errorf("max_completion_tokens: want 4096, got %v", (*got)["max_completion_tokens"])
	}
}

// A settings blob can hold keys from an older schema, a hand-edited row, or a
// since-changed catalog. Those must never become request parameters.
func TestSettings_DropsKeysTheModelRejects(t *testing.T) {
	srv, got := captureBody(t, true)
	defer srv.Close()

	tr := NewHTTPTranslator()
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderClaude, Model: testOpus5, APIKey: "sk-ant", BaseURL: srv.URL,
		Settings: Settings{
			// Removed on Opus 5 — sending any of these is a hard 400.
			"temperature": 0.5, "top_p": 0.9, "top_k": float64(40),
			"thinking_budget_tokens": float64(4096),
			// Not a parameter at all — the injection case.
			"stream": true,
		},
	}, texts("hello")); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"temperature", "top_p", "top_k", "thinking_budget_tokens", "stream"} {
		if _, present := (*got)[key]; present {
			t.Errorf("%q must not reach an Opus 5 request (it would 400)", key)
		}
	}
}

func TestSettings_ClaudeEffortAndThinking(t *testing.T) {
	t.Run("effort nests under output_config", func(t *testing.T) {
		srv, got := captureBody(t, true)
		defer srv.Close()
		tr := NewHTTPTranslator()
		if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
			Provider: ProviderClaude, Model: testOpus5, APIKey: "sk-ant", BaseURL: srv.URL,
			Settings: Settings{"effort": "xhigh"},
		}, texts("hello")); err != nil {
			t.Fatal(err)
		}
		oc, ok := (*got)["output_config"].(map[string]any)
		if !ok || oc["effort"] != "xhigh" {
			t.Errorf("want output_config.effort=xhigh, got %v", (*got)["output_config"])
		}
		if _, present := (*got)["effort"]; present {
			t.Error("effort must not be sent as a top-level field")
		}
	})

	t.Run("thinking off becomes an object", func(t *testing.T) {
		srv, got := captureBody(t, true)
		defer srv.Close()
		tr := NewHTTPTranslator()
		if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
			Provider: ProviderClaude, Model: testSonnet5, APIKey: "sk-ant", BaseURL: srv.URL,
			Settings: Settings{"thinking": ThinkingDisabled},
		}, texts("hello")); err != nil {
			t.Fatal(err)
		}
		th, ok := (*got)["thinking"].(map[string]any)
		if !ok || th["type"] != ThinkingDisabled {
			t.Errorf("want thinking={type:disabled}, got %v", (*got)["thinking"])
		}
	})

	// Defensive: save-time validation blocks this pair, so only a legacy or
	// hand-edited row reaches here. Honour the explicit "off" rather than
	// turning thinking back on and multiplying the tenant's spend.
	t.Run("disabled thinking wins over an incompatible effort", func(t *testing.T) {
		srv, got := captureBody(t, true)
		defer srv.Close()
		tr := NewHTTPTranslator()
		if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
			Provider: ProviderClaude, Model: testOpus5, APIKey: "sk-ant", BaseURL: srv.URL,
			Settings: Settings{"thinking": ThinkingDisabled, "effort": "max"},
		}, texts("hello")); err != nil {
			t.Fatal(err)
		}
		if th, ok := (*got)["thinking"].(map[string]any); !ok || th["type"] != ThinkingDisabled {
			t.Errorf("thinking should stay off, got %v", (*got)["thinking"])
		}
		// output_config itself may be present — it also carries the output
		// schema — so the assertion is on the effort key, not on the object.
		if cfg, ok := (*got)["output_config"].(map[string]any); ok {
			if _, present := cfg["effort"]; present {
				t.Error("the incompatible effort must be dropped, not sent")
			}
		}
	})
}

// Haiku 4.5 predates adaptive thinking: its budget maps to the older
// thinking={type:enabled,budget_tokens} shape.
func TestSettings_ClaudeLegacyThinkingBudget(t *testing.T) {
	srv, got := captureBody(t, true)
	defer srv.Close()
	tr := NewHTTPTranslator()
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderClaude, Model: testHaiku45, APIKey: "sk-ant", BaseURL: srv.URL,
		Settings: Settings{"thinking_budget_tokens": float64(2048), "max_tokens": float64(16000), "temperature": 0.3},
	}, texts("hello")); err != nil {
		t.Fatal(err)
	}
	th, ok := (*got)["thinking"].(map[string]any)
	if !ok || th["type"] != "enabled" {
		t.Fatalf("want thinking={type:enabled,...}, got %v", (*got)["thinking"])
	}
	if b, ok := th["budget_tokens"].(float64); !ok || b != 2048 {
		t.Errorf("want budget_tokens 2048, got %v", th["budget_tokens"])
	}
	if v, ok := (*got)["temperature"].(float64); !ok || v != 0.3 {
		t.Errorf("Haiku accepts temperature; want 0.3, got %v", (*got)["temperature"])
	}
}

// A budget that doesn't leave room for the answer is a 400 — drop it rather
// than send it (validation blocks it on save; this covers older rows).
func TestSettings_DropsBudgetThatExceedsMaxTokens(t *testing.T) {
	srv, got := captureBody(t, true)
	defer srv.Close()
	tr := NewHTTPTranslator()
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderClaude, Model: testHaiku45, APIKey: "sk-ant", BaseURL: srv.URL,
		Settings: Settings{"thinking_budget_tokens": float64(60000), "max_tokens": float64(4096)},
	}, texts("hello")); err != nil {
		t.Fatal(err)
	}
	if _, present := (*got)["thinking"]; present {
		t.Errorf("a budget ≥ max_tokens must be dropped, got %v", (*got)["thinking"])
	}
}

// An explicit Max output tokens wins over the batch-derived value.
func TestSettings_MaxTokensOverridesTheBatchDefault(t *testing.T) {
	srv, got := captureBody(t, true)
	defer srv.Close()
	tr := NewHTTPTranslator()
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderClaude, Model: testSonnet5, APIKey: "sk-ant", BaseURL: srv.URL,
		Settings: Settings{"max_tokens": float64(1234)},
	}, texts("hello")); err != nil {
		t.Fatal(err)
	}
	if v, ok := (*got)["max_tokens"].(float64); !ok || v != 1234 {
		t.Errorf("want the pinned max_tokens 1234, got %v", (*got)["max_tokens"])
	}
}

// With no pinned value, the batch-derived cap applies — and it must account for
// thinking, which shares the budget with the answer.
func TestSettings_ThinkingWidensTheBatchBudget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings Settings
		want     int
	}{
		{"thinking on by default", nil, batchMaxTokens(1, true)},
		{"thinking off", Settings{"thinking": ThinkingDisabled}, batchMaxTokens(1, false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := captureBody(t, true)
			defer srv.Close()
			tr := NewHTTPTranslator()
			if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
				Provider: ProviderClaude, Model: testSonnet5, APIKey: "sk-ant", BaseURL: srv.URL,
				Settings: tc.settings,
			}, texts("hello")); err != nil {
				t.Fatal(err)
			}
			if v, ok := (*got)["max_tokens"].(float64); !ok || int(v) != tc.want {
				t.Errorf("want max_tokens %d, got %v", tc.want, (*got)["max_tokens"])
			}
		})
	}
}

// The QE+APE path shares the transport, so settings must apply there too.
func TestSettings_ReachThePredictRequest(t *testing.T) {
	srv, got := captureBody(t, true)
	defer srv.Close()
	tr := NewHTTPTranslator()
	if _, err := tr.PredictBatch(context.Background(), PredictConfig{
		Provider: ProviderClaude, Model: testOpus5, APIKey: "sk-ant", BaseURL: srv.URL,
		SourceLang: "en", TargetLang: "de", Mode: ModeQEAPE,
		Settings: Settings{"effort": "low"},
	}, []PredictInput{{ID: "1", Source: "Hello", Translation: "Hallo"}}); err != nil {
		t.Fatal(err)
	}
	oc, ok := (*got)["output_config"].(map[string]any)
	if !ok || oc["effort"] != "low" {
		t.Errorf("want output_config.effort=low on the predict request, got %v", (*got)["output_config"])
	}
}

func TestParseSettings(t *testing.T) {
	if s := ParseSettings(t.Context(), nil); s != nil {
		t.Errorf("nil column → nil settings, got %v", s)
	}
	if s := ParseSettings(t.Context(), []byte(`{}`)); len(s) != 0 {
		t.Errorf("empty object → empty settings, got %v", s)
	}
	if s := ParseSettings(t.Context(), []byte(`{"effort":"high"}`)); s["effort"] != "high" {
		t.Errorf("want effort=high, got %v", s)
	}
	// A corrupt blob must not stop a file from being translated.
	if s := ParseSettings(t.Context(), []byte(`{not json`)); s != nil {
		t.Errorf("unparseable settings → nil, got %v", s)
	}
}
