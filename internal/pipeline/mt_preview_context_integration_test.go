package pipeline_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// contextStub is an OpenAI-shaped provider that records each request's user
// message and answers every segment with a fixed translation.
func contextStub(t *testing.T) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var last []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		var segs []map[string]any
		for _, m := range req.Messages {
			if m.Role == "user" {
				if err := json.Unmarshal([]byte(m.Content), &segs); err != nil {
					t.Errorf("user message is not a segment array: %v", err)
				}
			}
		}
		mu.Lock()
		last = segs
		mu.Unlock()
		results := make([]map[string]any, len(segs))
		for i, s := range segs {
			results[i] = map[string]any{"id": s["id"], "target": "Voir les détails."}
		}
		content, _ := json.Marshal(map[string]any{"results": results})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": string(content)}}},
			"usage":   map[string]int{"prompt_tokens": stubPromptTokens, "completion_tokens": stubCompletionTokens},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

// taggedHelloWorld is the env segment's source with an inline tag; its plain
// source is "Hello world".
const taggedHelloWorld = `<g id="1" ctype="bold">Hello</g> world`

// newTMContextEnv is the usage env with an AI MT engine whose prompt asks for
// the TM match, and a TM holding the env segment's source. A non-empty
// sourceRichText tags the segment.
func newTMContextEnv(t *testing.T, sourceRichText string) (*aiUsageEnv, func() []map[string]any) {
	t.Helper()
	srv, lastSegments := contextStub(t)
	e := newAIUsageEnv(t, srv.URL)
	ctx := context.Background()
	engine := e.engine(t, "GPT translator", "mt", false)
	if _, err := e.pool.Exec(ctx,
		`UPDATE ai_prompts SET segment_context = '{tm_match}' WHERE tenant_id = $1`, e.tenant.ID); err != nil {
		t.Fatalf("set segment_context: %v", err)
	}
	if sourceRichText != "" {
		if _, err := e.pool.Exec(ctx,
			`UPDATE segments SET source_rich_text = $1 WHERE file_id = $2`, sourceRichText, e.file.ID); err != nil {
			t.Fatalf("tag the segment: %v", err)
		}
	}
	testutil.SeedMTRouterForProfile(t, e.q, e.tenant.ID, e.project.SettingsProfileID, nil, &engine.ID)
	tm := testutil.SeedTMWithLangs(t, e.q, e.tenant.ID, "Main TM", "en", []string{"fr"})
	testutil.SeedTMAssignment(t, e.q, e.project.SettingsProfileID, tm.ID, "read_write", 0)
	testutil.SeedTMEntry(t, e.q, tm.ID, "en", "fr", "Hello world", "Bonjour le monde")
	return e, lastSegments
}

// assertSentTMMatch checks the one segment sent carries the TM entry with
// score 100, and — for a tagged segment — that the model read the chip labels,
// so the score proves the lookup matched on the plain source.
func assertSentTMMatch(t *testing.T, segs []map[string]any, tagged bool) {
	t.Helper()
	if len(segs) != 1 {
		t.Fatalf("want one segment sent, got %v", segs)
	}
	if source, _ := segs[0]["source"].(string); tagged != strings.Contains(source, "<1>") {
		t.Fatalf("source sent = %q, want chip labels only for a tagged segment", source)
	}
	matches, ok := segs[0]["tm_matches"].([]any)
	if !ok || len(matches) != 1 {
		t.Fatalf("the request must carry the TM match the prompt asks for, sent: %v", segs[0])
	}
	match, _ := matches[0].(map[string]any)
	if match["source"] != "Hello world" || match["target"] != "Bonjour le monde" || match["score"] != float64(100) {
		t.Errorf("tm_matches[0] = %v, want the TM entry with score 100", match)
	}
}

// The editor's MT suggestion sends the same per-segment context as a pipeline
// run: a prompt that asks for the TM match gets it, with its score. A tagged
// segment matches on its plain source, so the chip labels do not lower the
// score. Its tokens count toward the project, like a pipeline run's.
func TestIntegrationMTPreview_SendsPromptContext(t *testing.T) {
	for _, tc := range []struct{ name, rich string }{
		{"plain segment", ""},
		{"tagged segment", taggedHelloWorld},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, lastSegments := newTMContextEnv(t, tc.rich)

			out, err := e.svc.MTPreview(context.Background(), e.tenant.ID, e.file.ID, "en", "fr", "Hello world", tc.rich, e.tr.ID)
			if err != nil {
				t.Fatalf("MTPreview: %v", err)
			}
			if out.Text != "Voir les détails." {
				t.Fatalf("preview text = %q, want the stub's translation", out.Text)
			}
			assertSentTMMatch(t, lastSegments(), tc.rich != "")

			usage := e.usageRows(t)
			if len(usage) != 1 || usage[0].projectID == nil || *usage[0].projectID != e.project.ID {
				t.Errorf("preview usage = %+v, want one row on project %d", usage, e.project.ID)
			}
		})
	}
}

// A pipeline MT run looks a tagged segment's TM match up by its plain source,
// while the model reads the chip labels.
func TestIntegrationTranslateFile_TaggedSegmentTMMatchUsesPlainSource(t *testing.T) {
	e, lastSegments := newTMContextEnv(t, taggedHelloWorld)

	if _, err := e.svc.TranslateFile(context.Background(), e.tenant.ID, e.file.ID, "en", "fr", nil,
		pipeline.TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	assertSentTMMatch(t, lastSegments(), true)
}
