package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// texts builds index-ided TranslateInputs, the way the pipeline does for a
// chunk without segment identity.
func texts(ss ...string) []TranslateInput {
	in := make([]TranslateInput, len(ss))
	for i, s := range ss {
		in[i] = TranslateInput{ID: fmt.Sprint(i), Text: s}
	}
	return in
}

// openAIStub answers every segment in the batch with its uppercased content,
// in the {"results":[…]} envelope.
func openAIStub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/chat/completions") {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_, segs := decodeRequest(t, body)
		objects := make([]string, len(segs))
		for i, s := range segs {
			obj, _ := json.Marshal(map[string]string{"id": s.ID, "target": strings.ToUpper(s.text())})
			objects[i] = string(obj)
		}
		writeOpenAIContent(w, resultsReply(objects...))
	}))
}

func writeOpenAIContent(w http.ResponseWriter, content string) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{"message": map[string]string{"content": content}}},
	})
}

// MT replies follow the {"target": string} contract
// per segment, wrapped in the batch envelope.
//
// Pinned to a model without structured-output support: that is the path where
// the prose contract is the whole story. On a schema-capable model the prose
// half is dropped on purpose — see
// TestAssemblePrompt_SchemaModelDropsTheRedundantHalf.
func TestTranslateBatch_ParsesTargetContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		system, segs := decodeRequest(t, body)
		if !strings.Contains(system, `"target": string`) {
			t.Errorf("MT system prompt must state the target contract, got: %s", system)
		}
		if strings.Contains(system, `"comment"`) {
			t.Errorf("MT declares no comment output, so the contract must not ask for one: %s", system)
		}
		if !strings.Contains(system, `"results"`) {
			t.Errorf("MT system prompt must ask for the results envelope, got: %s", system)
		}
		writeOpenAIContent(w, "```json\n"+resultsReply(`{"id":"`+segs[0].ID+`","target":"Hallo Welt"}`)+"\n```")
	}))
	defer srv.Close()

	tr := NewHTTPTranslator()
	out, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, Model: "gpt-4-turbo", APIKey: "sk-test", BaseURL: srv.URL,
		SourceLang: "en", TargetLang: "de",
	}, texts("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Text != "Hallo Welt" {
		t.Errorf("want parsed target, got %q", out[0].Text)
	}
}

// An MT prompt declares no comment output, so a model that volunteers one
// anyway is translated from and otherwise ignored — it must not reach the
// segment as an AI comment.
func TestTranslateBatch_IgnoresAVolunteeredComment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		system, segs := decodeRequest(t, body)
		if strings.Contains(system, `"comment"`) {
			t.Errorf("the MT contract must not ask for a comment, got: %s", system)
		}
		writeOpenAIContent(w, resultsReply(`{"id":"`+segs[0].ID+`","target":"Hallo","comment":"brand name kept in English"}`))
	}))
	defer srv.Close()

	tr := NewHTTPTranslator()
	out, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, Model: "gpt-4-turbo", APIKey: "sk-test", BaseURL: srv.URL,
		SourceLang: "en", TargetLang: "de",
	}, texts("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Text != "Hallo" {
		t.Errorf("want the target parsed, got %+v", out[0])
	}
}

func TestTranslateBatch_OpenAI(t *testing.T) {
	srv := openAIStub(t)
	defer srv.Close()

	tr := NewHTTPTranslator()
	out, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider:   ProviderOpenAI,
		APIKey:     "sk-test",
		BaseURL:    srv.URL,
		PromptText: "translate faithfully",
		SourceLang: "en",
		TargetLang: "es",
	}, texts("hello", "", "world"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 results (index-aligned), got %d", len(out))
	}
	if out[0].Text != "HELLO" || out[2].Text != "WORLD" {
		t.Errorf("unexpected translations: %v", out)
	}
	// Blank input stays blank (never sent) — index alignment preserved.
	if out[1].Text != "" {
		t.Errorf("empty input should pass through empty, got %q", out[1].Text)
	}
}

// Batching: N segments cost N/llmBatchSize requests, not N.
func TestTranslateBatch_BatchesRequests(t *testing.T) {
	var mu sync.Mutex
	calls, batchSizes := 0, []int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, segs := decodeRequest(t, body)
		mu.Lock()
		calls++
		batchSizes = append(batchSizes, len(segs))
		mu.Unlock()
		objects := make([]string, len(segs))
		for i, s := range segs {
			objects[i] = `{"id":"` + s.ID + `","target":"t-` + s.text() + `"}`
		}
		writeOpenAIContent(w, resultsReply(objects...))
	}))
	defer srv.Close()

	// Two full batches plus a remainder, derived so the case keeps its shape
	// whatever llmBatchSize becomes.
	in := make([]string, llmBatchSize*2+5)
	for i := range in {
		in[i] = fmt.Sprintf("s%d", i)
	}
	tr := NewHTTPTranslator()
	out, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, APIKey: "sk-test", BaseURL: srv.URL,
	}, texts(in...))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("%d segments at batch size %d → want 3 requests, got %d (sizes %v)", len(in), llmBatchSize, calls, batchSizes)
	}
	for i := range in {
		if want := "t-" + in[i]; out[i].Text != want {
			t.Fatalf("result %d: want %q, got %q", i, want, out[i].Text)
		}
	}
}

