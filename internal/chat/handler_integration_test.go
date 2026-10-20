// Built-in chat exercised against a real DB and the real MCP server
// over the in-process transport. Only the LLM is fake: its scripted turns
// drive REAL tool calls through ConnectInProcess, so what's proven here is the
// full chain browser-claims → loop → MCP → tenant-scoped SQL → SSE.
package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	"openloc/internal/chat"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/httputil/ssetest"
	"openloc/internal/issues"
	"openloc/internal/mcp"
	"openloc/internal/secrets"
	"openloc/internal/testutil"
)

// scriptedLLM emits turns in order; the second turn's text proves history
// (tool results included) actually reached the model.
type scriptedLLM struct {
	turns []*chat.Turn
	seen  []chat.Request
}

// reset swaps the script and clears the record, so one fixture can drive
// several independent calls.
func (s *scriptedLLM) reset(turns []*chat.Turn) {
	s.turns = turns
	s.seen = nil
}

// lastSystem / lastUser expose what was actually SENT — the thing worth
// asserting on when the reply is scripted anyway.
func (s *scriptedLLM) lastSystem() string {
	if len(s.seen) == 0 {
		return ""
	}
	return s.seen[len(s.seen)-1].System
}

func (s *scriptedLLM) lastUser() string {
	if len(s.seen) == 0 {
		return ""
	}
	msgs := s.seen[len(s.seen)-1].Messages
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].Text
}

func (s *scriptedLLM) Stream(_ context.Context, req chat.Request, onDelta func(string)) (*chat.Turn, error) {
	i := len(s.seen)
	s.seen = append(s.seen, req)
	if i >= len(s.turns) {
		return &chat.Turn{Text: "(exhausted)", StopReason: "end_turn"}, nil
	}
	t := s.turns[i]
	if t.Text != "" {
		onDelta(t.Text)
	}
	return t, nil
}

type chatFixture struct {
	q       db.Querier
	pool    *pgxpool.Pool
	handler *chat.Handler
	router  chi.Router
	llm     *scriptedLLM
	store   *file.MemoryStore
	// cipher is the encrypted test key, kept so a test can seed a SECOND
	// vendor's token (the multi-provider path) without re-deriving it.
	cipher string
	// backend is the MCP backend behind the chat, for a test that wires a
	// service into it (mcp.WireIssues).
	backend mcp.Backend

	tenantA db.Tenant
	tenantB db.Tenant
	ownerA  db.User
	ownerB  db.User
	project db.Project
}

func newChatFixture(t *testing.T, script []*chat.Turn) *chatFixture {
	t.Helper()
	return newChatFixtureWith(t, script, true)
}

