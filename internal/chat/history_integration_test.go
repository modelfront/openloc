package chat_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/chat"
)

// The panel restores a conversation after a page refresh from
// GET /conversations/{id}/messages, so tool_calls must arrive as a JSON array
// the UI can render as chips — not as the base64 of the stored JSONB bytes.
func TestIntegrationChatMessagesReturnToolCallsAsJSON(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{
		{StopReason: chat.StopToolUse, ToolCalls: []chat.ToolCall{{ID: "t1", Name: "openloc_list_projects", Args: json.RawMessage(`{}`)}}},
		{Text: "You have no projects yet.", StopReason: "end_turn"},
	})
	rec := f.do(t, "POST", "/api/chat", `{"message":"my projects?"}`, f.ownerA, f.tenantA.ID)
	convID := readTurn(t, rec.Body.String()).conversationID

	rec = f.do(t, "GET", fmt.Sprintf("/api/chat/conversations/%d/messages", convID), "", f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("messages: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Name string `json:"name"`
				OK   bool   `json:"ok"`
			} `json:"tool_calls"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode (tool_calls must be an array): %v — body %s", err, rec.Body.String())
	}
	if len(env.Data) != 2 || env.Data[1].Role != "assistant" {
		t.Fatalf("messages = %+v", env.Data)
	}
	if tc := env.Data[1].ToolCalls; len(tc) != 1 || tc[0].Name != "openloc_list_projects" || !tc[0].OK {
		t.Errorf("tool_calls = %+v", tc)
	}
	if env.Data[0].ToolCalls != nil {
		t.Errorf("user message should have no tool_calls, got %+v", env.Data[0].ToolCalls)
	}
}
