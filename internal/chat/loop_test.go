package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeLLM returns scripted turns in order, capturing each request.
type fakeLLM struct {
	turns    []*Turn
	requests []Request
	deltas   []string // streamed before each corresponding turn
}

func (f *fakeLLM) Stream(_ context.Context, req Request, onDelta func(string)) (*Turn, error) {
	i := len(f.requests)
	f.requests = append(f.requests, req)
	if i < len(f.deltas) && f.deltas[i] != "" {
		onDelta(f.deltas[i])
	}
	if i >= len(f.turns) {
		// Script exhausted: a well-behaved test never gets here, but a loop
		// bug (e.g. ignoring stop reasons) would — end the turn instead of
		// panicking so the failure reads as an assertion, not a crash.
		return &Turn{Text: "(script exhausted)", StopReason: "end_turn"}, nil
	}
	return f.turns[i], nil
}

// fakeSession is a scriptable toolSession.
type fakeSession struct {
	tools   []*sdk.Tool
	results map[string]*sdk.CallToolResult
	calls   []string
}

func (f *fakeSession) ListTools(context.Context, *sdk.ListToolsParams) (*sdk.ListToolsResult, error) {
	return &sdk.ListToolsResult{Tools: f.tools}, nil
}

func (f *fakeSession) CallTool(_ context.Context, p *sdk.CallToolParams) (*sdk.CallToolResult, error) {
	f.calls = append(f.calls, p.Name)
	if r, ok := f.results[p.Name]; ok {
		return r, nil
	}
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: "unknown tool"}},
		IsError: true,
	}, nil
}

func textResult(s string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: s}}}
}

func newFakeSession() *fakeSession {
	schema := json.RawMessage(`{"type":"object","properties":{"project_id":{"type":"integer"}}}`)
	var toolSchema any
	_ = json.Unmarshal(schema, &toolSchema)
	return &fakeSession{
		tools: []*sdk.Tool{{
			Name:        "openloc_list_jobs",
			Description: "List jobs",
			InputSchema: toolSchema,
		}},
		results: map[string]*sdk.CallToolResult{
			"openloc_list_jobs": textResult(`{"jobs":[{"id":7,"status":"in_progress"}]}`),
		},
	}
}

