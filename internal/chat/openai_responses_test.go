package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/ai"
)

// responsesServer serves Responses-API SSE frames and records the request.
func responsesServer(t *testing.T, status int, frames []string) (*httptest.Server, *string, *map[string]any) {
	t.Helper()
	path := ""
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(frames[0]))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			_, _ = w.Write([]byte("data: " + f + "\n\n"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &path, &body
}

func responsesLLM(baseURL, model string) *openaiLLM {
	l := NewOpenAI("sk-x", baseURL, model).(*openaiLLM)
	l.responses = true
	return l
}

func TestResponsesAPIEndpointChoice(t *testing.T) {
	cases := map[string]bool{
		"":                                     true,
		"https://api.openai.com":               true,
		"https://acme.openai.azure.com/openai": true,
		"https://gateway.example.com":          false,
	}
	for base, want := range cases {
		if got := NewOpenAI("sk-x", base, "gpt-6-sol").(*openaiLLM).responses; got != want {
			t.Errorf("base %q: responses = %v, want %v", base, got, want)
		}
	}
}

func TestResponsesStream_TextToolCallsAndUsage(t *testing.T) {
	srv, path, body := responsesServer(t, http.StatusOK, []string{
		`{"type":"response.output_text.delta","delta":"Let me "}`,
		`{"type":"response.output_text.delta","delta":"check."}`,
		`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_A","name":"openloc_list_jobs"}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"proj"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"ect_id\":4}"}`,
		`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_2","call_id":"call_B","name":"highlight"}}`,
		`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_2","call_id":"call_B","name":"highlight","arguments":"{\"anchor\":\"projects.new\",\"caption\":\"Here.\"}"}}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":120,"output_tokens":30}}}`,
	})
	var deltas []string
	turn, err := responsesLLM(srv.URL, "gpt-6-sol").Stream(context.Background(), Request{
		System:    "sys",
		Messages:  []Message{{Role: "user", Text: "jobs?"}},
		Tools:     []ToolDef{{Name: "openloc_list_jobs", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxTokens: 500,
	}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if *path != "/v1/responses" {
		t.Errorf("path = %s, want /v1/responses", *path)
	}
	if turn.Text != "Let me check." || strings.Join(deltas, "") != "Let me check." {
		t.Errorf("text = %q, deltas = %v", turn.Text, deltas)
	}
	if turn.StopReason != StopToolUse || len(turn.ToolCalls) != 2 {
		t.Fatalf("turn = %+v", turn)
	}
	if c := turn.ToolCalls[0]; c.ID != "call_A" || c.Name != "openloc_list_jobs" || string(c.Args) != `{"project_id":4}` {
		t.Errorf("call 1 = %+v (args %s)", c, c.Args)
	}
	if c := turn.ToolCalls[1]; c.ID != "call_B" || !strings.Contains(string(c.Args), "projects.new") {
		t.Errorf("call 2 = %+v (args %s)", c, c.Args)
	}
	if turn.InputTokens != 120 || turn.OutputTokens != 30 {
		t.Errorf("usage = %d/%d", turn.InputTokens, turn.OutputTokens)
	}

	// The request is Responses-shaped: instructions, flat tools, output cap,
	// nothing stored, and no Chat Completions fields.
	b := *body
	if b["instructions"] != "sys" || b["store"] != false || b["max_output_tokens"] != float64(500) {
		t.Errorf("request = %v", b)
	}
	tools, _ := b["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "openloc_list_jobs" {
		t.Errorf("tools = %v", b["tools"])
	}
	for _, chatOnly := range []string{"messages", "max_tokens", "max_completion_tokens", "stream_options", "reasoning_effort"} {
		if _, ok := b[chatOnly]; ok {
			t.Errorf("Chat Completions field %q sent to /v1/responses", chatOnly)
		}
	}
}

func TestResponsesInput_ToolRoundTrip(t *testing.T) {
	items := toResponsesInput([]Message{
		{Role: "user", Text: "jobs?"},
		{Role: "assistant", Text: "Checking.", ToolCalls: []ToolCall{{ID: "call_A", Name: "openloc_list_jobs", Args: json.RawMessage(`{"project_id":4}`)}}},
		{Role: "user", ToolResults: []ToolResult{{ID: "call_A", Content: `{"jobs":[]}`}}},
	})
	raw, _ := json.Marshal(items)
	want := `[{"content":"jobs?","role":"user"},{"content":"Checking.","role":"assistant"},` +
		`{"arguments":"{\"project_id\":4}","call_id":"call_A","name":"openloc_list_jobs","type":"function_call"},` +
		`{"call_id":"call_A","output":"{\"jobs\":[]}","type":"function_call_output"}]`
	if string(raw) != want {
		t.Errorf("input =\n%s\nwant\n%s", raw, want)
	}
}

func TestResponsesStream_FailuresAreProviderErrors(t *testing.T) {
	srv, _, _ := responsesServer(t, http.StatusOK, []string{
		`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"The model failed."}}}`,
	})
	if _, err := responsesLLM(srv.URL, "gpt-6-sol").Stream(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil); err == nil || !strings.Contains(err.Error(), "The model failed.") {
		t.Errorf("response.failed: err = %v", err)
	}
	bad, _, _ := responsesServer(t, http.StatusUnauthorized, []string{`{"error":{"message":"Incorrect API key"}}`})
	if _, err := responsesLLM(bad.URL, "gpt-6-sol").Stream(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("401: err = %v", err)
	}
}

// A stream that closes before response.completed / response.incomplete is a
// cut-off answer, not a finished one: the caller must get an Unreachable
// provider error instead of partial text or half-written tool arguments.
func TestResponsesStream_EndsWithoutTerminalEventIsUnreachable(t *testing.T) {
	cases := map[string][]string{
		"mid-text": {
			`{"type":"response.output_text.delta","delta":"The answer is "}`,
		},
		"mid-tool-call": {
			`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"list_projects"}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"lim"}`,
		},
	}
	for name, frames := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _, _ := responsesServer(t, http.StatusOK, frames)
			turn, err := responsesLLM(srv.URL, "gpt-6-sol").Stream(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil)
			var perr *ai.ProviderError
			if !errors.As(err, &perr) || perr.Kind != ai.ProviderErrUnreachable {
				t.Fatalf("err = %v (turn %+v), want ProviderErrUnreachable", err, turn)
			}
			if turn != nil {
				t.Errorf("turn = %+v, want nil", turn)
			}
		})
	}
}