// Results are matched by id, so a model that reorders (or interleaves) its
// array can't misattribute a translation to the wrong segment.
func TestTranslateBatch_MatchesByIDNotPosition(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, segs := decodeRequest(t, body)
		objects := make([]string, 0, len(segs))
		for i := len(segs) - 1; i >= 0; i-- { // reversed on purpose
			objects = append(objects, `{"id":"`+segs[i].ID+`","target":"T-`+segs[i].text()+`"}`)
		}
		writeOpenAIContent(w, resultsReply(objects...))
	}))
	defer srv.Close()

	tr := NewHTTPTranslator()
	out, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, APIKey: "sk-test", BaseURL: srv.URL,
	}, texts("one", "two", "three"))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"T-one", "T-two", "T-three"} {
		if out[i].Text != want {
			t.Errorf("result %d: want %q, got %q — results must be matched by id", i, want, out[i].Text)
		}
	}
}

// A segment the model skips is retried on its own; the other segments in the
// batch keep their translations.
func TestTranslateBatch_RetriesMissingSegment(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, segs := decodeRequest(t, body)
		mu.Lock()
		calls++
		mu.Unlock()
		objects := make([]string, 0, len(segs))
		for _, s := range segs {
			if len(segs) > 1 && s.text() == "two" {
				continue // drop it from the batched reply
			}
			objects = append(objects, `{"id":"`+s.ID+`","target":"T-`+s.text()+`"}`)
		}
		writeOpenAIContent(w, resultsReply(objects...))
	}))
	defer srv.Close()

	tr := NewHTTPTranslator()
	out, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, APIKey: "sk-test", BaseURL: srv.URL,
	}, texts("one", "two", "three"))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("want 1 batch + 1 single-segment retry = 2 requests, got %d", calls)
	}
	for i, want := range []string{"T-one", "T-two", "T-three"} {
		if out[i].Text != want {
			t.Errorf("result %d: want %q, got %q", i, want, out[i].Text)
		}
	}
}

// When even the retry comes back empty the result stays empty — the pipeline
// then leaves the segment untranslated rather than writing blank text.
func TestTranslateBatch_UnanswerableSegmentStaysEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, segs := decodeRequest(t, body)
		objects := make([]string, 0, len(segs))
		for _, s := range segs {
			if s.text() == "two" {
				continue
			}
			objects = append(objects, `{"id":"`+s.ID+`","target":"T-`+s.text()+`"}`)
		}
		writeOpenAIContent(w, resultsReply(objects...))
	}))
	defer srv.Close()

	tr := NewHTTPTranslator()
	out, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, APIKey: "sk-test", BaseURL: srv.URL,
	}, texts("one", "two", "three"))
	if err != nil {
		t.Fatal(err)
	}
	if out[1].Text != "" {
		t.Errorf("unanswered segment must stay empty, got %q", out[1].Text)
	}
	if out[0].Text != "T-one" || out[2].Text != "T-three" {
		t.Errorf("one bad segment must not cost the others: %+v", out)
	}
}

// The rendered segment prompt is what the model sees, not the raw
// source text.
func TestTranslateBatch_SendsRenderedSegmentPrompt(t *testing.T) {
	var got []reqSegment
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, segs := decodeRequest(t, body)
		got = segs
		writeOpenAIContent(w, resultsReply(`{"id":"`+segs[0].ID+`","target":"x"}`))
	}))
	defer srv.Close()

	tr := NewHTTPTranslator()
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, APIKey: "sk-test", BaseURL: srv.URL,
	}, []TranslateInput{{ID: "5001", Text: "hello", Content: json.RawMessage(`{"source":"hello","tm_match":{"source":"hi","target":"hola"}}`)}}); err != nil {
		t.Fatal(err)
	}
	// The rendered context is spliced in flat, beside the id.
	if len(got) != 1 || got[0].text() != "hello" || !strings.Contains(got[0].raw(), `"tm_match"`) {
		t.Fatalf("want the rendered context beside the id, got %+v", got)
	}
	if got[0].ID != "5001" {
		t.Errorf("want the caller's id echoed into the request, got %q", got[0].ID)
	}
}

// TestTranslateBatch_UsesConfiguredModel proves the prompt's pinned model
// reaches the provider request, and that an empty Model falls back to the
// provider default.
func TestTranslateBatch_UsesConfiguredModel(t *testing.T) {
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &req)
		gotModel = req.Model
		_, segs := decodeRequest(t, body)
		writeOpenAIContent(w, resultsReply(`{"id":"`+segs[0].ID+`","target":"x"}`))
	}))
	defer srv.Close()

	tr := NewHTTPTranslator()

	// Configured model wins.
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, Model: "gpt-4o", APIKey: "sk-test", BaseURL: srv.URL,
	}, texts("hi")); err != nil {
		t.Fatal(err)
	}
	if gotModel != "gpt-4o" {
		t.Errorf("expected configured model %q in request, got %q", "gpt-4o", gotModel)
	}

	// Empty model → provider default.
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI, APIKey: "sk-test", BaseURL: srv.URL,
	}, texts("hi")); err != nil {
		t.Fatal(err)
	}
	if gotModel != defaultOpenAIModel {
		t.Errorf("expected fallback model %q, got %q", defaultOpenAIModel, gotModel)
	}
}