// newChatFixtureWith is newChatFixture on a deployment that does (or does not)
// take issue reports.
func newChatFixtureWith(t *testing.T, script []*chat.Turn, issueReports bool) *chatFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)

	f := &chatFixture{q: q, pool: pool, llm: &scriptedLLM{turns: script}}
	f.tenantA = testutil.SeedTenant(t, q, "ChatTenantA")
	f.tenantB = testutil.SeedTenant(t, q, "ChatTenantB")
	f.ownerA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "chat-a@test.com", "pass", "owner")
	f.ownerB = testutil.SeedUserWithRole(t, q, f.tenantB.ID, "chat-b@test.com", "pass", "owner")
	f.project = testutil.SeedProject(t, q, f.tenantA.ID, "Chat Alpha", "en", []string{"de"})

	enc, err := secrets.NewEncryptor("chat-test-secrets-key")
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	// Tenant A has a Claude key; tenant B deliberately does not.
	cipher, err := enc.Encrypt("sk-ant-test-not-real")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	f.cipher = cipher
	testutil.SeedAIVendorToken(t, q, f.tenantA.ID, "claude", "Default", cipher)

	f.backend = mcp.NewDirectBackend(q)
	mcpSrv, err := mcp.NewServer(mcp.Deps{Backend: f.backend, Audit: q, Version: "test", IssueReports: issueReports})
	if err != nil {
		t.Fatalf("mcp server: %v", err)
	}

	f.handler = chat.NewHandler(q, enc, mcpSrv, nil, testutil.TestAICatalog(pool))
	f.handler.SetPlayground(true)
	f.handler.SetIssueReports(issueReports)
	f.store = file.NewMemoryStore()
	f.handler.SetStorage(f.store)
	f.handler.SetLLMFactory(func(_, apiKey, _, model string) chat.LLM {
		if apiKey != "sk-ant-test-not-real" {
			t.Errorf("decrypted key mismatch: %q", apiKey)
		}
		if model != chat.DefaultModel {
			t.Errorf("model = %q, want %q", model, chat.DefaultModel)
		}
		return f.llm
	})

	r := chi.NewRouter()
	r.Post("/api/chat", f.handler.HandleSend)
	r.Post("/api/chat/confirm", f.handler.HandleConfirm)
	r.Post("/api/chat/issue-reported", f.handler.HandleIssueReported)
	r.Post("/api/chat/uploads", f.handler.HandleUpload)
	r.Get("/api/chat/uploads/{uploadID}", f.handler.HandleUploadImage)
	r.Get("/api/chat/playbooks", f.handler.HandleListPlaybooks)
	r.Get("/api/chat/speech", f.handler.HandleSpeechStatus)
	r.Post("/api/chat/speech", f.handler.HandleSpeech)
	r.Get("/api/chat/models", f.handler.HandleListModels)
	r.Get("/api/chat/conversations", f.handler.HandleListConversations)
	r.Get("/api/chat/conversations/{id}/messages", f.handler.HandleGetMessages)
	r.Delete("/api/chat/conversations/{id}", f.handler.HandleDeleteConversation)
	f.router = r
	return f
}

// wireIssues puts an issue service, with one support team member, behind the
// chat's MCP backend and returns it.
func (f *chatFixture) wireIssues() *issues.Service {
	svc := issues.NewService(f.pool, nil, nil, nil, []string{"ops@test.com"}, "http://localhost")
	mcp.WireIssues(f.backend, svc)
	return svc
}

func (f *chatFixture) do(t *testing.T, method, path, body string, user db.User, tenantID int64) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenantID, UserID: user.ID, Role: "owner",
	}))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// turnOutcome is what a streamed turn ended with: its conversation and the
// confirm card a guarded tool's preview sent, if any.
type turnOutcome struct {
	conversationID int64
	confirm        map[string]any
}

// turnOf reads a turn's events for its outcome.
func turnOf(events []struct {
	Name string
	Data map[string]any
}) turnOutcome {
	var out turnOutcome
	for _, ev := range events {
		switch ev.Name {
		case "confirm_required":
			out.confirm, _ = ev.Data["confirm"].(map[string]any)
		case "done":
			out.conversationID = int64(ev.Data["conversation_id"].(float64))
		}
	}
	return out
}

// readTurn is the outcome of a turn's streamed body.
func readTurn(t *testing.T, body string) turnOutcome {
	t.Helper()
	return turnOf(sseEvents(t, body))
}

// sseEvents parses "event: X\ndata: {...}" pairs from a streamed body.
func sseEvents(t *testing.T, body string) []struct {
	Name string
	Data map[string]any
} {
	t.Helper()
	var out []struct {
		Name string
		Data map[string]any
	}
	for frame := range ssetest.Frames(strings.NewReader(body)) {
		name := ssetest.Event(frame)
		var data map[string]any
		if raw := ssetest.Data(frame); raw != "" {
			if err := json.Unmarshal([]byte(raw), &data); err != nil {
				t.Fatalf("bad SSE data %q: %v", raw, err)
			}
		}
		if name != "" {
			out = append(out, struct {
				Name string
				Data map[string]any
			}{name, data})
		}
	}
	return out
}