// Navigate_to is a chat-layer tool — offered to the model, executed
// as a client_action event, NEVER sent to the MCP session.
func TestRunNavigateIsClientSideAndFireAndForget(t *testing.T) {
	llm := &fakeLLM{
		turns: []*Turn{
			{
				StopReason: StopToolUse,
				ToolCalls: []ToolCall{{
					ID: "nav1", Name: navigateToolName,
					Args: json.RawMessage(`{"page":"job_editor","project_id":42,"job_id":7}`),
				}},
			},
			{Text: "I've opened the editor.", StopReason: "end_turn"},
		},
	}
	session := newFakeSession()
	var events []Event
	res, err := Run(context.Background(), llm, session, SystemPrompt{Stable: "sys"}, nil, RunOptions{AllowWrites: true, SavedTours: true}, func(e Event) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The MCP session never saw the call.
	if len(session.calls) != 0 {
		t.Fatalf("navigate leaked to the MCP session: %v", session.calls)
	}
	// The client action carried the SERVER-built path.
	var nav *Event
	for i := range events {
		if events[i].Type == "client_action" {
			nav = &events[i]
		}
	}
	if nav == nil || nav.Action != "navigate" || nav.Path != "/projects/42/jobs/7/edit" {
		t.Fatalf("client_action wrong or missing: %+v", nav)
	}
	// Fire-and-forget: the model got an immediate success result.
	second := llm.requests[1]
	tr := second.Messages[len(second.Messages)-1].ToolResults[0]
	if tr.IsError || !strings.Contains(tr.Content, "navigation to /projects/42/jobs/7/edit") {
		t.Fatalf("synthesized result wrong: %+v", tr)
	}
	if res.Text != "I've opened the editor." {
		t.Fatalf("text = %q", res.Text)
	}

	// The tool was OFFERED alongside MCP tools.
	var offered bool
	for _, d := range llm.requests[0].Tools {
		if d.Name == navigateToolName {
			offered = true
		}
	}
	if !offered {
		t.Fatal("navigate_to not offered to the model")
	}
}

func TestRunNavigateRejectsUnknownPageModelVisibly(t *testing.T) {
	llm := &fakeLLM{
		turns: []*Turn{
			{
				StopReason: StopToolUse,
				ToolCalls: []ToolCall{{
					ID: "nav1", Name: navigateToolName,
					Args: json.RawMessage(`{"page":"platform_orgs"}`),
				}},
			},
			{Text: "Sorry, I can't open that.", StopReason: "end_turn"},
		},
	}
	var events []Event
	_, err := Run(context.Background(), llm, newFakeSession(), SystemPrompt{Stable: "sys"}, nil, RunOptions{AllowWrites: true, SavedTours: true}, func(e Event) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, e := range events {
		if e.Type == "client_action" {
			t.Fatalf("non-whitelisted page emitted a client action: %+v", e)
		}
	}
	tr := llm.requests[1].Messages[len(llm.requests[1].Messages)-1].ToolResults[0]
	if !tr.IsError || !strings.Contains(tr.Content, "unknown page") {
		t.Fatalf("model should see the whitelist rejection: %+v", tr)
	}
}

func TestRunPlainAnswerNoTools(t *testing.T) {
	llm := &fakeLLM{
		turns:  []*Turn{{Text: "Two jobs are overdue.", StopReason: "end_turn", InputTokens: 10, OutputTokens: 5}},
		deltas: []string{"Two jobs are overdue."},
	}
	var events []Event
	res, err := Run(context.Background(), llm, newFakeSession(), SystemPrompt{Stable: "sys"}, []Message{{Role: "user", Text: "hi"}}, RunOptions{AllowWrites: true, SavedTours: true}, func(e Event) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text != "Two jobs are overdue." {
		t.Fatalf("text = %q", res.Text)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("model called %d times, want 1", len(llm.requests))
	}
	// MCP tools must have been offered even though none were used (the
	// chat-layer navigate_to also rides along).
	var sawListJobs bool
	for _, d := range llm.requests[0].Tools {
		if d.Name == "openloc_list_jobs" {
			sawListJobs = true
		}
	}
	if !sawListJobs {
		t.Fatalf("MCP tools not offered to model: %+v", llm.requests[0].Tools)
	}
	if res.InputTokens != 10 || res.OutputTokens != 5 {
		t.Fatalf("usage not aggregated: %+v", res)
	}
}

func TestRunToolRoundTrip(t *testing.T) {
	llm := &fakeLLM{
		turns: []*Turn{
			{
				StopReason:  StopToolUse,
				ToolCalls:   []ToolCall{{ID: "tc1", Name: "openloc_list_jobs", Args: json.RawMessage(`{"project_id":1}`)}},
				InputTokens: 20, OutputTokens: 8,
			},
			{Text: "Job 7 is in progress.", StopReason: "end_turn", InputTokens: 30, OutputTokens: 6},
		},
	}
	session := newFakeSession()
	var events []Event
	res, err := Run(context.Background(), llm, session, SystemPrompt{Stable: "sys"}, []Message{{Role: "user", Text: "status?"}}, RunOptions{AllowWrites: true, SavedTours: true}, func(e Event) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text != "Job 7 is in progress." {
		t.Fatalf("text = %q", res.Text)
	}
	if len(session.calls) != 1 || session.calls[0] != "openloc_list_jobs" {
		t.Fatalf("tool calls = %v", session.calls)
	}

	// Second model call must carry the tool result back.
	second := llm.requests[1]
	last := second.Messages[len(second.Messages)-1]
	if len(last.ToolResults) != 1 || last.ToolResults[0].ID != "tc1" {
		t.Fatalf("tool result not threaded back: %+v", last)
	}
	if !strings.Contains(last.ToolResults[0].Content, `"id":7`) {
		t.Fatalf("tool result content lost: %q", last.ToolResults[0].Content)
	}

	// Events: tool_start then tool_end, ok=true.
	var sawStart, sawEnd bool
	for _, e := range events {
		if e.Type == "tool_start" && e.Tool == "openloc_list_jobs" {
			sawStart = true
		}
		if e.Type == "tool_end" && e.Tool == "openloc_list_jobs" && e.OK {
			sawEnd = true
		}
	}
	if !sawStart || !sawEnd {
		t.Fatalf("missing tool lifecycle events: %+v", events)
	}

	// Usage aggregates across iterations.
	if res.InputTokens != 50 || res.OutputTokens != 14 {
		t.Fatalf("usage = %d/%d, want 50/14", res.InputTokens, res.OutputTokens)
	}
	if len(res.ToolCalls) != 1 || !res.ToolCalls[0].OK {
		t.Fatalf("tool summary = %+v", res.ToolCalls)
	}
}

func TestRunToolErrorIsModelVisibleNotFatal(t *testing.T) {
	llm := &fakeLLM{
		turns: []*Turn{
			{StopReason: StopToolUse, ToolCalls: []ToolCall{{ID: "tc1", Name: "openloc_get_job", Args: json.RawMessage(`{}`)}}},
			{Text: "That job doesn't exist.", StopReason: "end_turn"},
		},
	}
	session := newFakeSession() // openloc_get_job → IsError result
	res, err := Run(context.Background(), llm, session, SystemPrompt{Stable: "sys"}, nil, RunOptions{AllowWrites: true, SavedTours: true}, func(Event) {})
	if err != nil {
		t.Fatalf("tool error must not fail the turn: %v", err)
	}
	if res.Text != "That job doesn't exist." {
		t.Fatalf("text = %q", res.Text)
	}
	last := llm.requests[1].Messages[len(llm.requests[1].Messages)-1]
	if !last.ToolResults[0].IsError {
		t.Fatal("error result not marked IsError for the model")
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].OK {
		t.Fatalf("summary should record failure: %+v", res.ToolCalls)
	}
}

func TestRunIterationCap(t *testing.T) {
	// A model that never stops asking for tools must be cut off at
	// maxIterations, with the truncation marker appended.
	turns := make([]*Turn, maxIterations+5)
	for i := range turns {
		turns[i] = &Turn{
			StopReason: StopToolUse,
			ToolCalls:  []ToolCall{{ID: "x", Name: "openloc_list_jobs", Args: json.RawMessage(`{}`)}},
		}
	}
	llm := &fakeLLM{turns: turns}
	session := newFakeSession()
	res, err := Run(context.Background(), llm, session, SystemPrompt{Stable: "sys"}, nil, RunOptions{AllowWrites: true, SavedTours: true}, func(Event) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(llm.requests) != maxIterations {
		t.Fatalf("model called %d times, want exactly %d", len(llm.requests), maxIterations)
	}
	if !strings.Contains(res.Text, "stopped: too many tool calls") {
		t.Fatalf("missing honest truncation marker: %q", res.Text)
	}
}

func TestRunTruncatesHugeToolResult(t *testing.T) {
	big := strings.Repeat("x", maxToolResultChars+1000)
	session := newFakeSession()
	session.results["openloc_list_jobs"] = textResult(big)
	llm := &fakeLLM{
		turns: []*Turn{
			{StopReason: StopToolUse, ToolCalls: []ToolCall{{ID: "tc1", Name: "openloc_list_jobs", Args: json.RawMessage(`{}`)}}},
			{Text: "done", StopReason: "end_turn"},
		},
	}
	if _, err := Run(context.Background(), llm, session, SystemPrompt{Stable: "sys"}, nil, RunOptions{AllowWrites: true, SavedTours: true}, func(Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	content := llm.requests[1].Messages[len(llm.requests[1].Messages)-1].ToolResults[0].Content
	if len(content) > maxToolResultChars+100 {
		t.Fatalf("tool result not truncated: %d chars", len(content))
	}
	if !strings.Contains(content, "truncated") {
		t.Fatal("truncation not disclosed to the model")
	}
}

// Render_chart is a chat-layer tool. The loop validates
// the spec, emits it as a client_action carrying the normalized JSON, and
// synthesizes success — the MCP session never sees it. A bad spec bounces
// back to the model as a tool error instead of emitting anything.
func TestRunRenderChartIsClientSideAndValidated(t *testing.T) {
	spec := `{"title":"MT usage","type":"bar","x_key":"mt_engine","series":["words"],"data":[{"mt_engine":"DeepL","words":120},{"mt_engine":"Claude","words":80}]}`
	llm := &fakeLLM{
		turns: []*Turn{
			{
				StopReason: StopToolUse,
				ToolCalls: []ToolCall{
					{ID: "c1", Name: chartToolName, Args: json.RawMessage(`{"type":"donut","x_key":"x","series":["y"],"data":[{"x":1}]}`)},
					{ID: "c2", Name: chartToolName, Args: json.RawMessage(spec)},
				},
			},
			{Text: "Here's the chart.", StopReason: "end_turn"},
		},
	}
	session := newFakeSession()
	var events []Event
	if _, err := Run(context.Background(), llm, session, SystemPrompt{Stable: "sys"}, nil, RunOptions{AllowWrites: true, SavedTours: true}, func(e Event) {
		events = append(events, e)
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(session.calls) != 0 {
		t.Fatalf("render_chart leaked to the MCP session: %v", session.calls)
	}

	// Exactly ONE client action (the invalid donut emitted nothing) and its
	// Chart payload round-trips the series/data the model sent.
	var charts []Event
	for _, e := range events {
		if e.Type == "client_action" && e.Action == "render_chart" {
			charts = append(charts, e)
		}
	}
	if len(charts) != 1 {
		t.Fatalf("want 1 chart client_action, got %d: %+v", len(charts), events)
	}
	var got struct {
		Type   string           `json:"type"`
		XKey   string           `json:"x_key"`
		Series []string         `json:"series"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(charts[0].Chart, &got); err != nil {
		t.Fatalf("chart payload: %v", err)
	}
	if got.Type != "bar" || got.XKey != "mt_engine" || len(got.Data) != 2 {
		t.Fatalf("chart spec mangled: %+v", got)
	}

	// The invalid call fed an error result back to the model; the valid one
	// synthesized success.
	second := llm.requests[1]
	results := second.Messages[len(second.Messages)-1].ToolResults
	if len(results) != 2 {
		t.Fatalf("tool results: %+v", results)
	}
	if !results[0].IsError || !strings.Contains(results[0].Content, "unknown chart type") {
		t.Fatalf("invalid spec not bounced to model: %+v", results[0])
	}
	if results[1].IsError || !strings.Contains(results[1].Content, "chart rendered") {
		t.Fatalf("valid spec not acknowledged: %+v", results[1])
	}
}

// The model never receives a confirm token, so one in its arguments is a guess
// or an injection. It is refused before it reaches the MCP session.
func TestRunRefusesModelSuppliedConfirmToken(t *testing.T) {
	llm := &fakeLLM{turns: []*Turn{
		{StopReason: StopToolUse, ToolCalls: []ToolCall{{
			ID: "d1", Name: "openloc_delete_custom_palette",
			Args: json.RawMessage(`{"id":"x","confirm_token":"123.abc"}`),
		}}},
		{Text: "ok", StopReason: "end_turn"},
	}}
	session := newFakeSession()
	if _, err := Run(context.Background(), llm, session, SystemPrompt{Stable: "sys"}, nil, RunOptions{AllowWrites: true, SavedTours: true}, func(Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.calls) != 0 {
		t.Fatalf("a model-supplied confirm token reached MCP: %v", session.calls)
	}
	tr := llm.requests[1].Messages[len(llm.requests[1].Messages)-1].ToolResults[0]
	if !tr.IsError || !strings.Contains(tr.Content, "confirmation card") {
		t.Errorf("want a model-visible refusal, got %+v", tr)
	}
}

// Every chat-layer tool must reach the provider. The Anthropic adapter drops a
// tool whose schema does not parse, so an invalid schema silently hides the
// tool from the model.
func TestChatLayerToolsReachTheProvider(t *testing.T) {
	llm := &fakeLLM{turns: []*Turn{{Text: "ok", StopReason: "end_turn"}}}
	if _, err := Run(context.Background(), llm, newFakeSession(), SystemPrompt{Stable: "sys"}, nil, RunOptions{}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	offered := map[string]ToolDef{}
	for _, d := range llm.requests[0].Tools {
		offered[d.Name] = d
	}
	sent := map[string]bool{}
	for _, tool := range toAnthropicTools(llm.requests[0].Tools) {
		sent[tool.OfTool.Name] = true
	}
	for _, name := range []string{navigateToolName, chartToolName, highlightToolName, tourToolName, getPlaybookToolName, repliesToolName} {
		d, ok := offered[name]
		if !ok {
			t.Errorf("%s is not offered to the model", name)
			continue
		}
		if !json.Valid(d.InputSchema) {
			t.Errorf("%s has an invalid JSON schema", name)
		}
		if !sent[name] {
			t.Errorf("%s does not reach the Anthropic request", name)
		}
	}
}
