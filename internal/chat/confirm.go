package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Per-call confirmation for guarded MCP tools.
//
// A guarded tool's first call returns a preview and a confirm_token. The loop
// keeps the token away from the model and sends it to the browser in a
// confirm_required event. The panel shows a card; Confirm posts the token to
// /api/chat/confirm, which runs the call. The model can only ask, never
// confirm.

// ConfirmRequest is the payload of a confirm_required event.
type ConfirmRequest struct {
	Tool      string          `json:"tool"`
	Args      json.RawMessage `json:"args"`
	Summary   string          `json:"summary"`
	Effects   []string        `json:"effects,omitempty"`
	Token     string          `json:"token"`
	ExpiresAt string          `json:"expires_at"`
	// VerificationToken goes with an issue report the user sends from the
	// report form: it proves the assistant's check to the server.
	VerificationToken string `json:"verification_token,omitempty"`
}

// guardedPreview is the part of a guarded tool's result the loop reads.
type guardedPreview struct {
	Status       string `json:"status"`
	ConfirmToken string `json:"confirm_token"`
	ExpiresAt    string `json:"expires_at"`
	Preview      *struct {
		Summary string   `json:"summary"`
		Effects []string `json:"effects"`
	} `json:"preview"`
}

const previewNoteForModel = "Nothing has changed yet. The user now sees a confirmation card " +
	"with this preview and will confirm or cancel it there. Do not call this tool again " +
	"for the same change. Tell the user briefly what will happen and that they can confirm in the card."

// asConfirmRequest recognises a guarded tool's preview result. It returns the
// browser payload plus the text the model sees instead of the raw result:
// the preview without the token.
func asConfirmRequest(tc ToolCall, res *sdk.CallToolResult) (*ConfirmRequest, string, bool) {
	if res == nil || res.IsError || res.StructuredContent == nil {
		return nil, "", false
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return nil, "", false
	}
	var g guardedPreview
	if err := json.Unmarshal(raw, &g); err != nil || g.Status != "needs_confirmation" ||
		g.ConfirmToken == "" || g.Preview == nil {
		return nil, "", false
	}
	// Read on its own, so the preview the model sees below never carries it.
	var tokens struct {
		Preview struct {
			VerificationToken string `json:"verification_token"`
		} `json:"preview"`
	}
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return nil, "", false
	}
	args := tc.Args
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	cr := &ConfirmRequest{
		Tool:              tc.Name,
		Args:              args,
		Summary:           g.Preview.Summary,
		Effects:           g.Preview.Effects,
		Token:             g.ConfirmToken,
		ExpiresAt:         g.ExpiresAt,
		VerificationToken: tokens.Preview.VerificationToken,
	}
	forModel, err := json.Marshal(map[string]any{
		"status":  "needs_confirmation",
		"preview": g.Preview,
		"note":    previewNoteForModel,
	})
	if err != nil {
		return nil, "", false
	}
	return cr, string(forModel), true
}

// carriesConfirmToken reports whether the model put a confirm_token in its
// arguments. It never receives one, so this is a guess or an injection.
func carriesConfirmToken(args json.RawMessage) bool {
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return false
	}
	v, ok := m["confirm_token"]
	return ok && v != nil && v != ""
}

type confirmRequestBody struct {
	ConversationID int64          `json:"conversation_id"`
	Tool           string         `json:"tool"`
	Args           map[string]any `json:"args"`
	ConfirmToken   string         `json:"confirm_token"`
}