func TestTranslateBatch_AuthErrorPropagates(t *testing.T) {
	srv := openAIStub(t)
	defer srv.Close()

	tr := NewHTTPTranslator()
	_, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI,
		APIKey:   "wrong-key",
		BaseURL:  srv.URL,
	}, texts("hello"))
	if err == nil {
		t.Fatal("expected an auth error to propagate")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ProviderError, got %T: %v", err, err)
	}
	if pe.Kind != ProviderErrAuth {
		t.Errorf("expected kind %q, got %q", ProviderErrAuth, pe.Kind)
	}
	if pe.Status != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", pe.Status)
	}
}

func TestTranslateBatch_QuotaClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`))
	}))
	defer srv.Close()

	tr := NewHTTPTranslator()
	_, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderOpenAI,
		APIKey:   "sk-test",
		BaseURL:  srv.URL,
	}, texts("hello"))
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ProviderError, got %T: %v", err, err)
	}
	if pe.Kind != ProviderErrQuota {
		t.Errorf("expected kind %q (429 insufficient_quota), got %q", ProviderErrQuota, pe.Kind)
	}
	if !strings.Contains(pe.Detail, "exceeded your current quota") {
		t.Errorf("expected provider message in detail, got %q", pe.Detail)
	}
}

func TestTranslateBatch_RejectsExtension(t *testing.T) {
	tr := NewHTTPTranslator()
	_, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: testExtProvider,
		APIKey:   "x",
	}, texts("hi"))
	if err == nil {
		t.Fatal("an extension reviews translations, it is not an AI translator — expected rejection")
	}
}

// Claude requires max_tokens; it must scale with the batch so an APE-sized
// reply isn't truncated mid-array.
func TestTranslateBatch_ClaudeMaxTokensScales(t *testing.T) {
	var gotMaxTokens int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.Unmarshal(body, &req)
		gotMaxTokens = req.MaxTokens
		_, segs := decodeRequest(t, body)
		objects := make([]string, len(segs))
		for i, s := range segs {
			objects[i] = `{"id":"` + s.ID + `","target":"x"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": resultsReply(objects...)}},
		})
	}))
	defer srv.Close()

	in := make([]string, llmBatchSize)
	for i := range in {
		in[i] = fmt.Sprintf("s%d", i)
	}
	tr := NewHTTPTranslator()
	if _, err := tr.TranslateBatch(context.Background(), TranslateConfig{
		Provider: ProviderClaude, APIKey: "sk-ant", BaseURL: srv.URL,
	}, texts(in...)); err != nil {
		t.Fatal(err)
	}
	// No Model set → the default Claude model isn't in the catalog lookup by
	// empty id, so no settings apply and thinking is treated as off.
	if want := batchMaxTokens(llmBatchSize, false); gotMaxTokens != want {
		t.Errorf("want max_tokens %d for a full batch, got %d", want, gotMaxTokens)
	}
}

// The request body keeps <, > and & as written in every string — system prompt
// and user message — so the raw body reads like the prompt it carries.
func TestRequestBody_KeepsMarkupCharacters(t *testing.T) {
	for _, tc := range []struct {
		name, provider, model string
		claude                bool
	}{
		{"openai", ProviderOpenAI, testOpenAIModel, false},
		{"claude", ProviderClaude, testSonnet5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ = io.ReadAll(r.Body)
				_, segs := decodeRequest(t, raw)
				reply := resultsReply(`{"id":"` + segs[0].ID + `","target":"x"}`)
				if tc.claude {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"content": []map[string]string{{"type": "text", "text": reply}},
					})
					return
				}
				writeOpenAIContent(w, reply)
			}))
			defer srv.Close()

			if _, err := NewHTTPTranslator().TranslateBatch(context.Background(), TranslateConfig{
				Provider: tc.provider, Model: tc.model, APIKey: "sk-test", BaseURL: srv.URL,
				PromptText: "Keep <1>tags</1> & spacing.",
			}, texts("See <1>details</1>.")); err != nil {
				t.Fatal(err)
			}
			body := string(raw)
			for _, escape := range []string{"\\u003c", "\\u003e", "\\u0026"} {
				if strings.Contains(body, escape) {
					t.Errorf("request body escapes markup characters (%s): %s", escape, body)
				}
			}
			if !strings.Contains(body, "Keep <1>tags</1> & spacing.") || !strings.Contains(body, "See <1>details</1>.") {
				t.Errorf("request body lost the prompt or the segment as written: %s", body)
			}
		})
	}
}
