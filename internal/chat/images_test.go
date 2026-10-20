package chat

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"openloc/internal/ai"
)

var testShot = &Image{MediaType: "image/png", Data: []byte("\x89PNG shot")}

// Each adapter sends the screenshot as its vendor's image part, beside the
// text of the same message.
func TestImagePartsPerAdapter(t *testing.T) {
	msgs := []Message{{Role: "user", Text: "Here it is.", Image: testShot}}

	raw, err := json.Marshal((&anthropicLLM{model: DefaultModel}).buildAnthropicParams(Request{Messages: msgs, MaxTokens: 10}))
	if err != nil {
		t.Fatal(err)
	}
	var claude struct {
		Messages []struct {
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Source struct {
					Type, MediaType, Data string
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &claude); err != nil {
		t.Fatal(err)
	}
	if c := claude.Messages[0].Content; len(c) != 2 || c[0].Type != "image" || c[0].Source.Type != "base64" ||
		c[0].Source.Data != testShot.Base64() || c[1].Text != "Here it is." {
		t.Errorf("anthropic content = %s", raw)
	}
	if !strings.Contains(string(raw), `"media_type":"image/png"`) {
		t.Errorf("anthropic image has no media type: %s", raw)
	}

	chat, _ := json.Marshal(toOpenAIMessages("", msgs))
	if !strings.Contains(string(chat), `{"image_url":{"url":"`+testShot.DataURL()+`"},"type":"image_url"}`) ||
		!strings.Contains(string(chat), `{"text":"Here it is.","type":"text"}`) {
		t.Errorf("chat completions content = %s", chat)
	}

	resp, _ := json.Marshal(toResponsesInput(msgs))
	if !strings.Contains(string(resp), `{"image_url":"`+testShot.DataURL()+`","type":"input_image"}`) ||
		!strings.Contains(string(resp), `{"text":"Here it is.","type":"input_text"}`) {
		t.Errorf("responses input = %s", resp)
	}
}

func TestOneImageKeepsTheNewest(t *testing.T) {
	older := &Image{MediaType: "image/png", Data: []byte("older")}
	got := oneImage([]Message{
		{Role: "user", Text: "first", Image: older},
		{Role: "assistant", Text: "ok"},
		{Role: "user", Text: "second", Image: testShot},
	})
	if got[0].Image != nil || got[2].Image != testShot || got[0].Text != "first" {
		t.Errorf("oneImage = %+v", got)
	}
}

// failingImageLLM refuses any request that carries an image, as a vendor
// does for a model without vision.
type failingImageLLM struct{ requests []Request }

func (f *failingImageLLM) Stream(_ context.Context, req Request, _ func(string)) (*Turn, error) {
	f.requests = append(f.requests, req)
	if hasImage(req.Messages) {
		return nil, &ai.ProviderError{Provider: ai.ProviderOpenAI, Status: 400, Kind: ai.ProviderErrProvider, Detail: "image input not supported"}
	}
	return &Turn{Text: "I cannot see it, but I noted it.", StopReason: "end_turn"}, nil
}

// A refused image is retried once with the text marker in its place.
func TestRunRetriesARefusedImageWithTheMarker(t *testing.T) {
	llm := &failingImageLLM{}
	res, err := Run(context.Background(), llm, newFakeSession(), SystemPrompt{Stable: "sys"},
		[]Message{{Role: "user", Text: "Here it is.", Image: testShot}}, RunOptions{}, func(Event) {})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(llm.requests) != 2 || hasImage(llm.requests[1].Messages) ||
		!strings.Contains(llm.requests[1].Messages[0].Text, imageRefusedNote) || res.Text == "" {
		t.Errorf("requests = %+v, result %q", llm.requests, res.Text)
	}

	// Any other failure is not retried.
	if imageRefused(&ai.ProviderError{Status: 401, Kind: ai.ProviderErrAuth}) ||
		imageRefused(&ai.ProviderError{Status: 429, Kind: ai.ProviderErrRateLimit}) ||
		imageRefused(&ai.ProviderError{Status: 500, Kind: ai.ProviderErrProvider}) {
		t.Error("a key, rate or server failure counts as an image refusal")
	}
}

// A pause tool the model calls where it is not offered shows no button.
func TestRunPauseToolRefusedOnSupportPage(t *testing.T) {
	llm := &fakeLLM{turns: []*Turn{
		{StopReason: StopToolUse, ToolCalls: []ToolCall{
			{ID: "a", Name: screenshotToolName, Args: json.RawMessage(`{"reason":"So the team sees it."}`)},
		}},
		{Text: "I can't take one here.", StopReason: "end_turn"},
	}}
	var actions []Event
	if _, err := Run(context.Background(), llm, newFakeSession(), SystemPrompt{Stable: "sys"},
		[]Message{{Role: "user", Text: "look at this report"}},
		RunOptions{IssueReports: true, PagePath: "/platform/issues/12"}, func(e Event) {
			if e.Type == "client_action" {
				actions = append(actions, e)
			}
		}); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 0 {
		t.Errorf("client actions on a support page = %+v", actions)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("model called %d times, want 2 (the refusal goes back to it)", len(llm.requests))
	}
	results := llm.requests[1].Messages[len(llm.requests[1].Messages)-1].ToolResults
	if len(results) != 1 || !results[0].IsError {
		t.Errorf("tool results = %+v, want one refusal", results)
	}
}

// A pause tool shows its button and ends the turn: the model is not called
// again, and a second request in the same message is refused.
func TestRunPauseToolEndsTheTurn(t *testing.T) {
	llm := &fakeLLM{turns: []*Turn{{
		Text:       "Could you add a screenshot?",
		StopReason: StopToolUse,
		ToolCalls: []ToolCall{
			{ID: "a", Name: screenshotToolName, Args: json.RawMessage(`{"reason":"So the team sees the empty grid."}`)},
			{ID: "b", Name: repeatToolName, Args: json.RawMessage(`{"reason":"Show it again."}`)},
		},
	}}}
	var actions []Event
	var refused []bool
	res, err := Run(context.Background(), llm, newFakeSession(), SystemPrompt{Stable: "sys"},
		[]Message{{Role: "user", Text: "the grid is empty"}}, RunOptions{IssueReports: true}, func(e Event) {
			if e.Type == "client_action" {
				actions = append(actions, e)
			}
			if e.Type == "tool_end" {
				refused = append(refused, !e.OK)
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.requests) != 1 {
		t.Errorf("the model was called %d times after the pause", len(llm.requests))
	}
	if len(actions) != 1 || actions[0].Action != "ask_screenshot" || actions[0].Reason != "So the team sees the empty grid." {
		t.Errorf("client actions = %+v", actions)
	}
	if len(refused) != 2 || refused[0] || !refused[1] {
		t.Errorf("tool outcomes (refused) = %v, want the second refused", refused)
	}
	if res.Text != "Could you add a screenshot?" {
		t.Errorf("text = %q", res.Text)
	}
}

func TestPauseToolsNeedIssueReports(t *testing.T) {
	names := func(opts RunOptions) []string {
		llm := &fakeLLM{turns: []*Turn{{Text: "hi", StopReason: "end_turn"}}}
		if _, err := Run(context.Background(), llm, newFakeSession(), SystemPrompt{Stable: "sys"}, nil,
			opts, func(Event) {}); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, d := range llm.requests[0].Tools {
			if isPauseTool(d.Name) {
				out = append(out, d.Name)
			}
		}
		return out
	}
	if got := names(RunOptions{}); len(got) != 0 {
		t.Errorf("offered without issue reports: %v", got)
	}
	// The browser's test reads the same cases, so both lists stay one list.
	raw, err := os.ReadFile("testdata/support_pages.json")
	if err != nil {
		t.Fatal(err)
	}
	var pages struct{ Support, Other []string }
	if err := json.Unmarshal(raw, &pages); err != nil {
		t.Fatal(err)
	}
	for _, path := range pages.Other {
		if got := names(RunOptions{IssueReports: true, PagePath: path}); len(got) != 2 {
			t.Errorf("not offered on %s: %v", path, got)
		}
	}
	for _, path := range pages.Support {
		if got := names(RunOptions{IssueReports: true, PagePath: path}); len(got) != 0 {
			t.Errorf("offered on %s: %v", path, got)
		}
	}
	for _, args := range []string{`{"reason":""}`, `{"reason":"` + strings.Repeat("x", maxPauseReason+1) + `"}`, `{`} {
		if _, isErr := executePause(screenshotToolName, json.RawMessage(args), false, func(Event) {}); !isErr {
			t.Errorf("accepted %s", args)
		}
	}
}
