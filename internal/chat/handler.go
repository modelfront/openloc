package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"openloc/internal/ai"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack"
	"openloc/internal/file"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/mcp"
	"openloc/internal/secrets"
)

// turnTimeout bounds one whole user message: all model iterations and tool
// calls together. Generous because a multi-tool answer legitimately takes a
// while; the per-request ctx also dies with the connection if the user
// navigates away.
const turnTimeout = 120 * time.Second

// keepaliveInterval is the shared stream keepalive. A var, not a const, so the
// heartbeat integration test can shrink it instead of sleeping 25 real
// seconds.
var keepaliveInterval = httputil.SSEKeepalive

// SetKeepaliveIntervalForTest shrinks the ping cadence so the heartbeat
// integration test doesn't sleep 25 real seconds. Returns a restore func.
// Test-only by contract; production callers have no reason to touch this.
func SetKeepaliveIntervalForTest(d time.Duration) (restore func()) {
	old := keepaliveInterval
	keepaliveInterval = d
	return func() { keepaliveInterval = old }
}

// newLLM builds the model client for a turn. Swapped by tests to avoid the
// network.
type newLLM func(provider, apiKey, baseURL, model string) LLM

// Handler serves the built-in chat.
type Handler struct {
	q      db.Querier
	enc    *secrets.Encryptor
	mcpSrv *mcp.Server
	newLLM newLLM
	// draining is closed when the process begins shutting down; see the
	// SSEHandler field of the same name. A turn is allowed up to turnTimeout
	// (120s), far beyond any pod termination grace period, so a turn in flight
	// during a deploy is going to be killed either way — better to end it with
	// an SSE error the client already knows how to retry than to hold shutdown
	// open until the deadline and then get SIGKILLed mid-stream.
	draining <-chan struct{}
	// catalog answers "which models may this tenant pick" for the picker and
	// for request validation.
	catalog *ai.Catalog
	// storage keeps assistant uploads (uploads.go); nil disables the route.
	storage file.BlobStore
	// trustedSpeechBase is the only endpoint whose audio fills the shared
	// tour-speech cache (speech.go).
	trustedSpeechBase string
	// playground says the Playground is on: saved tours run only there.
	playground bool
	// issueReports: the deployment takes issue reports (SetIssueReports).
	issueReports bool
}

func NewHandler(q db.Querier, enc *secrets.Encryptor, mcpSrv *mcp.Server, draining <-chan struct{}, catalog *ai.Catalog) *Handler {
	return &Handler{q: q, enc: enc, mcpSrv: mcpSrv, newLLM: defaultLLMFactory, draining: draining, catalog: catalog,
		trustedSpeechBase: defaultOpenAIBaseURL}
}

// defaultLLMFactory picks the adapter for the model's provider. Adding a
// provider is this switch plus `adapterFor` — everything else (picker,
// validation, key + endpoint resolution) keys off the tenant's model catalog.
func defaultLLMFactory(provider, apiKey, baseURL, model string) LLM {
	switch provider {
	case ai.ProviderOpenAI:
		return NewOpenAI(apiKey, baseURL, model)
	default:
		return NewAnthropic(apiKey, baseURL, model)
	}
}

// SetLLMFactory replaces how the model client is built. Tests inject fakes
// here.
func (h *Handler) SetLLMFactory(f func(provider, apiKey, baseURL, model string) LLM) {
	h.newLLM = f
}

type sendRequest struct {
	ConversationID int64  `json:"conversation_id,omitempty"`
	Message        string `json:"message"`
	// Model is optional; empty means DefaultModel. Validated against the
	// tenant's fetched catalog + adapter availability — see validateModel.
	Model string `json:"model,omitempty"`
	// AllowWrites is the mode picker: the user pre-authorizing this
	// message's turn to use write tools. The user consenting to their own
	// session — role/tenant gates are unchanged.
	AllowWrites bool `json:"allow_writes,omitempty"`
	// PagePath is the browser's current location — a HINT for the
	// system prompt, parsed against the server-side page registry. Ids in it
	// grant nothing; unknown paths are silently ignored.
	PagePath string `json:"page_path,omitempty"`
	// VisibleAnchors are the data-guide ids on screen —
	// also a hint: filtered to catalog ids, used only in the prompt.
	VisibleAnchors []string `json:"visible_anchors,omitempty"`
	// ScreenshotUploadID names the screenshot the user approved for this
	// message (screenshot.go): the model sees it with this message only.
	ScreenshotUploadID string `json:"screenshot_upload_id,omitempty"`
}

