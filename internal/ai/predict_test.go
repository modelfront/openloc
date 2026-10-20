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
	"testing"
)

// predictStub is an OpenAI-shaped server whose reply content is produced by
// respond(batchedSegments, systemPrompt) — tests script per-segment outputs
// and can capture the prompts. The reply must be the
// {"results":[…]} envelope; verdictReply builds it.
func predictStub(t *testing.T, respond func(segs []reqSegment, system string) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		system, segs := decodeRequest(t, body)
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": respond(segs, system)}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// verdictReply answers each segment with verdict(seg), injecting its id.
func verdictReply(segs []reqSegment, verdict func(reqSegment) string) string {
	objects := make([]string, len(segs))
	for i, s := range segs {
		objects[i] = `{"id":"` + s.ID + `",` + strings.TrimPrefix(verdict(s), "{")
	}
	return resultsReply(objects...)
}

// The model is pinned to one WITHOUT structured-output support, because these
// tests are about the prose contract — on a schema-capable model the provider
// enforces the shape and the prose half is deliberately omitted
// (TestAssemblePrompt_SchemaModelDropsTheRedundantHalf covers that side).
func predictCfg(baseURL, mode string) PredictConfig {
	return PredictConfig{
		Provider:   ProviderOpenAI,
		Model:      "gpt-4-turbo",
		APIKey:     "sk-test",
		BaseURL:    baseURL,
		PromptText: "Judge strictly.",
		SourceLang: "en",
		TargetLang: "de",
		Mode:       mode,
	}
}

// The happy path: the model returns our expected format {postedit, approved,
// score, postedit_approved, postedit_score, comment} and every field lands
// parsed on the aligned result — the original's verdict and the postedit's
// verdict stay separate.
func TestPredictBatch_ParsesContract(t *testing.T) {
	srv := predictStub(t, func(segs []reqSegment, _ string) string {
		// Script verdicts off the segment content: the good segment approves,
		// the bad one gets an approved postedit + comment.
		return verdictReply(segs, func(s reqSegment) string {
			if strings.Contains(s.raw(), "Hallo") {
				return `{"postedit": null, "approved": true, "score": 97, "postedit_approved": null, "postedit_score": null, "comment": null}`
			}
			return `{"postedit": "Welt", "approved": false, "score": 41.5, "postedit_approved": true, "postedit_score": 92, "comment": "typo in target"}`
		})
	})
	defer srv.Close()

	tr := NewHTTPTranslator()
	out, err := tr.PredictBatch(context.Background(), predictCfg(srv.URL, ModeQEAPE), []PredictInput{
		{ID: "1", Source: "Hello", Translation: "Hallo"},
		{ID: "2", Source: "World", Translation: "Wlt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 aligned results, got %d", len(out))
	}
	if out[0].ID != "1" || !out[0].Approved || out[0].Postedit != nil || out[0].Comment != nil || out[0].PosteditApproved != nil {
		t.Errorf("result 0: want approved/no-postedit/no-comment, got %+v", out[0])
	}
	if out[0].Score == nil || *out[0].Score != 97 {
		t.Errorf("result 0: want score 97, got %v", out[0].Score)
	}
	if out[1].ID != "2" || out[1].Approved {
		t.Errorf("result 1: want original not-approved, got %+v", out[1])
	}
	if out[1].Postedit == nil || *out[1].Postedit != "Welt" {
		t.Errorf("result 1: want postedit \"Welt\", got %v", out[1].Postedit)
	}
	if out[1].PosteditApproved == nil || !*out[1].PosteditApproved {
		t.Errorf("result 1: want postedit_approved=true, got %v", out[1].PosteditApproved)
	}
	if out[1].PosteditScore == nil || *out[1].PosteditScore != 92 {
		t.Errorf("result 1: want postedit_score 92, got %v", out[1].PosteditScore)
	}
	if out[1].Comment == nil || *out[1].Comment != "typo in target" {
		t.Errorf("result 1: want comment, got %v", out[1].Comment)
	}
	if out[1].Score == nil || *out[1].Score != 41.5 {
		t.Errorf("result 1: want original score 41.5, got %v", out[1].Score)
	}
	if out[0].Invalid || out[1].Invalid {
		t.Errorf("contract-conforming outputs must not be Invalid")
	}
}

// A chatty model that wraps the JSON in a code fence + preamble still parses;
// out-of-range scores clamp to 0-100; empty-string postedit/comment → nil.
func TestPredictBatch_TolerantParsing(t *testing.T) {
	srv := predictStub(t, func(segs []reqSegment, _ string) string {
		return "Here is my assessment:\n```json\n" +
			verdictReply(segs, func(reqSegment) string {
				return `{"approved": true, "postedit": "", "score": 250, "comment": "  "}`
			}) + "\n```"
	})
	defer srv.Close()

	tr := NewHTTPTranslator()
	out, err := tr.PredictBatch(context.Background(), predictCfg(srv.URL, ModeQEAPE), []PredictInput{
		{ID: "1", Source: "Hello", Translation: "Hallo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := out[0]
	if r.Invalid {
		t.Fatalf("fenced output should parse, got Invalid (raw %q)", r.Raw)
	}
	if !r.Approved {
		t.Errorf("want approved=true, got %+v", r)
	}
	if r.Postedit != nil {
		t.Errorf("empty-string postedit should map to nil, got %v", *r.Postedit)
	}
	if r.Comment != nil {
		t.Errorf("blank comment should map to nil, got %v", *r.Comment)
	}
	if r.Score == nil || *r.Score != 100 {
		t.Errorf("score should clamp to 100, got %v", r.Score)
	}
}

// Output that doesn't match the contract — including a missing or non-boolean
// "approved" — flags Invalid and preserves the raw text so the pipeline can
// apply the row's bad-output action.
func TestPredictBatch_InvalidOutput(t *testing.T) {
	for _, garbage := range []string{
		"The translation looks fine to me!",
		`{"approved": "maybe", "score": 50}`,
		`{"score": 50, "comment": "no verdict"}`,
	} {
		srv := predictStub(t, func([]reqSegment, string) string { return garbage })
		tr := NewHTTPTranslator()
		out, err := tr.PredictBatch(context.Background(), predictCfg(srv.URL, ModeQE), []PredictInput{
			{ID: "1", Source: "Hello", Translation: "Hallo"},
		})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !out[0].Invalid {
			t.Errorf("output %q should be Invalid", garbage)
		}
		if out[0].Raw != garbage {
			t.Errorf("Raw should carry the offending output, got %q", out[0].Raw)
		}
	}
}

// The QE contract must forbid rewriting and not mention a postedit field; the
// APE contract must ask for one. The user message carries the segment as JSON
// so source/translation can't be conflated.
func TestPredictBatch_ContractPerMode(t *testing.T) {
	var system string
	var segments []reqSegment
	srv := predictStub(t, func(segs []reqSegment, s string) string {
		segments, system = segs, s
		return verdictReply(segs, func(reqSegment) string {
			return `{"approved": true, "score": 90, "comment": null}`
		})
	})
	defer srv.Close()
	tr := NewHTTPTranslator()

	in := []PredictInput{{ID: "1", Source: "Hello", Translation: "Hallo"}}
	if _, err := tr.PredictBatch(context.Background(), predictCfg(srv.URL, ModeQE), in); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(system, `"postedit"`) {
		t.Errorf("QE system prompt must not request a postedit, got: %s", system)
	}
	if !strings.Contains(system, "Judge strictly.") {
		t.Errorf("system prompt must carry the engine row's instruction text")
	}
	for _, key := range []string{`"approved"`, `"score"`, `"comment"`} {
		if !strings.Contains(system, key) {
			t.Errorf("QE contract must include %s", key)
		}
	}
	if len(segments) != 1 || segments[0].ID != "1" {
		t.Fatalf("want one batched segment carrying the caller's id, got %+v", segments)
	}
	// Flat: the source and translation sit beside the id, not under a wrapper.
	var seg struct{ Source, Translation string }
	if err := json.Unmarshal([]byte(segments[0].raw()), &seg); err != nil || seg.Source != "Hello" || seg.Translation != "Hallo" {
		t.Errorf("a segment should carry its text beside its id, got %q (err %v)", segments[0].text(), err)
	}

	if _, err := tr.PredictBatch(context.Background(), predictCfg(srv.URL, ModeQEAPE), in); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"postedit"`, `"approved"`, `"score"`, `"postedit_approved"`, `"postedit_score"`, `"comment"`} {
		if !strings.Contains(system, key) {
			t.Errorf("APE contract must include %s", key)
		}
	}
}

// Claude transport: same contract through the /v1/messages shape.
func TestPredictBatch_Claude(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		if r.Header.Get("x-api-key") != "sk-ant" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": `{"postedit": "Besser", "approved": true, "score": 55, "comment": "minor fix"}`},
			},
		})
	}))
	defer srv.Close()

	tr := NewHTTPTranslator()
	out, err := tr.PredictBatch(context.Background(), PredictConfig{
		Provider: ProviderClaude, APIKey: "sk-ant", BaseURL: srv.URL,
		SourceLang: "en", TargetLang: "de", Mode: ModeQEAPE,
	}, []PredictInput{{ID: "7", Source: "Hello", Translation: "Hallo"}})
	if err != nil {
		t.Fatal(err)
	}
	r := out[0]
	if r.ID != "7" || !r.Approved || r.Postedit == nil || *r.Postedit != "Besser" ||
		r.Score == nil || *r.Score != 55 || r.Comment == nil || *r.Comment != "minor fix" {
		t.Errorf("unexpected claude result: %+v", r)
	}
}

// Provider failures classify into ProviderError so the pipeline can surface
// an actionable message (same mapping as TranslateBatch).
func TestPredictBatch_ProviderErrorClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"bad key","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()

	tr := NewHTTPTranslator()
	_, err := tr.PredictBatch(context.Background(), predictCfg(srv.URL, ModeQE), []PredictInput{
		{ID: "1", Source: "Hello", Translation: "Hallo"},
	})
	var provErr *ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if provErr.Kind != ProviderErrAuth {
		t.Errorf("expected auth kind, got %q", provErr.Kind)
	}
}

// An extension can't run the LLM contract — its QE+APE goes through its own
// predict function, never through PredictBatch.
func TestPredictBatch_RejectsExtension(t *testing.T) {
	tr := NewHTTPTranslator()
	_, err := tr.PredictBatch(context.Background(), PredictConfig{Provider: testExtProvider}, []PredictInput{
		{ID: "1", Source: "a", Translation: "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot run LLM QE+APE") {
		t.Fatalf("expected provider rejection, got %v", err)
	}
}
