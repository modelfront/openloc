package mt

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"openloc/internal/logging"
)

func TestLibreTranslate_Translate_Batch(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var raw map[string]any
		json.NewDecoder(r.Body).Decode(&raw)

		// Batch mode: q is an array
		qArr, ok := raw["q"].([]any)
		if !ok {
			t.Fatalf("expected q to be array, got %T", raw["q"])
		}

		translated := make([]string, len(qArr))
		for i, q := range qArr {
			translated[i] = "translated: " + q.(string)
		}
		json.NewEncoder(w).Encode(ltBatchResponse{TranslatedText: translated})
	}))
	defer srv.Close()

	lt := LibreTranslate{APIURL: srv.URL}
	results, err := lt.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "de",
		Texts:      []string{"Hello", "World"},
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}

	// Should have made 1 batch request
	if requestCount != 1 {
		t.Fatalf("expected 1 batch HTTP request, got %d", requestCount)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Text != "translated: Hello" {
		t.Errorf("expected 'translated: Hello', got %s", results[0].Text)
	}
	if results[1].Text != "translated: World" {
		t.Errorf("expected 'translated: World', got %s", results[1].Text)
	}
}

func TestLibreTranslate_FallbackToOneByOne(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		var raw map[string]any
		json.NewDecoder(r.Body).Decode(&raw)

		// If q is array (batch), return error to trigger fallback
		if _, ok := raw["q"].([]any); ok {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("batch not supported"))
			return
		}

		// Single mode: q is string
		q, _ := raw["q"].(string)
		json.NewEncoder(w).Encode(ltSingleResponse{TranslatedText: "ok: " + q})
	}))
	defer srv.Close()

	lt := LibreTranslate{APIURL: srv.URL}
	results, err := lt.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "de",
		Texts:      []string{"Hello", "World"},
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}

	// 1 batch (failed) + 2 one-by-one
	if requestCount != 3 {
		t.Fatalf("expected 3 requests (1 batch fail + 2 single), got %d", requestCount)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Text != "ok: Hello" {
		t.Errorf("expected 'ok: Hello', got %s", results[0].Text)
	}
}

func TestLibreTranslate_FallbackLogsBatchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		json.NewDecoder(r.Body).Decode(&raw)
		if _, ok := raw["q"].([]any); ok {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("array q not supported"))
			return
		}
		q, _ := raw["q"].(string)
		json.NewEncoder(w).Encode(ltSingleResponse{TranslatedText: "ok: " + q})
	}))
	defer srv.Close()

	var buf bytes.Buffer
	prev := logging.L()
	logging.Init(logging.Options{ECS: true, Writer: &buf})
	t.Cleanup(func() { logging.SetLogger(prev) })

	lt := LibreTranslate{APIURL: srv.URL}
	if _, err := lt.Translate(context.Background(), TranslateRequest{
		SourceLang: "en", TargetLang: "de", Texts: []string{"Hello", "World"},
	}); err != nil {
		t.Fatalf("expected success via fallback, got err: %v", err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "batch") || !strings.Contains(logs, "libretranslate returned 400") || strings.Contains(logs, "array q not supported") {
		t.Errorf("expected a batch log with the upstream status and without the response body, got: %q", logs)
	}
}

func TestLibreTranslate_RespectsContextDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	lt := LibreTranslate{APIURL: srv.URL}
	start := time.Now()
	_, err := lt.Translate(ctx, TranslateRequest{
		SourceLang: "en", TargetLang: "de", Texts: []string{"Hello"},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from context deadline")
	}
	if elapsed > 1*time.Second {
		t.Errorf("Translate took %s, expected to abort well within 1s", elapsed)
	}
}

func TestLibreTranslate_WithAPIKey(t *testing.T) {
	var receivedKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		json.NewDecoder(r.Body).Decode(&raw)
		if k, ok := raw["api_key"].(string); ok {
			receivedKey = k
		}
		json.NewEncoder(w).Encode(ltBatchResponse{TranslatedText: []string{"ok"}})
	}))
	defer srv.Close()

	lt := LibreTranslate{APIURL: srv.URL, APIKey: "my-key"}
	_, err := lt.Translate(context.Background(), TranslateRequest{
		SourceLang: "en", TargetLang: "de", Texts: []string{"Hello"},
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}

	if receivedKey != "my-key" {
		t.Errorf("expected api_key 'my-key', got %s", receivedKey)
	}
}

func TestLibreTranslate_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	lt := LibreTranslate{APIURL: srv.URL}
	_, err := lt.Translate(context.Background(), TranslateRequest{
		SourceLang: "en", TargetLang: "de", Texts: []string{"Hello"},
	})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestLibreTranslate_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	lt := LibreTranslate{APIURL: srv.URL}
	_, err := lt.Translate(context.Background(), TranslateRequest{
		SourceLang: "en", TargetLang: "de", Texts: []string{"Hello"},
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestLibreTranslate_DefaultURL(t *testing.T) {
	if got := (LibreTranslate{}).resolveBaseURL(); got != "http://localhost:5100" {
		t.Errorf("default base URL = %q, want http://localhost:5100", got)
	}
	if got := (LibreTranslate{APIURL: "http://custom:8080"}).resolveBaseURL(); got != "http://custom:8080" {
		t.Errorf("custom base URL = %q, want http://custom:8080", got)
	}
}

func TestLibreTranslate_EmptyTexts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not call server for empty texts")
	}))
	defer srv.Close()

	lt := LibreTranslate{APIURL: srv.URL}
	results, err := lt.Translate(context.Background(), TranslateRequest{
		SourceLang: "en", TargetLang: "de", Texts: []string{},
	})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}