// HandleSend is POST /api/chat: one user message in, an SSE stream of events
// out. Event names: text, tool_start, tool_end, done, error.
func (h *Handler) HandleSend(w http.ResponseWriter, r *http.Request) {
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

	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid JSON body")
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		httputil.BadRequest(w, "message is required")
		return
	}
	if len(req.Message) > 8000 {
		httputil.BadRequest(w, "message too long (max 8000 characters)")
		return
	}
	model, ok := h.validateModel(ctx, claims.TenantID, req.Model)
	if !ok {
		httputil.BadRequest(w, "model is not available for chat — pick one from /api/chat/models")
		return
	}

	// The tenant's own key for the SELECTED model's vendor. Missing
	// key is a setup problem, not an error — 412 with a stable code the FE maps
	// to a settings link.
	provider := providerForModel(model)
	apiKey, baseURL, err := h.vendorKey(r, provider)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// House envelope via ErrorCode, so the FE's error parsing stays in
			// step.
			// The code stays `no_claude_key` for wire compatibility with the FE.
			httputil.ErrorCode(w, http.StatusPreconditionFailed, "no_claude_key",
				fmt.Sprintf("No %s API key is configured — connect one in AI settings → Providers.", providerLabel(provider)))
			return
		}
		logging.Error(r.Context(), err, "chat provider key load failed", "chat-key-load-failed")
		httputil.ServerError(w, "load", "AI provider key")
		return
	}

	title := conversationTitle(req.Message)
	var screenshot *Image
	if req.ScreenshotUploadID != "" {
		screenshot, err = h.loadUploadImage(ctx, claims, req.ScreenshotUploadID)
		if errors.Is(err, errNotScreenshot) {
			httputil.BadRequest(w, "The screenshot is gone or is not an image. Take it again.")
			return
		}
		if err != nil {
			logging.Error(ctx, err, "loading a chat screenshot failed", "chat-screenshot-load-failed")
			httputil.ServerError(w, "load", "screenshot")
			return
		}
		req.Message = withScreenshotMarker(req.Message, req.ScreenshotUploadID)
	}

	conv, err := h.conversationFor(r, &req, claims, title)
	if err != nil {
		httputil.DBError(w, err, "conversation")
		return
	}

	userMsg, err := h.q.InsertChatMessage(ctx, db.InsertChatMessageParams{
		ConversationID: conv.ID,
		Role:           "user",
		Content:        req.Message,
	})
	if err != nil {
		logging.Error(r.Context(), err, "chat user message not saved", "chat-user-message-failed", conversationIDAttr(conv.ID))
		httputil.ServerError(w, "save", "message")
		return
	}

	history, err := h.replayHistory(r, conv.ID, claims)
	if err != nil {
		logging.Error(r.Context(), err, "chat history load failed", "chat-history-load-failed", conversationIDAttr(conv.ID))
		httputil.ServerError(w, "load", "history")
		return
	}
	// The newest row is this message: the screenshot goes with it alone.
	if n := len(history); screenshot != nil && n > 0 && history[n-1].Role == "user" {
		history[n-1].Image = screenshot
	}

	// The in-process MCP session rides r.Context(), which auth.Middleware has
	// already stamped with claims — the same identity the REST API trusts.
	// In write mode the connect context also carries the write grant, the
	// only non-PAT way through the MCP write gate.
	sessionCtx := ctx
	if req.AllowWrites {
		sessionCtx = auth.WithWriteGrant(sessionCtx)
	}
	session, err := h.mcpSrv.ConnectInProcess(sessionCtx, claims.Role)
	if err != nil {
		logging.Error(r.Context(), err, "chat MCP session start failed", "chat-mcp-connect-failed")
		httputil.ServerError(w, "start", "assistant session")
		return
	}
	defer session.Close()

	// From here on the response is a stream: errors become SSE error events,
	// because the 200 header is already gone.
	sse, ok := httputil.StartSSE(w)
	if !ok {
		return
	}

	// One mutex for every write: emit runs on the loop's goroutine, the
	// keepalive ticker on its own, and interleaved partial SSE frames would
	// corrupt the stream.
	var wmu sync.Mutex
	emit := func(name string, payload any) {
		wmu.Lock()
		defer wmu.Unlock()
		if err := sse.Event(name, payload); err != nil {
			// The client is gone; the turn's context ends with the request.
			return
		}
	}

	// Keepalive comments, same rationale as notification/sse_handler.go: idle
	// proxies (ALB defaults to 60s) kill silent streams, and a model turn
	// carrying many tool results can generate for minutes without a text
	// delta. A comment line is invisible to the event parser but keeps bytes
	// flowing, so the load balancer does not cut an idle stream — the FE also
	// treats any received chunk as proof of life.
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		defer errtrack.Monitor(ctx, "chat", "sse-keepalive")
		ticker := time.NewTicker(keepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-ticker.C:
				wmu.Lock()
				err := sse.Comment("ping")
				wmu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()

	loopCtx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()

	// Cut the turn short when the process starts draining. Run below is a
	// blocking call, so cancelling its context is the only way to interrupt it;
	// the error path already emits an SSE error event the FE handles.
	if h.draining != nil {
		go func() {
			defer errtrack.Monitor(loopCtx, "chat", "drain-watch")
			select {
			case <-h.draining:
				cancel()
			case <-loopCtx.Done():
				// Turn finished (or timed out) first — nothing to do.
			}
		}()
	}

	llm := h.newLLM(provider, apiKey, baseURL, model)
	result, err := Run(loopCtx, llm, session, systemPrompt(claims, req.AllowWrites, h.issueReports, req.PagePath, req.VisibleAnchors), history, RunOptions{AllowWrites: req.AllowWrites, SavedTours: h.playground, IssueReports: h.issueReports, PagePath: req.PagePath}, func(ev Event) {
		emit(ev.Type, ev)
	})
	if err != nil {
		emit("error", map[string]string{"error": userFacingLLMError(err)})
		logging.Error(r.Context(), err, "chat turn failed", "chat-turn-failed", conversationIDAttr(conv.ID))
		return
	}

	assistantMsg, err := h.persistAssistant(r, conv, result)
	if err != nil {
		// The user already saw the streamed answer; losing persistence is
		// logged but must not turn a delivered answer into an error event.
		logging.Error(r.Context(), err, "chat assistant message not saved", "chat-assistant-message-failed", conversationIDAttr(conv.ID))
	}

	done := map[string]any{
		"conversation_id": conv.ID,
		"user_message_id": userMsg.ID,
		"input_tokens":    result.InputTokens,
		"output_tokens":   result.OutputTokens,
	}
	if assistantMsg != nil {
		done["assistant_message_id"] = assistantMsg.ID
	}
	emit("done", done)
}

