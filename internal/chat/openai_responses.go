package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"openloc/internal/ai"
)

// OpenAI Responses API. Newer OpenAI models reason by
// default and refuse function tools with reasoning on /v1/chat/completions;
// /v1/responses takes both, and serves the pro models Chat Completions does
// not. Used for api.openai.com and Azure; other custom endpoints (gateways)
// keep Chat Completions, which they commonly implement and this may not be.

// usesResponsesAPI reports whether an endpoint gets the Responses API.
func usesResponsesAPI(baseURL string) bool {
	return baseURL == defaultOpenAIBaseURL || ai.IsAzureEndpoint(baseURL)
}

// rsEvent is one Responses stream event; only the fields the loop reads.
type rsEvent struct {
	Type   string `json:"type"`
	Delta  string `json:"delta"`
	ItemID string `json:"item_id"`
	Item   *struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"item"`
	Response *struct {
		Status string `json:"status"`
		Usage  *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

func (o *openaiLLM) streamResponses(ctx context.Context, req Request, onDelta func(string)) (*Turn, error) {
	payload := map[string]any{
		"model":  o.model,
		"input":  toResponsesInput(req.Messages),
		"stream": true,
		// The loop replays history itself; nothing is kept on OpenAI's side.
		"store": false,
	}
	if sys := req.FullSystem(); sys != "" {
		payload["instructions"] = sys
	}
	if len(req.Tools) > 0 {
		payload["tools"] = toResponsesTools(req.Tools)
	}
	if req.MaxTokens > 0 {
		payload["max_output_tokens"] = req.MaxTokens
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	ai.SetOpenAIAuth(httpReq, o.apiKey, o.baseURL)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, classifyOpenAIErr(0, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(resp.Body); err != nil {
			return nil, classifyOpenAIErr(resp.StatusCode, err.Error())
		}
		return nil, classifyOpenAIErr(resp.StatusCode, buf.String())
	}
	return readResponsesStream(resp, onDelta)
}

// readResponsesStream forwards text deltas and assembles function calls:
// output_item.added names a call, argument deltas arrive keyed by item id,
// and output_item.done carries the final arguments. A stream that ends
// before response.completed or response.incomplete was cut off (a dropped
// connection or proxy), so it is an Unreachable error, never a finished turn.
func readResponsesStream(resp *http.Response, onDelta func(string)) (*Turn, error) {
	turn := &Turn{}
	terminal := false
	var text strings.Builder
	calls := map[string]*ToolCall{} // item id → call
	args := map[string]*strings.Builder{}
	var order []string

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev rsEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			text.WriteString(ev.Delta)
			if onDelta != nil && ev.Delta != "" {
				onDelta(ev.Delta)
			}
		case "response.output_item.added":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				calls[ev.Item.ID] = &ToolCall{ID: ev.Item.CallID, Name: ev.Item.Name}
				args[ev.Item.ID] = &strings.Builder{}
				order = append(order, ev.Item.ID)
			}
		case "response.function_call_arguments.delta":
			if b, ok := args[ev.ItemID]; ok {
				b.WriteString(ev.Delta)
			}
		case "response.output_item.done":
			// The finished item carries the full arguments; prefer them over
			// the joined deltas.
			if ev.Item != nil && ev.Item.Type == "function_call" {
				if c, ok := calls[ev.Item.ID]; ok && ev.Item.Arguments != "" {
					args[ev.Item.ID].Reset()
					args[ev.Item.ID].WriteString(ev.Item.Arguments)
					if ev.Item.CallID != "" {
						c.ID = ev.Item.CallID
					}
				}
			}
		case "response.completed", "response.incomplete":
			terminal = true
			if ev.Response != nil && ev.Response.Usage != nil {
				turn.InputTokens = ev.Response.Usage.InputTokens
				turn.OutputTokens = ev.Response.Usage.OutputTokens
			}
			if ev.Type == "response.incomplete" {
				turn.StopReason = "max_tokens"
				if ev.Response != nil && ev.Response.IncompleteDetails != nil {
					turn.StopReason = ev.Response.IncompleteDetails.Reason
				}
			}
		case "response.failed":
			msg := "the response failed"
			if ev.Response != nil && ev.Response.Error != nil {
				msg = ev.Response.Error.Message
			}
			return nil, classifyOpenAIErr(0, msg)
		case "error":
			return nil, classifyOpenAIErr(0, strings.TrimSpace(ev.Code+" "+ev.Message))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, classifyOpenAIErr(0, "read stream: "+err.Error())
	}
	if !terminal {
		return nil, classifyOpenAIErr(0, "the stream ended before the response completed")
	}
	turn.Text = text.String()
	for _, id := range order {
		c := calls[id]
		a := args[id].String()
		if a == "" {
			a = "{}"
		}
		c.Args = json.RawMessage(a)
		turn.ToolCalls = append(turn.ToolCalls, *c)
	}
	if len(turn.ToolCalls) > 0 {
		turn.StopReason = StopToolUse
	} else if turn.StopReason == "" {
		turn.StopReason = "end_turn"
	}
	return turn, nil
}

// toResponsesInput flattens provider-neutral history into Responses input
// items. A call goes back as a function_call item without its item id (with
// store=false OpenAI does not look it up), a result as function_call_output.
func toResponsesInput(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		if len(m.ToolResults) > 0 {
			for _, tr := range m.ToolResults {
				out = append(out, map[string]any{"type": "function_call_output", "call_id": tr.ID, "output": tr.Content})
			}
			continue
		}
		switch {
		case m.Image != nil:
			out = append(out, map[string]any{"role": m.Role, "content": []map[string]any{
				{"type": "input_text", "text": m.Text},
				{"type": "input_image", "image_url": m.Image.DataURL()},
			}})
		case m.Text != "":
			out = append(out, map[string]any{"role": m.Role, "content": m.Text})
		}
		for _, c := range m.ToolCalls {
			a := string(c.Args)
			if a == "" {
				a = "{}"
			}
			out = append(out, map[string]any{"type": "function_call", "call_id": c.ID, "name": c.Name, "arguments": a})
		}
	}
	return out
}

func toResponsesTools(defs []ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		schema := json.RawMessage(d.InputSchema)
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, map[string]any{"type": "function", "name": d.Name, "description": d.Description, "parameters": schema})
	}
	return out
}
