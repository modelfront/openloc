package mt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeepLTranslate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v2/translate" {
			t.Errorf("expected /v2/translate, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "DeepL-Auth-Key test-key" {
			t.Errorf("expected auth header, got %q", auth)
		}

		var req deeplRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		// Verify language code conversion
		if req.SourceLang != "EN" {
			t.Errorf("source_lang = %q, want EN", req.SourceLang)
		}
		if req.TargetLang != "DE" {
			t.Errorf("target_lang = %q, want DE", req.TargetLang)
		}

		resp := deeplResponse{
			Translations: make([]struct {
				Text string `json:"text"`
			}, len(req.Text)),
		}
		for i, text := range req.Text {
			resp.Translations[i].Text = "[DE] " + text
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	engine := DeepL{APIKey: "test-key", BaseURL: server.URL}
	results, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "de",
		Texts:      []string{"Hello", "World"},
	})
	if err != nil {
		t.Fatalf("translate: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Text != "[DE] Hello" {
		t.Errorf("results[0] = %q, want %q", results[0].Text, "[DE] Hello")
	}
	if results[1].Text != "[DE] World" {
		t.Errorf("results[1] = %q, want %q", results[1].Text, "[DE] World")
	}
}

// TestDeepLTranslateTagged — DeepL implements
// TagPreservingEngine; TranslateTagged must send tag_handling=xml with
// outline_detection=false and return the engine's tag-repositioned output
// verbatim. A plain Translate call must NOT set tag_handling.
func TestDeepLTranslateTagged(t *testing.T) {
	// Compile-time proof DeepL satisfies the capability interface.
	var _ TagPreservingEngine = DeepL{}

	var gotReq deeplRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotReq)
		// Echo a plausibly tag-repositioned translation for each input.
		resp := deeplResponse{Translations: make([]struct {
			Text string `json:"text"`
		}, len(gotReq.Text))}
		for i := range gotReq.Text {
			resp.Translations[i].Text = `Este texto está en <g id="1" ctype="bold">negrita</g>`
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	engine := DeepL{APIKey: "key", BaseURL: server.URL}
	results, err := engine.TranslateTagged(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "es",
		Texts:      []string{`This text is in <g id="1" ctype="bold">bold</g>`},
	})
	if err != nil {
		t.Fatalf("TranslateTagged: %v", err)
	}

	// Request shape: xml mode + outline detection disabled.
	if gotReq.TagHandling != "xml" {
		t.Errorf("tag_handling = %q, want \"xml\"", gotReq.TagHandling)
	}
	if gotReq.OutlineDetection == nil || *gotReq.OutlineDetection != false {
		t.Errorf("outline_detection = %v, want pointer to false", gotReq.OutlineDetection)
	}
	// Result is the engine's tagged output, passed through untouched.
	want := `Este texto está en <g id="1" ctype="bold">negrita</g>`
	if len(results) != 1 || results[0].Text != want {
		t.Errorf("result = %v, want [%q]", results, want)
	}
}

func TestDeepLTranslate_NoTagHandlingByDefault(t *testing.T) {
	var gotReq deeplRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotReq)
		resp := deeplResponse{Translations: []struct {
			Text string `json:"text"`
		}{{Text: "Hola"}}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	engine := DeepL{APIKey: "key", BaseURL: server.URL}
	if _, err := engine.Translate(context.Background(), TranslateRequest{SourceLang: "en", TargetLang: "es", Texts: []string{"Hello"}}); err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if gotReq.TagHandling != "" {
		t.Errorf("plain Translate must not set tag_handling; got %q", gotReq.TagHandling)
	}
	if gotReq.OutlineDetection != nil {
		t.Errorf("plain Translate must not set outline_detection; got %v", *gotReq.OutlineDetection)
	}
}

func TestDeepLEmptyInput(t *testing.T) {
	engine := DeepL{APIKey: "test-key"}
	results, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "de",
		Texts:      nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty input, got %v", results)
	}
}

func TestDeepLNoAPIKey(t *testing.T) {
	engine := DeepL{}
	_, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "de",
		Texts:      []string{"Hello"},
	})
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
}

