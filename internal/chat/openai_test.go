package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/ai"
)

// sseServer replays canned SSE frames and captures the request the adapter sent.
func sseServer(t *testing.T, frames []string) (*httptest.Server, *http.Request, *map[string]any) {
	t.Helper()
	var gotReq *http.Request
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r.Clone(r.Context())
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, f := range frames {
			_, _ = w.Write([]byte("data: " + f + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, gotReq, &body
}

func TestOpenAIStream_TextAndUsage(t *testing.T) {
	srv, _, _ := sseServer(t, []string{
		`{"choices":[{"delta":{"content":"Hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":4}}`,
	})
	var deltas []string
	turn, err := NewOpenAI("sk-x", srv.URL, "gpt-4o").
		Stream(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, func(d string) {
			deltas = append(deltas, d)
		})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if turn.Text != "Hello" {
		t.Errorf("text = %q, want Hello", turn.Text)
	}
	// Deltas must arrive incrementally — the panel streams them.
	if len(deltas) != 2 || deltas[0] != "Hel" {
		t.Errorf("deltas = %v, want incremental fragments", deltas)
	}
	if turn.InputTokens != 11 || turn.OutputTokens != 4 {
		t.Errorf("usage = %d/%d, want 11/4", turn.InputTokens, turn.OutputTokens)
	}
	if turn.StopReason == StopToolUse {
		t.Errorf("stop reason should not be tool_use for a plain answer")
	}
}

// The fiddly part of the protocol: one tool call's arguments arrive as several
// fragments keyed by index, and only the first fragment carries id + name.
// Joining them wrongly yields unparseable JSON and a dead agent loop.
func TestOpenAIStream_ToolCallFragmentsAccumulate(t *testing.T) {
	srv, _, _ := sseServer(t, []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"openloc_list_projects","arguments":"{\"lim"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"it\":5}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	})
	turn, err := NewOpenAI("sk-x", srv.URL, "gpt-4o").
		Stream(context.Background(), Request{Messages: []Message{{Role: "user", Text: "go"}}}, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if turn.StopReason != StopToolUse {
		t.Fatalf("stop reason = %q, want %q", turn.StopReason, StopToolUse)
	}
	if len(turn.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(turn.ToolCalls))
	}
	c := turn.ToolCalls[0]
	if c.ID != "call_1" || c.Name != "openloc_list_projects" {
		t.Errorf("call identity = %q/%q", c.ID, c.Name)
	}
	// The whole point: the two fragments must join into valid JSON.
	var args struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(c.Args, &args); err != nil {
		t.Fatalf("args %q not valid JSON: %v", c.Args, err)
	}
	if args.Limit != 5 {
		t.Errorf("limit = %d, want 5", args.Limit)
	}
}

func TestOpenAIStream_ParallelToolCallsKeepOrder(t *testing.T) {
	srv, _, _ := sseServer(t, []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"first","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"second","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	})
	turn, err := NewOpenAI("sk-x", srv.URL, "gpt-4o").
		Stream(context.Background(), Request{Messages: []Message{{Role: "user", Text: "go"}}}, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(turn.ToolCalls) != 2 || turn.ToolCalls[0].Name != "first" || turn.ToolCalls[1].Name != "second" {
		t.Fatalf("calls = %+v, want first then second", turn.ToolCalls)
	}
}

// Tool results are their own `role:"tool"` messages in this dialect — nesting
// them in a user message (Anthropic's shape) is silently accepted by the JSON
// encoder and rejected by the provider.
func TestToOpenAIMessages_ToolRoundTrip(t *testing.T) {
	msgs := toOpenAIMessages("sys", []Message{
		{Role: "user", Text: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "t", Args: json.RawMessage(`{"a":1}`)}}},
		{Role: "user", ToolResults: []ToolResult{{ID: "c1", Content: "done"}}},
	})
	roles := make([]string, len(msgs))
	for i, m := range msgs {
		roles[i] = m["role"].(string)
	}
	want := []string{"system", "user", "assistant", "tool"}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	if msgs[3]["tool_call_id"] != "c1" {
		t.Errorf("tool result must carry tool_call_id, got %v", msgs[3])
	}
	if _, hasContent := msgs[2]["content"]; hasContent {
		t.Errorf("a text-less assistant tool call must not send an empty content field")
	}
}

// GPT-5 rejects max_tokens outright; sending it 400s every request.
func TestOpenAIStream_ReasoningModelUsesCompletionTokens(t *testing.T) {
	for _, tc := range []struct{ model, field, other string }{
		{"gpt-5", "max_completion_tokens", "max_tokens"},
		{"gpt-4o", "max_tokens", "max_completion_tokens"},
	} {
		srv, _, body := sseServer(t, []string{`{"choices":[{"delta":{"content":"x"},"finish_reason":"stop"}]}`})
		if _, err := NewOpenAI("sk-x", srv.URL, tc.model).
			Stream(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}, MaxTokens: 256}, nil); err != nil {
			t.Fatalf("%s: %v", tc.model, err)
		}
		if _, ok := (*body)[tc.field]; !ok {
			t.Errorf("%s: request should carry %s, got keys %v", tc.model, tc.field, keysOf(*body))
		}
		if _, ok := (*body)[tc.other]; ok {
			t.Errorf("%s: request must not carry %s, got keys %v", tc.model, tc.other, keysOf(*body))
		}
	}
}

func TestOpenAIStream_AuthErrorIsClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key"}}`))
	}))
	defer srv.Close()
	_, err := NewOpenAI("bad", srv.URL, "gpt-4o").
		Stream(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	var pe *ai.ProviderError
	if !errorsAs(err, &pe) {
		t.Fatalf("want *ai.ProviderError, got %T (%v)", err, err)
	}
	// The handler maps this kind to "your key was rejected" — a generic error
	// would surface as an opaque blob instead.
	if pe.Kind != ai.ProviderErrAuth {
		t.Errorf("kind = %q, want %q", pe.Kind, ai.ProviderErrAuth)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func errorsAs(err error, target **ai.ProviderError) bool {
	pe, ok := err.(*ai.ProviderError)
	if ok {
		*target = pe
	}
	return ok
}
