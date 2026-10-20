package chat

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Conversation history endpoints: the panel lists a user's conversations and
// reloads one after a page refresh. Every read is scoped to tenant and user.

// HandleListConversations is GET /api/chat/conversations.
func (h *Handler) HandleListConversations(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	rows, err := h.q.ListChatConversations(r.Context(), db.ListChatConversationsParams{
		TenantID: claims.TenantID,
		UserID:   claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "listing conversations failed", "chat-conversations-list-failed")
		httputil.ServerError(w, "list", "conversations")
		return
	}
	httputil.Data(w, rows)
}

// HandleGetMessages is GET /api/chat/conversations/{id}/messages.
func (h *Handler) HandleGetMessages(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "id")
	if err != nil {
		httputil.BadRequest(w, "invalid conversation id")
		return
	}
	// Ownership check via the scoped conversation fetch; the message query is
	// also scoped, but a foreign id must 404 here, not return [].
	if _, err := h.q.GetChatConversation(r.Context(), db.GetChatConversationParams{
		ID: id, TenantID: claims.TenantID, UserID: claims.UserID,
	}); err != nil {
		httputil.DBError(w, err, "conversation")
		return
	}
	rows, err := h.q.ListChatMessages(r.Context(), db.ListChatMessagesParams{
		ConversationID: id, TenantID: claims.TenantID, UserID: claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "listing conversation messages failed", "chat-messages-list-failed", conversationIDAttr(id))
		httputil.ServerError(w, "list", "messages")
		return
	}
	out := make([]messageView, 0, len(rows))
	for _, m := range rows {
		out = append(out, toMessageView(m))
	}
	httputil.Data(w, out)
}

// messageView is a stored message as the panel reads it. ToolCalls is the
// JSONB column passed through as JSON; the sqlc row holds it as []byte,
// which encoding/json would send as base64.
type messageView struct {
	ID             int64           `json:"id"`
	ConversationID int64           `json:"conversation_id"`
	Role           string          `json:"role"`
	Content        string          `json:"content"`
	ToolCalls      json.RawMessage `json:"tool_calls"`
	CreatedAt      string          `json:"created_at"`
}

func toMessageView(m db.ChatMessage) messageView {
	v := messageView{ID: m.ID, ConversationID: m.ConversationID, Role: m.Role, Content: m.Content}
	if len(m.ToolCalls) > 0 {
		v.ToolCalls = json.RawMessage(m.ToolCalls)
	}
	if m.CreatedAt.Valid {
		v.CreatedAt = m.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	return v
}

// HandleDeleteConversation is DELETE /api/chat/conversations/{id}.
func (h *Handler) HandleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "id")
	if err != nil {
		httputil.BadRequest(w, "invalid conversation id")
		return
	}
	n, err := h.q.DeleteChatConversation(r.Context(), db.DeleteChatConversationParams{
		ID: id, TenantID: claims.TenantID, UserID: claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "deleting a conversation failed", "chat-conversation-delete-failed", conversationIDAttr(id))
		httputil.ServerError(w, "delete", "conversation")
		return
	}
	if n == 0 {
		http.Error(w, `{"error":"conversation not found"}`, http.StatusNotFound)
		return
	}
	httputil.NoContent(w)
}

// conversationIDAttr is the log field for a chat conversation id. It is a
// label because the index template has no dedicated conversation field.
func conversationIDAttr(id int64) slog.Attr {
	return logging.Label("conversation_id", strconv.FormatInt(id, 10))
}
