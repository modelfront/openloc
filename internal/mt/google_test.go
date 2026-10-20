package mt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGoogleTranslate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/language/translate/v2") {
			t.Errorf("expected /language/translate/v2, got %s", r.URL.Path)
		}
		if key := r.URL.Query().Get("key"); key != "test-key" {
			t.Errorf("expected API key in query, got %q", key)
		}

		var req googleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if req.Source != "en" {
			t.Errorf("source = %q, want en", req.Source)
		}
		if req.Target != "es" {
			t.Errorf("target = %q, want es", req.Target)
		}
		if req.Format != "text" {
			t.Errorf("format = %q, want text", req.Format)
		}

		translations := make([]struct {
			TranslatedText string `json:"translatedText"`
		}, len(req.Q))
		for i, text := range req.Q {
			translations[i].TranslatedText = "[ES] " + text
		}

		resp := googleResponse{}
		resp.Data.Translations = translations

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	engine := Google{APIKey: "test-key", BaseURL: server.URL}
	results, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "es",
		Texts:      []string{"Hello", "World"},
	})
	if err != nil {
		t.Fatalf("translate: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Text != "[ES] Hello" {
		t.Errorf("results[0] = %q, want %q", results[0].Text, "[ES] Hello")
	}
	if results[1].Text != "[ES] World" {
		t.Errorf("results[1] = %q, want %q", results[1].Text, "[ES] World")
	}
}

func TestGoogleEmptyInput(t *testing.T) {
	engine := Google{APIKey: "test-key"}
	results, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "es",
		Texts:      nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty input, got %v", results)
	}
}

func TestGoogleNoAPIKey(t *testing.T) {
	engine := Google{}
	_, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "es",
		Texts:      []string{"Hello"},
	})
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
}

func TestGoogleError403(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    403,
				"message": "The request is missing a valid API key.",
			},
		})
	}))
	defer server.Close()

	engine := Google{APIKey: "bad-key", BaseURL: server.URL}
	_, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "es",
		Texts:      []string{"Hello"},
	})
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !strings.Contains(err.Error(), "The request is missing a valid API key") {
		t.Errorf("expected error message about API key, got: %s", err.Error())
	}
}

func TestGoogleError429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer server.Close()

	engine := Google{APIKey: "key", BaseURL: server.URL}
	_, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "es",
		Texts:      []string{"Hello"},
	})
	if err == nil {
		t.Fatal("expected error for 429")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("expected rate limit error, got: %s", err.Error())
	}
}

func TestGoogleErrorWithMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    400,
				"message": "Invalid Value",
			},
		})
	}))
	defer server.Close()

	engine := Google{APIKey: "key", BaseURL: server.URL}
	_, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "xx",
		Texts:      []string{"Hello"},
	})
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if !strings.Contains(err.Error(), "Invalid Value") {
		t.Errorf("expected error message, got: %s", err.Error())
	}
}

func TestGoogleResolveBaseURL(t *testing.T) {
	defaultEngine := Google{APIKey: "key"}
	if got := defaultEngine.resolveBaseURL(); got != "https://translation.googleapis.com" {
		t.Errorf("default URL = %q", got)
	}

	custom := Google{APIKey: "key", BaseURL: "http://custom:8080"}
	if got := custom.resolveBaseURL(); got != "http://custom:8080" {
		t.Errorf("custom URL = %q", got)
	}
}
