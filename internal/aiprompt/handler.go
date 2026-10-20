// Package aiprompt is the HTTP handler for system prompt templates.
// A prompt is its instruction text plus the set of output fields the model is
// expected to emit (status / text / note / comments). Provider, model and
// settings live on the AI engine row, not the prompt. Tenant-scoped; manager-read,
// owner/admin-write (wired in api.go).
package aiprompt

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

type Handler struct {
	queries db.Querier
	// pool wraps a prompt's row and its language additions in one transaction.
	// Nil is allowed (tests that don't write additions); writes then run on the
	// plain querier, mirroring qa.NewService.
	pool *pgxpool.Pool
}

func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{queries: q, pool: pool}
}

// promptInput is the JSON write shape for create + update. Non-pointer scalars
// because a prompt PUT/POST always sends the full resource. Field-compatible
// with service.Input, which holds the validation.
type promptInput struct {
	Name              string             `json:"name"`
	PromptText        string             `json:"prompt_text"`
	OutStatus         bool               `json:"out_status"`
	OutText           bool               `json:"out_text"`
	OutNote           bool               `json:"out_note"`
	OutComments       bool               `json:"out_comments"`
	SegmentContext    []string           `json:"segment_context"`
	LanguageAdditions []LanguageAddition `json:"language_additions"`
}

func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	// Lazy provisioning of the built-in templates — the AI page is
	// the only place prompts are picked, so ensuring here guarantees every
	// tenant sees them. Best-effort: a failure must not blank the list.
	// The demo organization is read-only and ships with its prompts.
	if !auth.IsDemo(r.Context()) {
		if err := EnsureDefaultPrompts(r.Context(), h.queries, tenantID); err != nil {
			logging.Warn(r.Context(), err, "ensuring default AI prompts failed", "aiprompt-ensure-defaults-failed")
		}
	}
	prompts, err := h.queries.ListAIPrompts(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI prompts")
		return
	}
	httputil.Data(w, listResponse(r.Context(), h.queries, prompts))
}

func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	var in promptInput
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	prompt, additions, err := CreateTx(r.Context(), h.queries, h.pool, tenantID, Input(in))
	if err != nil {
		// Validation covers the duplicate-name case too, so it reaches the
		// caller as an actionable 400 rather than an opaque 500.
		if msg, ok := AsValidation(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		httputil.ServiceError(w, err, "create", "AI prompt")
		return
	}
	httputil.Created(w, toResponse(prompt, additions))
}

func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "promptID")
	if err != nil {
		httputil.BadRequest(w, "invalid prompt ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())

	var in promptInput
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	// Tenant-scoped: a foreign id is a 404 like a missing one.
	prompt, additions, err := Edit(r.Context(), h.queries, h.pool, tenantID, id, Input(in))
	if msg, ok := AsValidation(err); ok {
		httputil.BadRequest(w, msg)
		return
	}
	if httputil.DBError(w, err, "AI prompt") {
		return
	}
	httputil.Data(w, toResponse(prompt, additions))
}

// HandleDelete removes a prompt; 409 for a built-in or while an AI engine still
// uses it (see Delete).
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "promptID")
	if err != nil {
		httputil.BadRequest(w, "invalid prompt ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if err := Delete(r.Context(), h.queries, tenantID, id); err != nil {
		httputil.ServiceError(w, err, "delete", "AI prompt")
		return
	}
	httputil.NoContent(w)
}