func TestIntegrationChatToolRoundTripAgainstRealMCP(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{
		{
			StopReason: chat.StopToolUse,
			ToolCalls: []chat.ToolCall{{
				ID: "tc1", Name: "openloc_list_projects", Args: json.RawMessage(`{}`),
			}},
			InputTokens: 100, OutputTokens: 20,
		},
		{Text: "You have one project: Chat Alpha.", StopReason: "end_turn", InputTokens: 150, OutputTokens: 12},
	})

	rec := f.do(t, "POST", "/api/chat", `{"message":"what projects do I have?"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	events := sseEvents(t, rec.Body.String())

	var names []string
	for _, e := range events {
		names = append(names, e.Name)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"tool_start", "tool_end", "text", "done"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q event; got %s", want, joined)
		}
	}

	// The REAL MCP tool ran: its result (tenant A's actual project) must have
	// been threaded into the second model request.
	second := f.llm.seen[1]
	lastMsg := second.Messages[len(second.Messages)-1]
	if len(lastMsg.ToolResults) != 1 {
		t.Fatalf("tool result not threaded: %+v", lastMsg)
	}
	if !strings.Contains(lastMsg.ToolResults[0].Content, "Chat Alpha") {
		t.Fatalf("real project name missing from tool result: %q", lastMsg.ToolResults[0].Content)
	}

	// Both messages persisted; assistant row carries tool summary + usage.
	convID := turnOf(events).conversationID
	if convID == 0 {
		t.Fatal("done event missing conversation_id")
	}
	msgs, err := f.q.ListChatMessages(context.Background(), db.ListChatMessagesParams{
		ConversationID: convID, TenantID: f.tenantA.ID, UserID: f.ownerA.ID,
	})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("persisted %d messages, want 2", len(msgs))
	}
	assistant := msgs[1]
	if assistant.Role != "assistant" || assistant.Content != "You have one project: Chat Alpha." {
		t.Fatalf("assistant row = %+v", assistant)
	}
	if assistant.InputTokens != 250 || assistant.OutputTokens != 32 {
		t.Fatalf("usage = %d/%d, want 250/32", assistant.InputTokens, assistant.OutputTokens)
	}
	if assistant.ToolCalls == nil || !strings.Contains(string(assistant.ToolCalls), "openloc_list_projects") {
		t.Fatalf("tool summary not persisted: %s", assistant.ToolCalls)
	}
}

func TestIntegrationChatNoKeyIs412(t *testing.T) {
	f := newChatFixture(t, nil)
	rec := f.do(t, "POST", "/api/chat", `{"message":"hello"}`, f.ownerB, f.tenantB.ID)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no_claude_key") {
		t.Fatalf("missing stable code: %s", rec.Body.String())
	}
}

func TestIntegrationChatConversationTenantIsolation(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{
		{Text: "hi", StopReason: "end_turn"},
	})
	// Create a conversation as tenant A.
	rec := f.do(t, "POST", "/api/chat", `{"message":"hello"}`, f.ownerA, f.tenantA.ID)
	events := sseEvents(t, rec.Body.String())
	convID := turnOf(events).conversationID
	if convID == 0 {
		t.Fatalf("no conversation created: %s", rec.Body.String())
	}

	// Tenant B must neither read nor continue nor delete it.
	path := fmt.Sprintf("/api/chat/conversations/%d/messages", convID)
	rec = f.do(t, "GET", path, "", f.ownerB, f.tenantB.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant read: status = %d, want 404", rec.Code)
	}

	// A key of its own takes tenant B past the missing-key 412 to the
	// conversation lookup.
	testutil.SeedAIVendorToken(t, f.q, f.tenantB.ID, "claude", "Default", f.cipher)
	body := fmt.Sprintf(`{"conversation_id":%d,"message":"continue"}`, convID)
	rec = f.do(t, "POST", "/api/chat", body, f.ownerB, f.tenantB.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant continuation: status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}

	rec = f.do(t, "DELETE", fmt.Sprintf("/api/chat/conversations/%d", convID), "", f.ownerB, f.tenantB.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant delete: status = %d, want 404", rec.Code)
	}

	// And the owner still can do all three.
	rec = f.do(t, "GET", path, "", f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner read broken: %d", rec.Code)
	}
}

func TestIntegrationChatHistoryReplaysAcrossTurns(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{
		{Text: "First answer.", StopReason: "end_turn"},
		{Text: "Second answer.", StopReason: "end_turn"},
	})
	rec := f.do(t, "POST", "/api/chat", `{"message":"first question"}`, f.ownerA, f.tenantA.ID)
	convID := readTurn(t, rec.Body.String()).conversationID

	body := fmt.Sprintf(`{"conversation_id":%d,"message":"second question"}`, convID)
	rec = f.do(t, "POST", "/api/chat", body, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("second turn: %d", rec.Code)
	}

	// The second model call must have seen: first question, first answer,
	// second question — in order.
	second := f.llm.seen[1]
	var texts []string
	for _, m := range second.Messages {
		texts = append(texts, m.Role+":"+m.Text)
	}
	joined := strings.Join(texts, "|")
	want := "user:first question|assistant:First answer.|user:second question"
	if joined != want {
		t.Fatalf("history = %s, want %s", joined, want)
	}
}

func TestIntegrationChatWriteToolDeniedEndToEnd(t *testing.T) {
	// The model tries a write; the MCP layer must refuse (read-only v1) and
	// the refusal must be model-visible, not a crash.
	f := newChatFixture(t, []*chat.Turn{
		{
			StopReason: chat.StopToolUse,
			ToolCalls: []chat.ToolCall{{
				ID: "tc1", Name: "openloc_add_comment",
				Args: json.RawMessage(`{"translation_id":1,"content":"attempt"}`),
			}},
		},
		{Text: "I can't make changes.", StopReason: "end_turn"},
	})
	rec := f.do(t, "POST", "/api/chat", `{"message":"add a comment"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	second := f.llm.seen[1]
	res := second.Messages[len(second.Messages)-1].ToolResults[0]
	if !res.IsError || !strings.Contains(res.Content, "user confirmation") {
		t.Fatalf("write not refused as read-only: %+v", res)
	}
}

// The mode picker. allow_writes=true lets the loop's session through
// the MCP write gate via the write grant; the default (read-only) both
// withholds write tools from the model AND keeps the gate closed.
func TestIntegrationChatWriteModeExecutesWrites(t *testing.T) {
	f := newChatFixture(t, nil)
	// A real segment to comment on.
	file := testutil.SeedFile(t, f.q, f.project.ID, "wm.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, f.q, file.ID, "Hello", 0)
	tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", db.TranslationStatusNeedsReview)

	f.llm.turns = []*chat.Turn{
		{
			StopReason: chat.StopToolUse,
			ToolCalls: []chat.ToolCall{{
				ID: "tc1", Name: "openloc_add_comment",
				Args: json.RawMessage(fmt.Sprintf(`{"translation_id":%d,"content":"chat says hi"}`, tr.ID)),
			}},
		},
		{Text: "Comment added.", StopReason: "end_turn"},
	}

	rec := f.do(t, "POST", "/api/chat", `{"message":"comment on it","allow_writes":true}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	// The tool result the model saw must be a SUCCESS, not a gate denial.
	res := f.llm.seen[1].Messages[len(f.llm.seen[1].Messages)-1].ToolResults[0]
	if res.IsError {
		t.Fatalf("write-mode comment denied: %s", res.Content)
	}
	// And the comment genuinely exists.
	comments, err := f.q.ListCommentsByTranslation(context.Background(), tr.ID)
	if err != nil || len(comments) != 1 || comments[0].Content != "chat says hi" {
		t.Fatalf("comment not persisted: %v %+v", err, comments)
	}
}

func TestIntegrationChatReadOnlyModeWithholdsWriteTools(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{{Text: "ok", StopReason: "end_turn"}})

	rec := f.do(t, "POST", "/api/chat", `{"message":"hi"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	offered := f.llm.seen[0].Tools
	if len(offered) == 0 {
		t.Fatal("no tools offered at all")
	}
	var sawReport bool
	for _, tool := range offered {
		switch tool.Name {
		case "openloc_add_comment", "openloc_lock_segment", "openloc_save_draft":
			t.Fatalf("read-only mode offered write tool %s to the model", tool.Name)
		case "openloc_report_issue":
			sawReport = true
		}
	}
	// Reporting an issue changes none of the org's data, so it stays.
	if !sawReport {
		t.Fatal("read-only mode withheld openloc_report_issue")
	}
	if !strings.Contains(f.llm.seen[0].FullSystem(), "openloc_report_issue") {
		t.Error("the read-only prompt does not name reporting as the exception")
	}
	// The system prompt teaches the mode-switch UX.
	if !strings.Contains(f.llm.seen[0].FullSystem(), "Allow write") {
		t.Fatal("read-only system prompt missing the mode-switch instruction")
	}

	// Write mode offers them again.
	f.llm.turns = []*chat.Turn{{Text: "ok", StopReason: "end_turn"}}
	f.llm.seen = nil
	rec = f.do(t, "POST", "/api/chat", `{"message":"hi","allow_writes":true}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("write-mode status = %d", rec.Code)
	}
	var sawWrite bool
	for _, tool := range f.llm.seen[0].Tools {
		if tool.Name == "openloc_save_draft" {
			sawWrite = true
		}
	}
	if !sawWrite {
		t.Fatal("write mode did not offer write tools")
	}
}

// Page context in, navigation out — over the real handler + SSE.
func TestIntegrationChatPageContextAndNavigation(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{
		{
			StopReason: chat.StopToolUse,
			ToolCalls: []chat.ToolCall{{
				ID: "nav1", Name: "navigate_to",
				Args: json.RawMessage(`{"page":"project","project_id":42}`),
			}},
		},
		{Text: "Opened it.", StopReason: "end_turn"},
	})

	body := fmt.Sprintf(`{"message":"open the alpha project","page_path":"/projects/%d/jobs/9/edit"}`, f.project.ID)
	rec := f.do(t, "POST", "/api/chat", body, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	// Context line reached the model, with "this/here" guidance.
	sys := f.llm.seen[0].FullSystem()
	if !strings.Contains(sys, "currently viewing the CAT editor for job 9") {
		t.Fatalf("page context missing from system prompt: %q", sys)
	}

	// client_action rode the SSE stream with the server-built path.
	var sawNav bool
	for _, e := range sseEvents(t, rec.Body.String()) {
		if e.Name == "client_action" {
			if e.Data["action"] != "navigate" || e.Data["path"] != "/projects/42" {
				t.Fatalf("client_action payload wrong: %+v", e.Data)
			}
			sawNav = true
		}
	}
	if !sawNav {
		t.Fatal("no client_action event on the stream")
	}

	// A garbage path is ignored, not echoed into the prompt.
	f.llm.turns = []*chat.Turn{{Text: "ok", StopReason: "end_turn"}}
	f.llm.seen = nil
	rec = f.do(t, "POST", "/api/chat", `{"message":"hi","page_path":"javascript:alert(1)"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(f.llm.seen[0].FullSystem(), "javascript") || strings.Contains(f.llm.seen[0].FullSystem(), "currently viewing") {
		t.Fatalf("garbage path leaked into the prompt: %q", f.llm.seen[0].FullSystem())
	}
}

func TestIntegrationChatModelsEndpoint(t *testing.T) {
	f := newChatFixture(t, nil)

	// Tenant A has a Claude key only. Claude models: connected+selectable.
	// OpenAI models: listed but not selectable (no key AND no adapter yet).
	// An installed extension never appears — it runs QE+APE, not chat.
	rec := f.do(t, "GET", "/api/chat/models", "", f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []struct {
			Provider   string `json:"provider"`
			ID         string `json:"id"`
			Label      string `json:"label"`
			Connected  bool   `json:"connected"`
			Selectable bool   `json:"selectable"`
			Default    bool   `json:"default"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var sawClaudeDefault bool
	for _, m := range resp.Data {
		switch m.Provider {
		case "claude":
			if !m.Connected || !m.Selectable {
				t.Fatalf("claude model %s should be connected+selectable: %+v", m.ID, m)
			}
			if m.ID == chat.DefaultModel {
				if !m.Default {
					t.Fatalf("%s not marked default", m.ID)
				}
				sawClaudeDefault = true
			}
		case "openai":
			// The catalog comes from the vendor's API, called with the tenant's
			// own key. Tenant A has no OpenAI key, so there
			// is no list to show — the picker can't advertise models it has no
			// way to enumerate.
			t.Fatalf("openai has no key for this tenant, so it should contribute no rows: %+v", m)
		case "ext:mockqe":
			t.Fatalf("an extension leaked into the chat model list: %+v", m)
		}
	}
	if !sawClaudeDefault {
		t.Fatalf("expected the claude default to be listed, got %+v", resp.Data)
	}

	// Linguists get the list too — the picker is for everyone with chat.
	linguist := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "chat-linguist@test.com", "pass", "linguist")
	rec = f.do(t, "GET", "/api/chat/models", "", linguist, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("linguist blocked from models list: %d", rec.Code)
	}
}

func TestIntegrationChatModelValidation(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{{Text: "ok", StopReason: "end_turn"}})
	var gotModel string
	f.handler.SetLLMFactory(func(_, _, _, model string) chat.LLM {
		gotModel = model
		return f.llm
	})

	// A valid, non-default Claude model flows through to the factory.
	rec := f.do(t, "POST", "/api/chat", `{"message":"q","model":"claude-haiku-4-5-20251001"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid model rejected: %d %s", rec.Code, rec.Body.String())
	}
	if gotModel != "claude-haiku-4-5-20251001" {
		t.Fatalf("factory got model %q", gotModel)
	}

	// An OpenAI model has an adapter, so validation lets it through — the
	// only thing standing in the way is this tenant having no OpenAI key, which
	// is a 412 setup problem rather than a 400 "unavailable model".
	rec = f.do(t, "POST", "/api/chat", `{"message":"q","model":"gpt-4o"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("openai model without a key: got %d, want 412 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "OpenAI") {
		t.Fatalf("412 should name the vendor whose key is missing, got %s", rec.Body.String())
	}

	// With an OpenAI key present it runs, and the factory is told which vendor
	// to build for rather than always constructing an Anthropic client.
	var gotProvider string
	f.handler.SetLLMFactory(func(provider, _, _, model string) chat.LLM {
		gotProvider, gotModel = provider, model
		return f.llm
	})
	testutil.SeedAIVendorToken(t, f.q, f.tenantA.ID, "openai", "Default", f.cipher)
	rec = f.do(t, "POST", "/api/chat", `{"message":"q","model":"gpt-4o"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("openai model with a key rejected: %d %s", rec.Code, rec.Body.String())
	}
	if gotProvider != "openai" || gotModel != "gpt-4o" {
		t.Fatalf("factory got provider=%q model=%q, want openai/gpt-4o", gotProvider, gotModel)
	}

	// Restore the plain factory for the remaining cases.
	f.handler.SetLLMFactory(func(_, _, _, model string) chat.LLM {
		gotModel = model
		return f.llm
	})

	// Garbage never reaches the provider.
	rec = f.do(t, "POST", "/api/chat", `{"message":"q","model":"gpt-999-turbo-max"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown model accepted: %d", rec.Code)
	}

	// Omitted model still defaults.
	gotModel = ""
	rec = f.do(t, "POST", "/api/chat", `{"message":"q"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("default-model send failed: %d", rec.Code)
	}
	if gotModel != chat.DefaultModel {
		t.Fatalf("factory got %q, want default %q", gotModel, chat.DefaultModel)
	}
}

// slowLLM blocks long enough for at least one keepalive tick before answering
// — the "model silently generating" shape that got prod streams killed by the
// load balancer's idle timeout.
type slowLLM struct {
	delay time.Duration
}

func (s *slowLLM) Stream(ctx context.Context, _ chat.Request, onDelta func(string)) (*chat.Turn, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	onDelta("late answer")
	return &chat.Turn{Text: "late answer", StopReason: "end_turn"}, nil
}

func TestIntegrationChatKeepaliveDuringSilentGeneration(t *testing.T) {
	restore := chat.SetKeepaliveIntervalForTest(30 * time.Millisecond)
	defer restore()

	f := newChatFixture(t, nil)
	f.handler.SetLLMFactory(func(_, _, _, _ string) chat.LLM {
		return &slowLLM{delay: 200 * time.Millisecond}
	})

	rec := f.do(t, "POST", "/api/chat", `{"message":"slow one"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, ": ping") {
		t.Fatalf("no keepalive comment during a %v silent model call:\n%s", 200*time.Millisecond, body)
	}
	// The answer still arrives intact after the pings — the mutex kept frames
	// from interleaving.
	if !strings.Contains(body, "late answer") || !strings.Contains(body, "event: done") {
		t.Fatalf("stream corrupted or incomplete:\n%s", body)
	}
}

func TestIntegrationChatValidation(t *testing.T) {
	f := newChatFixture(t, nil)
	for name, body := range map[string]string{
		"empty":    `{"message":"  "}`,
		"bad json": `{`,
		"too long": fmt.Sprintf(`{"message":%q}`, strings.Repeat("a", 8001)),
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.do(t, "POST", "/api/chat", body, f.ownerA, f.tenantA.ID)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

// Suggest-chart cascade (prompt cards): ai → none. Tenant A
// has a Claude key and the scripted LLM stands in for it; tenant B has none,
// so it gets the greyed hint. There is no static pool — a fixed list cannot
// know what a tenant already has.
func TestIntegrationSuggestChartCascade(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{
		{Text: "Create a chart of QA issues by check type and save it to the Quality dashboard.", StopReason: "end_turn"},
	})
	f.router.Get("/api/chat/suggest-chart", f.handler.HandleSuggestChart)
	ctx := context.Background()

	suggest := func(query string, tenant db.Tenant, user db.User) (code int, body map[string]any) {
		t.Helper()
		rec := f.do(t, "GET", "/api/chat/suggest-chart"+query, "", user, tenant.ID)
		var env struct {
			Data map[string]any `json:"data"`
		}
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("parse: %v", err)
			}
		}
		return rec.Code, env.Data
	}

	// Tier 1, empty tenant: the model's sentence comes back as BOTH prompt and
	// label — it is already one human line.
	code, data := suggest("", f.tenantA, f.ownerA)
	if code != 200 || data["source"] != "ai" {
		t.Fatalf("tier1: %d %v", code, data)
	}
	if p, _ := data["prompt"].(string); !strings.Contains(p, "QA issues by check type") {
		t.Fatalf("tier1 prompt: %q", p)
	}
	if data["label"] != data["prompt"] {
		t.Errorf("label should be the prompt: %v vs %v", data["label"], data["prompt"])
	}

	// Existing charts reach the model as context, so it can avoid repeating
	// them. Assert on what was SENT rather than on the scripted reply.
	d, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: f.tenantA.ID, Name: "Ops A"})
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if _, err := f.q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
		TenantID: f.tenantA.ID, DashboardID: d.ID, Title: "Words by language",
		Definition: []byte(`{"measure":"words_processed","dimensions":["target_language"],"last_days":90}`),
		Viz:        []byte(`{"type":"bar","x_key":"target_language","series":["words"],"series_key":"kind"}`),
	}); err != nil {
		t.Fatalf("chart: %v", err)
	}
	f.llm.reset([]*chat.Turn{{Text: "Create a chart of editing minutes per linguist on the Team dashboard.", StopReason: "end_turn"}})
	if _, data = suggest(fmt.Sprintf("?dashboard_id=%d", d.ID), f.tenantA, f.ownerA); data["source"] != "ai" {
		t.Fatalf("tier1 with context: %v", data)
	}
	sent := f.llm.lastUser()
	for _, want := range []string{"Words by language", "words_processed", "target_language", "kind"} {
		if !strings.Contains(sent, want) {
			t.Errorf("existing-chart context missing %q: %s", want, sent)
		}
	}
	// The dashboard the card sits in is named, so the suggestion lands there.
	if sys := f.llm.lastSystem(); !strings.Contains(sys, `"Ops A"`) {
		t.Errorf("system prompt should name the target dashboard: %s", sys)
	}

	// A reply that names a window is dropped — the dashboard's own controls own
	// it, so such advice would be wrong the moment someone moves a dropdown.
	f.llm.reset([]*chat.Turn{{Text: "Create a chart of words processed over the last 90 days.", StopReason: "end_turn"}})
	_, data = suggest("", f.tenantA, f.ownerA)
	if data["source"] != "none" {
		t.Errorf("window-naming suggestion should degrade to the hint: %v", data)
	}

	// Tier 2: tenant B has no Claude key, so there is nothing to suggest — the
	// card shows a greyed, non-sendable hint.
	_, data = suggest("", f.tenantB, f.ownerB)
	if data["source"] != "none" || data["hint"] == nil || data["prompt"] != nil {
		t.Fatalf("tier2 (no key): %v", data)
	}

	// Cross-tenant dashboard_id probe: opaque 404.
	code, _ = suggest(fmt.Sprintf("?dashboard_id=%d", d.ID), f.tenantB, f.ownerB)
	if code != http.StatusNotFound {
		t.Fatalf("cross-tenant dashboard_id = %d, want 404", code)
	}
}

// The prompt card also renders inside a PERSONAL dashboard, so the phrasing
// lookup has to carry the caller's id. Without it the visibility predicate
// matches only shared dashboards and the card 404s — and the parameter is a
// pointer, so omitting it compiles.
func TestIntegrationSuggestChartOnPersonalDashboard(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{
		{Text: `Create a chart of QA issues by severity and save it to "My experiments".`, StopReason: "end_turn"},
	})
	f.router.Get("/api/chat/suggest-chart", f.handler.HandleSuggestChart)
	ctx := context.Background()

	mine, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "My experiments", OwnerID: &f.ownerA.ID,
	})
	if err != nil {
		t.Fatalf("seed personal dashboard: %v", err)
	}
	query := "?dashboard_id=" + strconv.FormatInt(mine.ID, 10)

	rec := f.do(t, "GET", "/api/chat/suggest-chart"+query, "", f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner's own dashboard = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	// The system prompt names that dashboard, proving the row was resolved.
	if sys := f.llm.lastSystem(); !strings.Contains(sys, `"My experiments"`) {
		t.Errorf("system prompt should name the personal dashboard: %s", sys)
	}

	// A colleague cannot use it as a suggestion target — same 404 as absent.
	colleague := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "chat-a2@test.com", "pass", "owner")
	if rec := f.do(t, "GET", "/api/chat/suggest-chart"+query, "", colleague, f.tenantA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("colleague = %d, want 404", rec.Code)
	}
}

// A deployment without platform operators takes no reports: the assistant is
// neither offered the tool nor told it may report.
func TestIntegrationChatNoIssueReportsWithoutOperators(t *testing.T) {
	f := newChatFixtureWith(t, []*chat.Turn{{Text: "ok", StopReason: "end_turn"}}, false)
	if rec := f.do(t, "POST", "/api/chat", `{"message":"hi"}`, f.ownerA, f.tenantA.ID); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, tool := range f.llm.seen[0].Tools {
		if tool.Name == "openloc_report_issue" {
			t.Error("offered openloc_report_issue without operators")
		}
	}
	if strings.Contains(f.llm.seen[0].FullSystem(), "openloc_report_issue") {
		t.Error("the prompt mentions reporting without operators")
	}
}

// A new conversation is titled from the user's words, cut at 60 characters,
// never inside a multi-byte character.
func TestIntegrationChatTitleCutsByCharacter(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{{Text: "ok", StopReason: "end_turn"}})
	msg := strings.Repeat("a", 59) + strings.Repeat("ж", 10)
	rec := f.do(t, "POST", "/api/chat", fmt.Sprintf(`{"message":%q}`, msg), f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
	}
	var title string
	if err := f.pool.QueryRow(context.Background(), `SELECT title FROM chat_conversations`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if want := strings.Repeat("a", 59) + "ж"; title != want {
		t.Fatalf("title = %q, want %q", title, want)
	}
}
