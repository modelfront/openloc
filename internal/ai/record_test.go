package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"openloc/internal/llmdebug"
)

// captureRecorder collects recorded entries for assertions.
type captureRecorder struct {
	mu      sync.Mutex
	entries []llmdebug.Entry
}

func (c *captureRecorder) Record(_ context.Context, e llmdebug.Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
}

func (c *captureRecorder) all() []llmdebug.Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]llmdebug.Entry(nil), c.entries...)
}

// echoStub returns an OpenAI-shaped 200 and hands the test the raw request
// bytes the server actually received, for literal-body equality assertions.
func echoStub(t *testing.T, received *[]byte) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		*received = body
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": `{"target": "Hallo"}`}},
			},
		})
	}))
}

func TestRecord_TranslateBatchCapturesMTCall(t *testing.T) {
	var received []byte
	srv := echoStub(t, &received)
	defer srv.Close()

	rec := &captureRecorder{}
	tr := NewHTTPTranslator().WithRecorder(rec)
	_, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, APIKey: "sk-secret-key", BaseURL: srv.URL,
		PromptText: "Be precise. Translate from <source_lang> to <target_lang>.", SourceLang: "en", TargetLang: "de",
	}, texts("hello"))
	if err != nil {
		t.Fatal(err)
	}

	entries := rec.all()
	if len(entries) != 1 {
		t.Fatalf("want 1 recorded entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Kind != llmdebug.KindMT {
		t.Errorf("want kind mt, got %q", e.Kind)
	}
	if e.Provider != ProviderOpenAI {
		t.Errorf("want provider openai, got %q", e.Provider)
	}
	if e.Model != defaultOpenAIModel {
		t.Errorf("want effective default model %q when preset empty, got %q", defaultOpenAIModel, e.Model)
	}
	if e.Status != http.StatusOK {
		t.Errorf("want status 200, got %d", e.Status)
	}
	if e.RequestBody != string(received) {
		t.Errorf("RequestBody must equal the literal bytes the server received.\nrecorded: %s\nreceived: %s", e.RequestBody, received)
	}
	// The recorded system prompt is what actually went out, tokens resolved —
	// the debug viewer must not show the author's unsubstituted text.
	if !strings.Contains(e.System, "Be precise.") || !strings.Contains(e.System, "from English (en) to German (de)") {
		t.Errorf("System must carry the instruction with its languages resolved, got: %s", e.System)
	}
	// The user message is the batch envelope, so the recorded body
	// shows exactly what went over the wire — one entry per request, not per
	// segment.
	var segs []reqSegment
	if err := json.Unmarshal([]byte(e.User), &segs); err != nil ||
		len(segs) != 1 || segs[0].text() != "hello" {
		t.Errorf("User must be the batched array carrying the segment, got %q (err %v)", e.User, err)
	}
	if !strings.Contains(e.ResponseBody, "Hallo") {
		t.Errorf("ResponseBody must carry the raw provider reply, got: %s", e.ResponseBody)
	}
}

func TestRecord_PredictBatchKindsByMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": `{"approved": true, "score": 95}`}},
			},
		})
	}))
	defer srv.Close()

	for _, tc := range []struct {
		mode     string
		wantKind string
	}{
		{ModeQE, llmdebug.KindQE},
		{ModeAPE, llmdebug.KindAPE},
		{ModeQEAPE, llmdebug.KindAPE},
	} {
		rec := &captureRecorder{}
		tr := NewHTTPTranslator().WithRecorder(rec)
		_, err := tr.PredictBatch(context.Background(), PredictConfig{
			Provider: ProviderOpenAI, APIKey: "sk-secret-key", BaseURL: srv.URL,
			SourceLang: "en", TargetLang: "de", Mode: tc.mode,
		}, []PredictInput{{ID: "1", Source: "hello", Translation: "Hallo"}})
		if err != nil {
			t.Fatal(err)
		}
		entries := rec.all()
		if len(entries) != 1 {
			t.Fatalf("want 1 entry, got %d", len(entries))
		}
		if entries[0].Kind != tc.wantKind {
			t.Errorf("mode=%s: want kind %q, got %q", tc.mode, tc.wantKind, entries[0].Kind)
		}
		var segs []map[string]string
		if err := json.Unmarshal([]byte(entries[0].User), &segs); err != nil || len(segs) != 1 {
			t.Fatalf("User must be the batched array, got %q (err %v)", entries[0].User, err)
		}
		// Flat: the id sits beside the context keys, no content wrapper.
		if segs[0]["id"] != "1" || segs[0]["source"] != "hello" || segs[0]["translation"] != "Hallo" {
			t.Errorf(`segment must be {"id","source","translation"}, got %+v`, segs[0])
		}
	}
}

