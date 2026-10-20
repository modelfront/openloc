package chat

import (
	"context"
	"net/http"

	"openloc/internal/ai"
	"openloc/internal/auth"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// chatProviders are the providers whose models the picker lists, in display
// order. Extensions are deliberately absent — they run QE+APE, not chat.
var chatProviders = []string{ai.ProviderClaude, ai.ProviderOpenAI}

// adapterFor reports whether the chat runtime can actually DRIVE provider p.
// Claude + OpenAI today; a provider listed here must have an adapter wired in
// defaultLLMFactory. A vendor without one still contributes rows to the picker
// when the org holds its key, greyed and unselectable — the picker, validation
// and endpoint all key off this one function.
func adapterFor(p string) bool {
	return p == ai.ProviderClaude || p == ai.ProviderOpenAI
}

// providerForModel maps a model id back to its vendor. Derived from the id
// rather than looked up, so it holds for a model the tenant's catalog hasn't
// fetched. An unrecognised shape falls back to Claude — unreachable for an id
// validateModel accepted, and it keeps a live turn from running vendorless.
func providerForModel(model string) string {
	if p := ai.ProviderForModelID(model); p != "" {
		return p
	}
	return ai.ProviderClaude
}

// providerLabel is the vendor name as it appears in the UI, for error copy.
func providerLabel(p string) string {
	switch p {
	case ai.ProviderOpenAI:
		return "OpenAI"
	case ai.ProviderClaude:
		return "Claude"
	case "":
		// Unreachable via either adapter (both set Provider) — a guard so a
		// stray ProviderError can't render "Your  API key was rejected".
		return "AI"
	default:
		return p
	}
}

// ChatModel is one row of the picker: a model the org's key can reach, plus
// what the caller's org can do with it right now.
type ChatModel struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Label    string `json:"label"`
	// Connected: the org has a working key for this model's provider.
	Connected bool `json:"connected"`
	// Selectable: connected AND the chat runtime has an adapter — the only
	// state in which POST /api/chat will accept this model.
	Selectable bool `json:"selectable"`
	Default    bool `json:"default"`
}

// HandleListModels is GET /api/chat/models. Deliberately open to every
// authenticated role (unlike the manager-gated AI provider settings): a
// linguist's chat needs the picker too, and this payload carries only model
// names and connected flags — nothing key-shaped.
func (h *Handler) HandleListModels(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	connected, err := h.connectedProviders(r.Context(), claims.TenantID)
	if err != nil {
		logging.Error(r.Context(), err, "chat provider list failed", "chat-list-providers-failed")
		httputil.ServerError(w, "list", "chat models")
		return
	}
	out := make([]ChatModel, 0, 8)
	for _, p := range chatProviders {
		for _, m := range h.catalog.Models(r.Context(), claims.TenantID, p) {
			out = append(out, ChatModel{
				Provider:   p,
				ID:         m.ID,
				Label:      m.Label,
				Connected:  connected[p],
				Selectable: connected[p] && adapterFor(p),
				Default:    m.ID == DefaultModel,
			})
		}
	}
	httputil.Data(w, out)
}

func (h *Handler) connectedProviders(ctx context.Context, tenantID int64) (map[string]bool, error) {
	// A provider is "connected" when its vendor has at least one token.
	toks, err := h.q.ListVendorTokens(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	connected := make(map[string]bool, len(toks))
	for _, t := range toks {
		connected[t.Provider] = true
	}
	return connected, nil
}

// validateModel returns the model to use and whether it is allowed, enforcing
// the same rule the picker shows: in the tenant's catalog + adapter-backed.
// Never trust the client's string — an invalid or adapterless model is a caller
// mistake worth a clear 400, not something to silently coerce to the default.
func (h *Handler) validateModel(ctx context.Context, tenantID int64, requested string) (model string, ok bool) {
	if requested == "" {
		return DefaultModel, true
	}
	p := ai.ProviderForModelID(requested)
	if p == "" || !adapterFor(p) {
		return "", false
	}
	if !h.catalog.Valid(ctx, tenantID, p, requested) {
		return "", false
	}
	return requested, true
}
