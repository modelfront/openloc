package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"openloc/internal/auth"
	"openloc/internal/chat"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/issues"
	"openloc/internal/testutil"
)

// A guarded tool (delete_custom_palette) called by the model in write mode
// only previews. The token goes to the browser, never to the model, and only
// POST /api/chat/confirm runs the delete.
func TestIntegrationChatGuardedToolNeedsConfirmCard(t *testing.T) {
	f := newChatFixture(t, nil)
	anchors := `{"bg":"#0b1020","surface":"#121a33","elevated":"#1b2547","border":"#2a3660","text":"#eef2ff","muted":"#93a1c9","accent":"#5b8cff"}`
	f.llm.turns = []*chat.Turn{
		{StopReason: chat.StopToolUse, ToolCalls: []chat.ToolCall{
			{ID: "t1", Name: "openloc_upsert_custom_palette", Args: json.RawMessage(
				`{"id":"doomed","name":"Doomed","mode":"dark","anchors":` + anchors + `}`)},
			{ID: "t2", Name: "openloc_upsert_custom_palette", Args: json.RawMessage(
				`{"id":"keeper","name":"Keeper","mode":"dark","anchors":` + anchors + `}`)},
		}},
		{StopReason: chat.StopToolUse, ToolCalls: []chat.ToolCall{
			{ID: "t3", Name: "openloc_delete_custom_palette", Args: json.RawMessage(`{"id":"doomed"}`)},
		}},
		{Text: "Confirm in the card to delete it.", StopReason: "end_turn"},
	}
	rec := f.do(t, "POST", "/api/chat", `{"message":"make then delete a palette","allow_writes":true}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	turn := readTurn(t, rec.Body.String())
	confirm, convID := turn.confirm, turn.conversationID
	if confirm == nil {
		t.Fatal("no confirm_required event")
	}
	token, _ := confirm["token"].(string)
	if token == "" || !strings.Contains(fmt.Sprint(confirm["summary"]), "Doomed") {
		t.Fatalf("confirm payload incomplete: %+v", confirm)
	}

	// The model saw the preview, not the token.
	seen := f.llm.seen[2].Messages
	toModel := seen[len(seen)-1].ToolResults[0]
	if strings.Contains(toModel.Content, token) {
		t.Fatalf("the model received the confirm token: %s", toModel.Content)
	}
	if !strings.Contains(toModel.Content, "needs_confirmation") {
		t.Errorf("the model should see the preview status: %s", toModel.Content)
	}
	palettes := func() string {
		a, err := f.q.GetUserAppearance(context.Background(), f.ownerA.ID)
		if err != nil {
			t.Fatalf("read appearance: %v", err)
		}
		return string(a.CustomPalettes)
	}
	if !strings.Contains(palettes(), `"doomed"`) {
		t.Fatal("the preview deleted the palette")
	}

	post := func(body string) (int, map[string]any) {
		r := f.do(t, "POST", "/api/chat/confirm", body, f.ownerA, f.tenantA.ID)
		var env struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(r.Body.Bytes(), &env)
		return r.Code, env.Data
	}

	// A token for "doomed" cannot delete "keeper".
	code, out := post(fmt.Sprintf(`{"conversation_id":%d,"tool":"openloc_delete_custom_palette","args":{"id":"keeper"},"confirm_token":%q}`, convID, token))
	if code != http.StatusOK || out["ok"] != false {
		t.Errorf("tampered args: want 200 ok=false, got %d %+v", code, out)
	}
	// A token cannot be spent on a different, unguarded tool.
	code, out = post(fmt.Sprintf(`{"conversation_id":%d,"tool":"openloc_set_active_palette","args":{"dark_palette":"graphite"},"confirm_token":%q}`, convID, token))
	if code != http.StatusOK || out["ok"] != false {
		t.Errorf("other tool: want 200 ok=false, got %d %+v", code, out)
	}
	// Another user's conversation is not found.
	if r := f.do(t, "POST", "/api/chat/confirm", fmt.Sprintf(`{"conversation_id":%d,"tool":"openloc_delete_custom_palette","args":{"id":"doomed"},"confirm_token":%q}`, convID, token), f.ownerB, f.tenantB.ID); r.Code != http.StatusNotFound {
		t.Errorf("foreign conversation: want 404, got %d", r.Code)
	}
	if p := palettes(); !strings.Contains(p, `"doomed"`) || !strings.Contains(p, `"keeper"`) {
		t.Fatalf("a refused confirm changed palettes: %s", p)
	}

	code, out = post(fmt.Sprintf(`{"conversation_id":%d,"tool":"openloc_delete_custom_palette","args":{"id":"doomed"},"confirm_token":%q}`, convID, token))
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("confirm: want 200 ok=true, got %d %+v", code, out)
	}
	if p := palettes(); strings.Contains(p, `"doomed"`) || !strings.Contains(p, `"keeper"`) {
		t.Errorf("after confirm want only keeper, got %s", p)
	}

	// The outcome is in the conversation, so the next turn knows.
	msgs, err := f.q.ListChatMessages(context.Background(), db.ListChatMessagesParams{
		ConversationID: convID, TenantID: f.tenantA.ID, UserID: f.ownerA.ID,
	})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if last := msgs[len(msgs)-1]; last.Role != "assistant" || !strings.Contains(last.Content, "confirmed delete_custom_palette and it completed") {
		t.Errorf("outcome not recorded: %+v", last)
	}
}

// In a READ-ONLY conversation the model can report an issue: the call only
// previews, the browser gets a confirm card, and the user's click files it.
func TestIntegrationChatReportsIssueInReadOnlyMode(t *testing.T) {
	f := newChatFixture(t, nil)
	f.wireIssues()
	args := `{"kind":"bug","severity":"major","area":"editor","title":"Export does nothing","description":"I click Export and nothing happens.","page":"/projects/1"}`
	f.llm.turns = []*chat.Turn{
		{StopReason: chat.StopToolUse, ToolCalls: []chat.ToolCall{
			{ID: "r1", Name: "openloc_report_issue", Args: json.RawMessage(args)},
		}},
		{Text: "Confirm the report in the card.", StopReason: "end_turn"},
	}
	rec := f.do(t, "POST", "/api/chat", `{"message":"export is broken, please report it"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	turn := readTurn(t, rec.Body.String())
	confirm, convID := turn.confirm, turn.conversationID
	token, _ := confirm["token"].(string)
	if token == "" || !strings.Contains(fmt.Sprint(confirm["summary"]), "Export does nothing") {
		t.Fatalf("confirm card = %+v", confirm)
	}
	count := func() int {
		var n int
		if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM issues WHERE reporter_id = $1`, f.ownerA.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 0 {
		t.Fatalf("the preview filed %d issues", n)
	}

	r := f.do(t, "POST", "/api/chat/confirm", fmt.Sprintf(
		`{"conversation_id":%d,"tool":"openloc_report_issue","args":%s,"confirm_token":%q}`, convID, args, token), f.ownerA, f.tenantA.ID)
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &env); err != nil || r.Code != http.StatusOK || env.Data["ok"] != true {
		t.Fatalf("confirm: %d %s", r.Code, r.Body.String())
	}
	if n := count(); n != 1 {
		t.Errorf("issues after confirm = %d, want 1", n)
	}
}

// When the user sends the assistant's report from the form instead of the
// card, the conversation learns the outcome — only for their own conversation
// and an issue they reported in this org.
func TestIntegrationChatIssueReportedFromTheForm(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{{Text: "Review the report in the form.", StopReason: "end_turn"}})
	rec := f.do(t, "POST", "/api/chat", `{"message":"report a bug"}`, f.ownerA, f.tenantA.ID)
	convID := readTurn(t, rec.Body.String()).conversationID
	svc := f.wireIssues()
	mine, err := svc.Create(context.Background(), issues.Member{TenantID: f.tenantA.ID, UserID: f.ownerA.ID, Role: "owner"},
		issues.NewIssue{Kind: "enhancement", Title: "Mine", Description: "x"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := svc.Create(context.Background(), issues.Member{TenantID: f.tenantB.ID, UserID: f.ownerB.ID, Role: "owner"},
		issues.NewIssue{Kind: "enhancement", Title: "Theirs", Description: "x"})
	if err != nil {
		t.Fatal(err)
	}
	colleague := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "chat-colleague@test.com", "pass", "linguist")
	colleagues, err := svc.Create(context.Background(), issues.Member{TenantID: f.tenantA.ID, UserID: colleague.ID, Role: "linguist"},
		issues.NewIssue{Kind: "enhancement", Title: "A colleague's", Description: "x"})
	if err != nil {
		t.Fatal(err)
	}
	post := func(user db.User, tenantID, conv, issue int64) int {
		return f.do(t, "POST", "/api/chat/issue-reported",
			fmt.Sprintf(`{"conversation_id":%d,"issue_id":%d}`, conv, issue), user, tenantID).Code
	}
	if code := post(f.ownerA, f.tenantA.ID, convID, theirs.ID); code != http.StatusNotFound {
		t.Errorf("another org's issue: %d", code)
	}
	// Even an owner, who sees a colleague's issue, records only their own.
	if code := post(f.ownerA, f.tenantA.ID, convID, colleagues.ID); code != http.StatusNotFound {
		t.Errorf("a colleague's issue in the same org: %d", code)
	}
	if code := post(f.ownerB, f.tenantB.ID, convID, theirs.ID); code != http.StatusNotFound {
		t.Errorf("another user's conversation: %d", code)
	}
	if code := post(f.ownerA, f.tenantA.ID, 0, mine.ID); code != http.StatusBadRequest {
		t.Errorf("no conversation: %d", code)
	}
	if code := post(f.ownerA, f.tenantA.ID, convID, mine.ID); code != http.StatusOK {
		t.Fatalf("own issue: %d", code)
	}
	msgs, err := f.q.ListChatMessages(context.Background(), db.ListChatMessagesParams{
		ConversationID: convID, TenantID: f.tenantA.ID, UserID: f.ownerA.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("sent it as issue #%d", mine.ID)
	if last := msgs[len(msgs)-1]; last.Role != "assistant" || !strings.Contains(last.Content, want) {
		t.Errorf("last message = %+v, want it to contain %q", last, want)
	}
}

// The assistant's check of a report travels to the browser with the card: the
// form proves it with the verification token, which the model never sees. A
// confirmed call keeps the check too.
func TestIntegrationChatIssueCheckTokenGoesToTheBrowserOnly(t *testing.T) {
	f := newChatFixture(t, nil)
	f.wireIssues()
	args := `{"kind":"bug","severity":"major","area":"editor","title":"Editor stops at 100","description":"The editor lists 100 of 120 segments.","verification":"verified","verification_note":"Job 9 has 120 segments; the editor shows 100."}`
	f.llm.turns = []*chat.Turn{
		{StopReason: chat.StopToolUse, ToolCalls: []chat.ToolCall{
			{ID: "r1", Name: "openloc_report_issue", Args: json.RawMessage(args)},
		}},
		{Text: "Have a look in the form.", StopReason: "end_turn"},
	}
	rec := f.do(t, "POST", "/api/chat", `{"message":"the editor stops at 100"}`, f.ownerA, f.tenantA.ID)
	turn := readTurn(t, rec.Body.String())
	confirm, convID := turn.confirm, turn.conversationID
	checkToken, _ := confirm["verification_token"].(string)
	if checkToken == "" {
		t.Fatalf("the card carries no verification token: %+v", confirm)
	}
	if len(f.llm.seen) < 2 {
		t.Fatalf("the model was called %d times", len(f.llm.seen))
	}
	for _, m := range f.llm.seen[1].Messages {
		for _, tr := range m.ToolResults {
			if strings.Contains(tr.Content, checkToken) || strings.Contains(tr.Content, "verification_token") {
				t.Errorf("the model saw the verification token: %s", tr.Content)
			}
		}
	}

	r := f.do(t, "POST", "/api/chat/confirm", fmt.Sprintf(
		`{"conversation_id":%d,"tool":"openloc_report_issue","args":%s,"confirm_token":%q}`, convID, args, confirm["token"]), f.ownerA, f.tenantA.ID)
	if r.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", r.Code, r.Body.String())
	}
	var verdict, note string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT chat_verification, chat_verification_note FROM issues WHERE reporter_id = $1`, f.ownerA.ID).Scan(&verdict, &note); err != nil {
		t.Fatal(err)
	}
	if verdict != "verified" || note != "Job 9 has 120 segments; the editor shows 100." {
		t.Errorf("stored check = %q / %q", verdict, note)
	}
}

// The check the card carries lands on the report the form sends: the card's
// fields and token, posted as the report form posts them (field names shared
// with web/src/api/issues.test.ts), store the assistant's check.
func TestIntegrationChatIssueCheckSurvivesTheReportForm(t *testing.T) {
	f := newChatFixture(t, nil)
	svc := f.wireIssues()
	args := `{"kind":"bug","severity":"major","area":"editor","title":"Editor stops at 100","description":"The editor lists 100 of 120 segments.","verification":"verified","verification_note":"Job 9 has 120 segments; the editor shows 100."}`
	f.llm.turns = []*chat.Turn{
		{StopReason: chat.StopToolUse, ToolCalls: []chat.ToolCall{
			{ID: "r1", Name: "openloc_report_issue", Args: json.RawMessage(args)},
		}},
		{Text: "Have a look in the form.", StopReason: "end_turn"},
	}
	rec := f.do(t, "POST", "/api/chat", `{"message":"the editor stops at 100"}`, f.ownerA, f.tenantA.ID)
	confirm := readTurn(t, rec.Body.String()).confirm
	token, _ := confirm["verification_token"].(string)
	if token == "" {
		t.Fatalf("the card carries no verification token: %+v", confirm)
	}

	raw, err := os.ReadFile("../issues/testdata/report_form_check.json")
	if err != nil {
		t.Fatal(err)
	}
	var names struct{ Result, Note, Token string }
	if err := json.Unmarshal(raw, &names); err != nil {
		t.Fatal(err)
	}
	var card map[string]string
	if err := json.Unmarshal([]byte(args), &card); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for k, v := range map[string]string{
		"kind": card["kind"], "severity": card["severity"], "area": card["area"],
		"title": card["title"], "description": card["description"], "context": "{}",
		names.Result: card["verification"], names.Note: card["verification_note"], names.Token: token,
	} {
		if err := form.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/issues", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: f.tenantA.ID, UserID: f.ownerA.ID, Role: "owner"}))
	created := httptest.NewRecorder()
	issues.NewHandler(svc).HandleCreate(created, req)
	if created.Code != http.StatusCreated {
		t.Fatalf("report form: %d %s", created.Code, created.Body.String())
	}
	var verdict, note string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT chat_verification, chat_verification_note FROM issues WHERE reporter_id = $1`, f.ownerA.ID).Scan(&verdict, &note); err != nil {
		t.Fatal(err)
	}
	if verdict != "verified" || note != card["verification_note"] {
		t.Errorf("stored check = %q / %q", verdict, note)
	}
}