func TestRecord_ErrorResponseCaptured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	rec := &captureRecorder{}
	tr := NewHTTPTranslator().WithRecorder(rec)
	_, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderClaude, APIKey: "sk-ant-secret", BaseURL: srv.URL,
		SourceLang: "en", TargetLang: "de",
	}, texts("hello"))
	if err == nil {
		t.Fatal("want provider error")
	}
	entries := rec.all()
	if len(entries) != 1 {
		t.Fatalf("want the failed call recorded, got %d entries", len(entries))
	}
	e := entries[0]
	if e.Status != http.StatusUnauthorized {
		t.Errorf("want status 401, got %d", e.Status)
	}
	if e.Error == "" || !strings.Contains(e.Error, "bad key") {
		t.Errorf("want Error carrying the provider message, got %q", e.Error)
	}
	if !strings.Contains(e.ResponseBody, "bad key") {
		t.Errorf("want ResponseBody carrying the provider error body, got %q", e.ResponseBody)
	}
	if e.Model != defaultClaudeModel {
		t.Errorf("want effective default claude model, got %q", e.Model)
	}
}

// TestRecord_NeverCapturesAPIKey locks in the redaction-by-construction
// invariant: headers are never passed to the recorder, so the key must not
// appear anywhere in a marshaled entry — for either provider, on success or
// failure.
func TestRecord_NeverCapturesAPIKey(t *testing.T) {
	const secret = "sk-SUPER-SECRET-KEY-123"
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]string{"content": "ok"}},
				},
			})
		}))
		for _, provider := range []string{ProviderOpenAI, ProviderClaude} {
			rec := &captureRecorder{}
			tr := NewHTTPTranslator().WithRecorder(rec)
			_, _ = tr.TranslateBatch(context.Background(), TranslateConfig{
				Provider: provider, APIKey: secret, BaseURL: srv.URL,
				SourceLang: "en", TargetLang: "de",
			}, texts("hello"))
			for _, e := range rec.all() {
				marshaled, err := json.Marshal(e)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(marshaled), secret) {
					t.Fatalf("provider %s status %d: API key leaked into recorded entry: %s", provider, status, marshaled)
				}
			}
		}
		srv.Close()
	}
}

func TestRecord_NilRecorderNoPanic(t *testing.T) {
	var received []byte
	srv := echoStub(t, &received)
	defer srv.Close()

	tr := NewHTTPTranslator() // no recorder wired — production path
	_, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, APIKey: "sk-test", BaseURL: srv.URL,
		SourceLang: "en", TargetLang: "de",
	}, texts("hello"))
	if err != nil {
		t.Fatal(err)
	}
}

// A feed of 10-segment requests with 1-segment requests scattered through it
// is unreadable without these two fields: the small ones are a mix of batch
// tails, over-budget segments travelling alone, and retries of segments a
// reply left out. Only the last means the model misbehaved.
func TestRecord_CapturesBatchSizeAndRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First reply answers for segment 1 and silently drops segment 2 —
		// exactly what triggers the split-and-retry.
		body := `{"results":[{"id":"1","approved":true,"score":90}]}`
		if calls.Add(1) > 1 {
			body = `{"results":[{"id":"2","approved":false,"score":20}]}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": body}}},
		})
	}))
	defer srv.Close()

	rec := &captureRecorder{}
	tr := NewHTTPTranslator().WithRecorder(rec)
	out, err := tr.PredictBatch(context.Background(), PredictConfig{
		Provider: ProviderOpenAI, APIKey: "sk-test", BaseURL: srv.URL,
		SourceLang: "en", TargetLang: "de", Mode: ModeQE,
	}, []PredictInput{
		{ID: "1", Source: "Hello", Translation: "Hallo"},
		{ID: "2", Source: "Bye", Translation: "Tschüss"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[1].Invalid {
		t.Fatalf("the retry should have answered segment 2: %+v", out)
	}

	entries := rec.all()
	if len(entries) != 2 {
		t.Fatalf("want 2 recorded calls (the batch and its retry), got %d", len(entries))
	}
	if entries[0].Segments != 2 || entries[0].Retry {
		t.Errorf("first call = %d segments, retry=%v; want 2 segments and not a retry",
			entries[0].Segments, entries[0].Retry)
	}
	// The retry is what makes a 1-segment request meaningful rather than noise.
	if entries[1].Segments != 1 || !entries[1].Retry {
		t.Errorf("second call = %d segments, retry=%v; want 1 segment marked as a retry",
			entries[1].Segments, entries[1].Retry)
	}
}