// vendorKey loads and decrypts the tenant's key for one vendor, plus that
// token's custom endpoint if it has one — a tenant can point OpenAI at an Azure
// resource or a gateway, and chat honours it like the
// translate path does.
func (h *Handler) vendorKey(r *http.Request, provider string) (apiKey, baseURL string, err error) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	return h.tenantVendorKey(r.Context(), claims.TenantID, provider)
}

func (h *Handler) tenantVendorKey(ctx context.Context, tenantID int64, provider string) (apiKey, baseURL string, err error) {
	// The token the tenant PICKED for chat, falling back to the oldest when
	// nobody picked, so a tenant holding a cheap key and an expensive one
	// chooses which one chat spends.
	tok, err := h.q.GetChatVendorToken(ctx, db.GetChatVendorTokenParams{
		TenantID: tenantID,
		Provider: provider,
	})
	if err != nil {
		return "", "", err
	}
	key, err := h.enc.Decrypt(tok.ApiKeyEncrypted)
	if err != nil {
		return "", "", err
	}
	baseURL = strings.TrimSpace(tok.BaseUrl)
	return key, baseURL, nil
}

// conversationFor loads (and ownership-checks) or creates the conversation;
// a new one is named title.
func (h *Handler) conversationFor(r *http.Request, req *sendRequest, claims *auth.Claims, title string) (db.ChatConversation, error) {
	ctx := r.Context()
	if req.ConversationID != 0 {
		return h.q.GetChatConversation(ctx, db.GetChatConversationParams{
			ID:       req.ConversationID,
			TenantID: claims.TenantID,
			UserID:   claims.UserID,
		})
	}
	return h.q.CreateChatConversation(ctx, db.CreateChatConversationParams{
		TenantID: claims.TenantID,
		UserID:   claims.UserID,
		Title:    title,
	})
}

// conversationTitle is the first 60 characters of the user's message.
func conversationTitle(message string) string {
	if r := []rune(message); len(r) > 60 {
		return string(r[:60])
	}
	return message
}