func TestDeepLError403(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"message": "Forbidden"}`))
	}))
	defer server.Close()

	engine := DeepL{APIKey: "bad-key", BaseURL: server.URL}
	_, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "de",
		Texts:      []string{"Hello"},
	})
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if got := err.Error(); got != "deepl: authentication failed (403) — check your API key" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestDeepLError429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer server.Close()

	engine := DeepL{APIKey: "key", BaseURL: server.URL}
	_, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "de",
		Texts:      []string{"Hello"},
	})
	if err == nil {
		t.Fatal("expected error for 429")
	}
	if got := err.Error(); got != "deepl: rate limit exceeded (429) — too many requests" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestDeepLError456(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(456)
	}))
	defer server.Close()

	engine := DeepL{APIKey: "key", BaseURL: server.URL}
	_, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "de",
		Texts:      []string{"Hello"},
	})
	if err == nil {
		t.Fatal("expected error for 456")
	}
	if got := err.Error(); got != "deepl: quota exceeded (456) — translation limit reached" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestDeepLLangCodeMapping(t *testing.T) {
	tests := []struct {
		code     string
		isTarget bool
		want     string
	}{
		{"en", false, "EN"},
		{"de", false, "DE"},
		{"en", true, "EN-US"},
		{"pt", true, "PT-PT"},
		{"de", true, "DE"},
		{"fr", true, "FR"},
		{"en-gb", true, "EN-GB"},
	}
	for _, tt := range tests {
		got := deeplLangCode(tt.code, tt.isTarget)
		if got != tt.want {
			t.Errorf("deeplLangCode(%q, %v) = %q, want %q", tt.code, tt.isTarget, got, tt.want)
		}
	}
}

func TestDeepLFreeVsProURL(t *testing.T) {
	free := DeepL{APIKey: "abc123:fx"}
	if got := free.resolveBaseURL(); got != "https://api-free.deepl.com" {
		t.Errorf("free key URL = %q, want api-free", got)
	}

	pro := DeepL{APIKey: "abc123"}
	if got := pro.resolveBaseURL(); got != "https://api.deepl.com" {
		t.Errorf("pro key URL = %q, want api.deepl.com", got)
	}

	custom := DeepL{APIKey: "key", BaseURL: "http://custom:8080"}
	if got := custom.resolveBaseURL(); got != "http://custom:8080" {
		t.Errorf("custom URL = %q, want http://custom:8080", got)
	}
}

func TestDeepLTargetEnglishVariant(t *testing.T) {
	// Verify that translating TO English sends EN-US
	var receivedTargetLang string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req deeplRequest
		json.NewDecoder(r.Body).Decode(&req)
		receivedTargetLang = req.TargetLang
		resp := deeplResponse{
			Translations: []struct {
				Text string `json:"text"`
			}{{Text: "Hello"}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	engine := DeepL{APIKey: "key", BaseURL: server.URL}
	engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "de",
		TargetLang: "en",
		Texts:      []string{"Hallo"},
	})

	if receivedTargetLang != "EN-US" {
		t.Errorf("target lang = %q, want EN-US", receivedTargetLang)
	}
}

// An engine's options have to reach the wire — a stored option that never gets
// sent is a form that silently does nothing.
func TestDeepLSendsEngineOptions(t *testing.T) {
	var gotRaw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotRaw)
		resp := deeplResponse{Translations: []struct {
			Text string `json:"text"`
		}{{Text: "Hola"}}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Built from the stored settings object, as the pipeline does, so a key
	// renamed in the factory drops an option here too.
	engine, err := NewFromDB("deepl", "key", server.URL, map[string]any{
		"formality": "prefer_more", "glossary_id": "gl-1", "preserve_formatting": false,
		"split_sentences": "nonewlines", "model_type": "quality_optimized",
	})
	if err != nil {
		t.Fatalf("NewFromDB: %v", err)
	}
	if _, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en", TargetLang: "es", Texts: []string{"Hello"},
	}); err != nil {
		t.Fatalf("Translate: %v", err)
	}

	for key, want := range map[string]any{
		"formality": "prefer_more", "glossary_id": "gl-1",
		"split_sentences": "nonewlines", "model_type": "quality_optimized",
		// A chosen false must SEND false. Read off the raw body rather than the
		// typed struct: `omitempty` on a plain bool would drop it here, which is
		// exactly the bug the pointer prevents.
		"preserve_formatting": false,
	} {
		if got, ok := gotRaw[key]; !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", key, got, ok, want)
		}
	}
}

// ...and an engine with no options must send none, so DeepL's own defaults
// apply rather than a field we invented.
func TestDeepLSendsNoOptionsWhenUnset(t *testing.T) {
	var gotRaw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotRaw)
		resp := deeplResponse{Translations: []struct {
			Text string `json:"text"`
		}{{Text: "Hola"}}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	engine := DeepL{APIKey: "key", BaseURL: server.URL}
	if _, err := engine.Translate(context.Background(), TranslateRequest{
		SourceLang: "en", TargetLang: "es", Texts: []string{"Hello"},
	}); err != nil {
		t.Fatalf("Translate: %v", err)
	}
	for _, key := range []string{"formality", "glossary_id", "preserve_formatting", "split_sentences", "model_type"} {
		if _, present := gotRaw[key]; present {
			t.Errorf("%s was sent with no option set: %v", key, gotRaw[key])
		}
	}
}
