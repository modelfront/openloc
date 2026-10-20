package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"openloc/internal/ai"
)

// DefaultModel matches internal/ai's default for the claude provider so the
// MT path and the chat path never silently diverge on what "default Claude"
// means for a tenant.
const DefaultModel = "claude-sonnet-5"

// anthropicLLM adapts the official SDK to the LLM interface. Constructed per
// request — the key is the tenant's own, so nothing is cached across
// tenants.
type anthropicLLM struct {
	client anthropic.Client
	model  string
}

// NewAnthropic returns an LLM backed by the Anthropic Messages API. baseURL
// empty = api.anthropic.com; a token carrying a custom endpoint (a gateway or
// proxy) is honoured here the same way OpenAI's is.
func NewAnthropic(apiKey, baseURL, model string) LLM {
	if model == "" {
		model = DefaultModel
	}
	// llmClient refuses internal addresses at dial time; the SDK's default
	// client does not.
	opts := []option.RequestOption{option.WithAPIKey(apiKey), option.WithHTTPClient(llmClient)}
	if baseURL != "" {
		// The SDK appends its own paths, so the override is the ROOT.
		opts = append(opts, option.WithBaseURL(strings.TrimRight(baseURL, "/")+"/"))
	}
	return &anthropicLLM{
		client: anthropic.NewClient(opts...),
		model:  model,
	}
}

// buildAnthropicParams sets three cache breakpoints, in render order:
//  1. the last tool — the tool list is identical for every turn of a user in
//     one mode, and it is the largest part of the prefix;
//  2. the stable system prompt — the page line goes in a block after it;
//  3. automatic (top-level) — the growing conversation, so the 2–8 model
//     calls of one turn re-read the history from cache.
func (a *anthropicLLM) buildAnthropicParams(req Request) anthropic.MessageNewParams {
	system := []anthropic.TextBlockParam{{Text: req.System, CacheControl: anthropic.NewCacheControlEphemeralParam()}}
	if req.SystemVolatile != "" {
		system = append(system, anthropic.TextBlockParam{Text: req.SystemVolatile})
	}
	tools := toAnthropicTools(req.Tools)
	if n := len(tools); n > 0 {
		tools[n-1].OfTool.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
	return anthropic.MessageNewParams{
		Model:        anthropic.Model(a.model),
		MaxTokens:    int64(req.MaxTokens),
		System:       system,
		Messages:     toAnthropicMessages(req.Messages),
		Tools:        tools,
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}
}

func (a *anthropicLLM) Stream(ctx context.Context, req Request, onDelta func(string)) (*Turn, error) {
	params := a.buildAnthropicParams(req)

	stream := a.client.Messages.NewStreaming(ctx, params)
	acc := anthropic.Message{}
	for stream.Next() {
		event := stream.Current()
		if err := acc.Accumulate(event); err != nil {
			return nil, fmt.Errorf("chat: accumulate stream event: %w", err)
		}
		if ev, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if delta, ok := ev.Delta.AsAny().(anthropic.TextDelta); ok && delta.Text != "" {
				onDelta(delta.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, classifyAnthropicErr(err)
	}

	turn := &Turn{
		StopReason:   string(acc.StopReason),
		InputTokens:  acc.Usage.InputTokens + acc.Usage.CacheReadInputTokens + acc.Usage.CacheCreationInputTokens,
		OutputTokens: acc.Usage.OutputTokens,
	}
	for _, block := range acc.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			turn.Text += v.Text
		case anthropic.ToolUseBlock:
			turn.ToolCalls = append(turn.ToolCalls, ToolCall{
				ID:   v.ID,
				Name: v.Name,
				Args: json.RawMessage(v.JSON.Input.Raw()),
			})
		}
	}
	// Normalize: the loop branches only on StopToolUse.
	if acc.StopReason == anthropic.StopReasonToolUse {
		turn.StopReason = StopToolUse
	}
	return turn, nil
}

// toAnthropicMessages converts neutral history to SDK params. Tool results
// ride user messages; tool calls ride assistant messages — mirroring how the
// loop threads a live turn.
func toAnthropicMessages(msgs []Message) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		var blocks []anthropic.ContentBlockParamUnion
		// The image goes before the text that refers to it.
		if m.Image != nil {
			blocks = append(blocks, anthropic.NewImageBlockBase64(m.Image.MediaType, m.Image.Base64()))
		}
		if m.Text != "" {
			blocks = append(blocks, anthropic.NewTextBlock(m.Text))
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, json.RawMessage(tc.Args), tc.Name))
		}
		for _, tr := range m.ToolResults {
			blocks = append(blocks, anthropic.NewToolResultBlock(tr.ID, tr.Content, tr.IsError))
		}
		if len(blocks) == 0 {
			continue
		}
		if m.Role == "assistant" {
			out = append(out, anthropic.NewAssistantMessage(blocks...))
		} else {
			out = append(out, anthropic.NewUserMessage(blocks...))
		}
	}
	return out
}

// toAnthropicTools translates MCP JSON Schemas to the SDK's tool params. The
// schemas are plain objects with properties/required — exactly what the SDK
// wants — so this is unpacking, not transformation.
func toAnthropicTools(defs []ToolDef) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(defs))
	for _, d := range defs {
		var schema struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		// A schema that doesn't unmarshal would have failed
		// TestInProcessToolSchemasMarshal first; guard anyway.
		if err := json.Unmarshal(d.InputSchema, &schema); err != nil {
			continue
		}
		if schema.Properties == nil {
			schema.Properties = map[string]any{}
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
			Name:        d.Name,
			Description: anthropic.String(d.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: schema.Properties,
				Required:   schema.Required,
			},
		}})
	}
	return out
}

// classifyAnthropicErr maps SDK errors onto the ProviderError taxonomy that
// internal/ai already established, so the handler surfaces actionable 4xxs
// ("fix your key") instead of opaque 500s.
func classifyAnthropicErr(err error) error {
	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		return &ai.ProviderError{Provider: ai.ProviderClaude, Kind: ai.ProviderErrUnreachable, Detail: err.Error()}
	}
	kind := ai.ProviderErrProvider
	switch apierr.StatusCode {
	case 401, 403:
		kind = ai.ProviderErrAuth
	case 429:
		kind = ai.ProviderErrRateLimit
	}
	return &ai.ProviderError{
		Provider: ai.ProviderClaude,
		Status:   apierr.StatusCode,
		Kind:     kind,
		Detail:   apierr.Error(),
	}
}