// HandleConfirm is POST /api/chat/confirm: the user clicked Confirm on a card.
// The MCP guard checks the token against the tool, the user and the exact
// arguments, so a tampered request fails there. The outcome is stored in the
// conversation so the model sees it on the next turn.
func (h *Handler) HandleConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// auth.Middleware already refuses this; the chat holds the line itself.
	if auth.ReadOnlyKey(ctx) {
		auth.WriteReadOnlyKey(w)
		return
	}
	var req confirmRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid JSON body")
		return
	}
	if req.ConversationID == 0 || req.Tool == "" || req.ConfirmToken == "" {
		httputil.BadRequest(w, "conversation_id, tool and confirm_token are required")
		return
	}
	conv, err := h.q.GetChatConversation(ctx, db.GetChatConversationParams{
		ID: req.ConversationID, TenantID: claims.TenantID, UserID: claims.UserID,
	})
	if err != nil {
		httputil.DBError(w, err, "conversation")
		return
	}

	// The click is the user's consent for this one call.
	session, err := h.mcpSrv.ConnectInProcess(auth.WithWriteGrant(ctx), claims.Role)
	if err != nil {
		logging.Error(ctx, err, "connecting the assistant MCP session failed", "chat-confirm-mcp-connect-failed")
		httputil.ServerError(w, "start", "assistant session")
		return
	}
	defer session.Close()

	args := map[string]any{}
	for k, v := range req.Args {
		args[k] = v
	}
	args["confirm_token"] = req.ConfirmToken
	res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: req.Tool, Arguments: args})
	if err != nil {
		logging.Error(ctx, err, "confirmed tool call failed", "chat-confirm-call-failed", conversationIDAttr(conv.ID), logging.Label("tool", req.Tool))
		httputil.ServerError(w, "run", "confirmed action")
		return
	}

	okCall := !res.IsError
	label := strings.TrimPrefix(req.Tool, "openloc_")
	message := fmt.Sprintf("The user confirmed %s and it completed.", label)
	if !okCall {
		message = fmt.Sprintf("The user confirmed %s, but it failed: %s", label, resultText(res))
	}
	if _, err := h.persistAssistant(r, conv, &Result{
		Text:      message,
		ToolCalls: []ToolCallSummary{{Name: req.Tool, OK: okCall}},
	}); err != nil {
		logging.Error(ctx, err, "saving the confirmed call outcome failed", "chat-confirm-persist-failed", conversationIDAttr(conv.ID))
	}
	httputil.Data(w, map[string]any{"ok": okCall, "message": message})
}

func resultText(res *sdk.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// HandleIssueReported is POST /api/chat/issue-reported: the user sent the
// assistant's issue report from the report form instead of the confirm card.
// The outcome goes into the conversation, as HandleConfirm records a card's,
// so the model does not ask again. The issue must be one the caller reported
// in this org; the message is written here, never taken from the client.
func (h *Handler) HandleIssueReported(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		ConversationID int64 `json:"conversation_id"`
		IssueID        int64 `json:"issue_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid JSON body")
		return
	}
	if req.ConversationID <= 0 || req.IssueID <= 0 {
		httputil.BadRequest(w, "conversation_id and issue_id are required")
		return
	}
	conv, err := h.q.GetChatConversation(ctx, db.GetChatConversationParams{
		ID: req.ConversationID, TenantID: claims.TenantID, UserID: claims.UserID,
	})
	if err != nil {
		httputil.DBError(w, err, "conversation")
		return
	}
	if _, err := h.q.GetMemberIssue(ctx, db.GetMemberIssueParams{
		ID: req.IssueID, TenantID: claims.TenantID, ViewerID: claims.UserID,
	}); err != nil {
		httputil.DBError(w, err, "issue")
		return
	}
	message := fmt.Sprintf("The user reviewed the report in the form and sent it as issue #%d (/issues/%d).",
		req.IssueID, req.IssueID)
	if _, err := h.persistAssistant(r, conv, &Result{
		Text:      message,
		ToolCalls: []ToolCallSummary{{Name: "openloc_report_issue", OK: true}},
	}); err != nil {
		logging.Error(ctx, err, "saving the reported issue outcome failed", "chat-issue-reported-persist-failed", conversationIDAttr(conv.ID))
		httputil.ServerError(w, "save", "outcome")
		return
	}
	httputil.Data(w, map[string]any{"ok": true, "message": message})
}
