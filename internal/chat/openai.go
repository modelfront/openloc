package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"openloc/internal/ai"
	"openloc/internal/netguard"
)

// openaiLLM adapts OpenAI to the LLM interface — the second adapter behind the
// LLM seam, after Anthropic. api.openai.com and Azure go through the
// Responses API (openai_responses.go); other custom endpoints through Chat
// Completions, below.
//
// Hand-rolled rather than SDK-backed because the repo carries no OpenAI SDK
// (internal/ai already speaks this API directly) and because the endpoint must
// stay overridable: a tenant can point their OpenAI token at an Azure resource
// or a gateway, and chat has to honour that the same way
// translation does.
type openaiLLM struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
	// responses selects the Responses API; see usesResponsesAPI.
	responses bool
}

// NewOpenAI returns an LLM backed by OpenAI-compatible Chat Completions.
// baseURL empty = api.openai.com; anything else is the tenant's own endpoint.
func NewOpenAI(apiKey, baseURL, model string) LLM {
	if model == "" {
		model = defaultOpenAIChatModel
	}
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &openaiLLM{
		apiKey:    apiKey,
		baseURL:   baseURL,
		model:     model,
		responses: usesResponsesAPI(baseURL),
		client:    llmClient,
	}
}

// llmClient is shared by every OpenAI and Claude adapter so turns reuse pooled
// connections. No global timeout: the agent loop can stream for a while, and
// cancellation rides on the request context.
var llmClient = netguard.Client(0)

// defaultOpenAIChatModel is only a fallback for an empty model — the picker
// always sends one, validated against the tenant's model catalog.
const defaultOpenAIChatModel = "gpt-4o"

const defaultOpenAIBaseURL = "https://api.openai.com"

// --- wire types (only the fields we read) ---

type oaToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string            `json:"content"`
			ToolCalls []oaToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (o *openaiLLM) Stream(ctx context.Context, req Request, onDelta func(string)) (*Turn, error) {
	if o.responses {
		return o.streamResponses(ctx, req, onDelta)
	}
	payload := map[string]any{
		"model":    o.model,
		"messages": toOpenAIMessages(req.FullSystem(), req.Messages),
		"stream":   true,
		// Without this the final chunk carries no usage and token accounting
		// silently reports zeros.
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(req.Tools) > 0 {
		payload["tools"] = toOpenAITools(req.Tools)
	}
	if req.MaxTokens > 0 {
		// Reasoning models (GPT-5) reject `max_tokens` outright and take
		// `max_completion_tokens` instead.
		payload[ai.OpenAIMaxTokensField(o.model)] = req.MaxTokens
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// Shared with the connection tester and the translate path so a key that
	// verifies under one auth shape can't fail under another.
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
		_, _ = buf.ReadFrom(resp.Body)
		return nil, classifyOpenAIErr(resp.StatusCode, buf.String())
	}
	return o.readStream(resp, onDelta)
}

// readStream consumes the SSE body, forwarding text deltas as they arrive and
// accumulating tool calls. Tool-call arguments stream as fragments keyed by
// `index`, so they're joined per index and only parsed once the stream ends.
func (o *openaiLLM) readStream(resp *http.Response, onDelta func(string)) (*Turn, error) {
	turn := &Turn{}
	var text strings.Builder
	// index → partial call. A map (not a slice) because indexes arrive in
	// whatever order the provider emits and need not start at 0.
	partial := map[int]*ToolCall{}
	var argBuf = map[int]*strings.Builder{}
	order := []int{}

	sc := bufio.NewScanner(resp.Body)
	// Tool-call argument blobs can be large; the default 64KB token cap would
	// truncate one mid-stream and produce unparseable JSON.
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
		var chunk oaStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// A malformed frame mid-stream is not worth aborting a turn over.
			continue
		}
		if chunk.Error != nil {
			return nil, classifyOpenAIErr(0, chunk.Error.Message)
		}
		if chunk.Usage != nil {
			turn.InputTokens = chunk.Usage.PromptTokens
			turn.OutputTokens = chunk.Usage.CompletionTokens
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				text.WriteString(ch.Delta.Content)
				if onDelta != nil {
					onDelta(ch.Delta.Content)
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				if _, seen := partial[tc.Index]; !seen {
					partial[tc.Index] = &ToolCall{}
					argBuf[tc.Index] = &strings.Builder{}
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					partial[tc.Index].ID = tc.ID
				}
				if tc.Function.Name != "" {
					partial[tc.Index].Name = tc.Function.Name
				}
				argBuf[tc.Index].WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason == "tool_calls" {
				turn.StopReason = StopToolUse
			} else if ch.FinishReason != "" && turn.StopReason == "" {
				turn.StopReason = ch.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}

	turn.Text = text.String()
	for _, idx := range order {
		call := partial[idx]
		args := argBuf[idx].String()
		if args == "" {
			args = "{}"
		}
		call.Args = json.RawMessage(args)
		turn.ToolCalls = append(turn.ToolCalls, *call)
	}
	// Some endpoints omit finish_reason on the tool-call chunk; the presence of
	// calls is the authoritative signal for the loop.
	if len(turn.ToolCalls) > 0 {
		turn.StopReason = StopToolUse
	}
	return turn, nil
}

// toOpenAIMessages flattens provider-neutral history into OpenAI's shape.
// Tool results are their own `role:"tool"` messages keyed by tool_call_id,
// rather than content blocks inside a user message as Anthropic wants.
func toOpenAIMessages(system string, msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs)+1)
	if system != "" {
		out = append(out, map[string]any{"role": "system", "content": system})
	}
	for _, m := range msgs {
		// A user turn carrying tool results becomes one `tool` message per
		// result — it is a reply to the assistant's calls, not user prose.
		if len(m.ToolResults) > 0 {
			for _, tr := range m.ToolResults {
				out = append(out, map[string]any{
					"role":         "tool",
					"tool_call_id": tr.ID,
					"content":      tr.Content,
				})
			}
			continue
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				args := string(c.Args)
				if args == "" {
					args = "{}"
				}
				calls = append(calls, map[string]any{
					"id":       c.ID,
					"type":     "function",
					"function": map[string]any{"name": c.Name, "arguments": args},
				})
			}
			msg := map[string]any{"role": "assistant", "tool_calls": calls}
			if m.Text != "" {
				msg["content"] = m.Text
			}
			out = append(out, msg)
			continue
		}
		if m.Image != nil {
			out = append(out, map[string]any{"role": m.Role, "content": []map[string]any{
				{"type": "text", "text": m.Text},
				{"type": "image_url", "image_url": map[string]any{"url": m.Image.DataURL()}},
			}})
			continue
		}
		if m.Text == "" {
			continue
		}
		out = append(out, map[string]any{"role": m.Role, "content": m.Text})
	}
	return out
}

func toOpenAITools(defs []ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		schema := json.RawMessage(d.InputSchema)
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        d.Name,
				"description": d.Description,
				"parameters":  schema,
			},
		})
	}
	return out
}

// classifyOpenAIErr maps transport/HTTP failures onto the same ai.ProviderError
// kinds the Anthropic adapter emits, so the handler's user-facing messages stay
// provider-neutral.
func classifyOpenAIErr(status int, detail string) error {
	kind := ai.ProviderErrProvider
	switch {
	case status == 0:
		kind = ai.ProviderErrUnreachable
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = ai.ProviderErrAuth
	case status == http.StatusTooManyRequests:
		if strings.Contains(strings.ToLower(detail), "insufficient_quota") {
			kind = ai.ProviderErrQuota
		} else {
			kind = ai.ProviderErrRateLimit
		}
	}
	detail = strings.TrimSpace(detail)
	if len(detail) > 500 {
		detail = detail[:500]
	}
	return &ai.ProviderError{Provider: ai.ProviderOpenAI, Status: status, Kind: kind, Detail: detail}
}
