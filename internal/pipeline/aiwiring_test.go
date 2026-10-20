package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"openloc/internal/ai"
	"openloc/internal/llmdebug"
)

// 32 bytes hex-encoded — a valid AES key for internal/secrets.
const testSecretsKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // gitleaks:allow

type recorderSpy struct{ n int }

func (r *recorderSpy) Record(context.Context, llmdebug.Entry) { r.n++ }

func newBareService(t *testing.T) *Service {
	t.Helper()
	return NewService(nil, &passthroughTxRunner{}, nil)
}

// openAIStub answers every chat completion with the German "Hallo".
func openAIStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": `{"target": "Hallo"}`}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// translateHello sends one segment through the service's wired AI translator.
func translateHello(t *testing.T, svc *Service, baseURL string) []ai.TranslateResult {
	t.Helper()
	if svc.aiTranslator == nil {
		t.Fatal("AI translator not wired — AI-as-MT would be unavailable")
	}
	got, err := svc.aiTranslator.TranslateBatch(context.Background(), ai.TranslateConfig{
		Provider: ai.ProviderOpenAI, APIKey: "sk-test", BaseURL: baseURL,
		PromptText: "Translate from <source_lang> to <target_lang>.", SourceLang: "en", TargetLang: "de",
	}, []ai.TranslateInput{{ID: "0", Text: "hello"}})
	if err != nil {
		t.Fatalf("TranslateBatch: %v", err)
	}
	return got
}

// The HTTP router and the worker both call ConfigureAI, so the provider calls
// it wires must reach the recorder it was given.
func TestConfigureAI_WiresRecorderTranslatorAndDecryptor(t *testing.T) {
	svc := newBareService(t)
	rec := &recorderSpy{}

	dec, err := svc.ConfigureAI(testSecretsKey, rec)
	if err != nil {
		t.Fatalf("ConfigureAI: %v", err)
	}
	if dec == nil {
		t.Error("decryptor must be returned so the caller can share it")
	}
	if svc.aiDecryptor == nil {
		t.Error("decryptor not set on the service — LLM rows cannot read their keys without it")
	}
	if svc.aiPredictor == nil {
		t.Error("predictor not set — LLM QE+APE would be unavailable")
	}

	translateHello(t, svc, openAIStub(t).URL)
	if rec.n != 1 {
		t.Errorf("recorder captured %d provider calls, want 1", rec.n)
	}
}

func TestConfigureAI_MissingKeyLeavesAIUnwired(t *testing.T) {
	svc := newBareService(t)

	// An EMPTY key is what secrets.NewEncryptor rejects (it SHA-256s anything
	// non-empty into a valid AES key), and empty is the real-world case: the env
	// var unset in a deployment.
	if _, err := svc.ConfigureAI("", &recorderSpy{}); err == nil {
		t.Fatal("expected an error for a missing secrets key")
	}
	if svc.aiTranslator != nil || svc.aiPredictor != nil {
		t.Error("a bad key must leave the AI provider unwired")
	}
}

// Production runs with no recorder; provider calls must still work.
func TestConfigureAI_NilRecorderStillTranslates(t *testing.T) {
	svc := newBareService(t)
	if _, err := svc.ConfigureAI(testSecretsKey, nil); err != nil {
		t.Fatalf("ConfigureAI: %v", err)
	}
	got := translateHello(t, svc, openAIStub(t).URL)
	if len(got) != 1 || got[0].Text != "Hallo" {
		t.Errorf("translation = %+v, want [Hallo]", got)
	}
}
