// Package chat implements the built-in AI assistant: an agent loop
// over the tenant's own LLM key, with tools served by the MCP server over an
// in-process transport. Adapters: Anthropic and OpenAI; a new provider is an
// LLM implementation plus a case in defaultLLMFactory.
package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
)

// Message is one turn of provider-neutral conversation history. Persisted
// history replays as text-only (Text set, tool fields empty); within a live
// turn the loop threads tool calls and results through these same fields.
type Message struct {
	Role string // "user" or "assistant"
	Text string
	// Image is a screenshot the user approved and sent with this message
	// (user messages only). Only the newest message of a turn carries one;
	// in later turns its text marker stands in for it.
	Image       *Image
	ToolCalls   []ToolCall   // assistant messages only
	ToolResults []ToolResult // user messages only
}

// Image is one picture for the model.
type Image struct {
	// MediaType is image/png, image/jpeg, image/webp or image/gif.
	MediaType string
	Data      []byte
}

// Base64 is the image data as standard base64.
func (i *Image) Base64() string { return base64.StdEncoding.EncodeToString(i.Data) }

// DataURL is the image as a data: URL, the shape OpenAI takes.
func (i *Image) DataURL() string { return "data:" + i.MediaType + ";base64," + i.Base64() }

// ToolCall is the model asking for one tool invocation.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolResult carries one executed call's outcome back to the model.
type ToolResult struct {
	ID      string
	Content string
	IsError bool
}

// ToolDef describes a callable tool. InputSchema is raw JSON Schema exactly as
// the MCP server advertises it; adapters translate to their provider dialect.
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// SystemPrompt is the system prompt split for caching: Stable is the same for
// every turn of a user in one mode; Volatile changes per request (the page).
type SystemPrompt struct {
	Stable   string
	Volatile string
}

// Request is one model call within the loop.
type Request struct {
	// System is the stable part of the system prompt; SystemVolatile is the
	// per-request tail (page context). Adapters that cache prompts put the
	// cache breakpoint between the two, so navigating does not miss the cache.
	System         string
	SystemVolatile string
	Messages       []Message
	Tools          []ToolDef
	MaxTokens      int
}

// Turn is the model's complete response to one Request.
type Turn struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason string // provider-neutral: "tool_use" means execute and continue
	// InputTokens counts every input token, cached or not.
	InputTokens  int64
	OutputTokens int64
}

// FullSystem is the whole system prompt, for adapters without prompt caching.
func (r Request) FullSystem() string {
	return r.System + r.SystemVolatile
}

// StopToolUse is the one stop reason the loop branches on; adapters normalize
// their provider's value to this.
const StopToolUse = "tool_use"

// LLM is the provider seam. Stream runs one model call, invoking onDelta for
// each text fragment as it arrives, and returns the accumulated turn.
type LLM interface {
	Stream(ctx context.Context, req Request, onDelta func(string)) (*Turn, error)
}
