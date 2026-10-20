package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// The cache breakpoints sit on the last tool and the stable system block;
// the page line comes after them, so navigating does not invalidate the
// cached tools + system prefix.
func TestAnthropicParamsCacheBreakpoints(t *testing.T) {
	a := &anthropicLLM{model: DefaultModel}
	schema := json.RawMessage(`{"type":"object","properties":{}}`)
	params := a.buildAnthropicParams(Request{
		System:         "stable part",
		SystemVolatile: "The user is currently viewing the projects list.",
		Tools:          []ToolDef{{Name: "a", InputSchema: schema}, {Name: "b", InputSchema: schema}},
		Messages:       []Message{{Role: "user", Text: "hi"}},
		MaxTokens:      100,
	})
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		System []struct {
			Text         string          `json:"text"`
			CacheControl json.RawMessage `json:"cache_control"`
		} `json:"system"`
		Tools []struct {
			Name         string          `json:"name"`
			CacheControl json.RawMessage `json:"cache_control"`
		} `json:"tools"`
		CacheControl json.RawMessage `json:"cache_control"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.System) != 2 || body.System[0].Text != "stable part" {
		t.Fatalf("system blocks wrong: %+v", body.System)
	}
	if body.System[0].CacheControl == nil {
		t.Error("stable system block has no cache breakpoint")
	}
	if body.System[1].CacheControl != nil || !strings.Contains(body.System[1].Text, "currently viewing") {
		t.Errorf("page line must follow the breakpoint uncached: %+v", body.System[1])
	}
	if body.Tools[0].CacheControl != nil || body.Tools[1].CacheControl == nil {
		t.Errorf("breakpoint belongs on the last tool only: %s", raw)
	}
	if body.CacheControl == nil {
		t.Error("no automatic breakpoint for the conversation")
	}

	// No page line: one system block, still cached.
	params = a.buildAnthropicParams(Request{System: "stable part", MaxTokens: 100})
	if len(params.System) != 1 {
		t.Errorf("want one system block without a page line, got %d", len(params.System))
	}
}