// replayHistory converts persisted messages to text-only neutral history,
// excluding nothing: the just-inserted user message is the last row and doubles
// as the prompt. Only the newest maxHistoryMessages replay.
func (h *Handler) replayHistory(r *http.Request, convID int64, claims *auth.Claims) ([]Message, error) {
	rows, err := h.q.ListChatMessages(r.Context(), db.ListChatMessagesParams{
		ConversationID: convID,
		TenantID:       claims.TenantID,
		UserID:         claims.UserID,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) > maxHistoryMessages {
		rows = rows[len(rows)-maxHistoryMessages:]
	}
	msgs := make([]Message, 0, len(rows))
	for _, row := range rows {
		if row.Content == "" {
			continue
		}
		msgs = append(msgs, Message{Role: row.Role, Text: row.Content})
	}
	return msgs, nil
}

func (h *Handler) persistAssistant(r *http.Request, conv db.ChatConversation, result *Result) (*db.ChatMessage, error) {
	var toolCalls []byte
	if len(result.ToolCalls) > 0 {
		var err error
		toolCalls, err = json.Marshal(result.ToolCalls)
		if err != nil {
			toolCalls = nil
		}
	}
	msg, err := h.q.InsertChatMessage(r.Context(), db.InsertChatMessageParams{
		ConversationID: conv.ID,
		Role:           "assistant",
		Content:        result.Text,
		ToolCalls:      toolCalls,
		InputTokens:    result.InputTokens,
		OutputTokens:   result.OutputTokens,
	})
	if err != nil {
		return nil, err
	}
	if err := h.q.TouchChatConversation(r.Context(), db.TouchChatConversationParams{
		ID: conv.ID, TenantID: conv.TenantID,
	}); err != nil {
		logging.Warn(r.Context(), err, "chat conversation touch failed", "chat-touch-failed", conversationIDAttr(conv.ID))
	}
	return &msg, nil
}

// systemPrompt grounds the assistant. Deliberately brief: tool descriptions
// carry the per-tool guidance; this sets identity, honesty, and today's date
// (deadline questions are meaningless without it).
func systemPrompt(claims *auth.Claims, allowWrites, issueReports bool, pagePath string, visibleAnchors []string) SystemPrompt {
	mode := "You are in READ-ONLY mode: you cannot change any data. If the user asks you " +
		"to add a comment, lock a segment, save a draft, or change anything, tell them to " +
		"switch the mode picker next to the message box from \"Read-only\" to \"Allow write\" and ask again."
	if issueReports {
		mode += " Reporting an issue to the support team (openloc_report_issue) is the exception: it works in this mode."
	}
	if allowWrites {
		mode = "Write tools are enabled — the user switched this conversation to write mode. " +
			"Drafts are never auto-approved. Before saving a draft over existing text, re-read the segment and " +
			"pass expected_updated_at so you never silently overwrite concurrent human edits. " +
			"Deletes and edits to shared settings first return a preview: the user confirms or cancels it " +
			"in a card, so describe what will happen and let them decide."
	}
	// Page context: one line, only when the path maps to a known
	// page. Goes with a navigation hint so the model knows it can act on it.
	pageLine := ""
	if desc := DescribePath(pagePath); desc != "" {
		pageLine = fmt.Sprintf("\n\nThe user is currently viewing %s — \"this\"/\"here\" in their questions likely refers to it. You can also open app pages for them with the navigate_to tool when it helps (say what you're opening).", desc)
	}
	if line := visibleAnchorsLine(visibleAnchors); line != "" {
		pageLine += "\n\n" + line
	}
	stable := fmt.Sprintf(`You are the built-in assistant of OpenLoc, a translation management system. Today is %s.

Answer questions about the user's projects, jobs, translations, quality and terminology using the tools. Use tools for any factual claim about their data — never guess IDs, counts, or deadlines. If a tool returns nothing, say so plainly rather than inventing an answer. Numbers you compute (costs, word counts, percentages) should show their working.

When the user asks how the app works or where something is, answer from the docs pages (openloc_search_docs) and show them: point at one element with highlight; to show several, send them all at once in a single start_tour (look up any ids first) — never one highlight per message. When you ask the user to choose, end with suggest_replies so they can answer with one click.

%s The user's role is %q.

Keep answers focused; prefer a short direct answer with the key numbers over an exhaustive dump.`,
		time.Now().Format("2006-01-02"), mode, claims.Role)
	if line := playbooksLine(claims.Role, issueReports); line != "" {
		stable += "\n\n" + line
	}
	return SystemPrompt{Stable: stable, Volatile: pageLine}
}

// userFacingLLMError converts provider failures into actionable messages
// without leaking raw provider payloads (no err.Error() pass-through).
func userFacingLLMError(err error) string {
	var perr *ai.ProviderError
	if errors.As(err, &perr) {
		// Name the vendor that actually failed, so the user reconnects the
		// right key.
		vendor := providerLabel(perr.Provider)
		switch perr.Kind {
		case ai.ProviderErrAuth:
			return fmt.Sprintf("Your %s API key was rejected — reconnect it in AI settings → Providers.", vendor)
		case ai.ProviderErrQuota:
			return fmt.Sprintf("Your %s account is out of credit.", vendor)
		case ai.ProviderErrRateLimit:
			return fmt.Sprintf("%s is rate-limiting requests — try again in a moment.", vendor)
		case ai.ProviderErrUnreachable:
			return fmt.Sprintf("Could not reach %s — check your network and try again.", vendor)
		}
		return "The AI provider returned an error — try again."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "This question took too long to answer — try a narrower one."
	}
	// The only cancellation a still-connected client can see is the shutdown
	// drain cutting the turn short (client disconnects cancel too, but nobody
	// reads a message written to a dead connection). Retrying is exactly right:
	// the reconnect lands on a replica that is up.
	if errors.Is(err, context.Canceled) {
		return "This answer was interrupted — please try again."
	}
	return "Something went wrong answering this — try again."
}
