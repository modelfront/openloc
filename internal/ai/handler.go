// Package ai is the HTTP handler for the AI vendor registry.
// Tenants connect OpenAI / Claude / installed extensions by adding one or more named API
// tokens; each token's key is verified with a live connection test, then stored
// encrypted at rest (AES-256-GCM via internal/secrets).
//
// Hard rule: API keys are NEVER returned to the client. The list endpoint emits
// only token metadata (id, name, connection_id, base_url, key_last4,
// use_for_chat, created_at). Write endpoints are
// owner/admin-only (wired in api.go).
package ai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/secrets"
	"openloc/internal/sliceutil"
)

type Handler struct {
	queries db.Querier
	enc     *secrets.Encryptor
	tester  ConnectionTester
	catalog *Catalog
	ext     Extensions // nil = no extensions
}

func NewHandler(q db.Querier, enc *secrets.Encryptor, tester ConnectionTester, catalog *Catalog) *Handler {
	return &Handler{queries: q, enc: enc, tester: tester, catalog: catalog}
}

// vendorStatus is the ONLY shape returned to the client — no secrets, ever. A
// vendor is "connected" when it has at least one token. Models is the vendor's
// selectable catalog (empty for an extension); each entry carries its own tunable
// parameters, because those differ per model within a vendor
type vendorStatus struct {
	Provider string `json:"provider"`
	// DisplayName is the vendor name users see.
	DisplayName string `json:"display_name"`
	// Extension is set for a vendor backed by an installed extension.
	Extension *extensionInfo `json:"extension,omitempty"`
	Connected bool           `json:"connected"`
	Tokens    []tokenStatus  `json:"tokens"`
	// Connections this vendor can be reached through. Sent
	// alongside the tokens so the settings page renders both lists from one GET
	// and a token's endpoint is resolvable client-side.
	Connections []connectionStatus `json:"connections"`
	Models      []Model            `json:"models"`
	// Why the model list is empty, when it is empty because a fetch failed.
	// Client-safe: it never quotes the provider's response.
	// Empty means "nothing failed" — including the honest case of a key that can
	// reach no models at all.
	ModelsError string `json:"models_error,omitempty"`
}

type tokenStatus struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// The endpoint lives on the token's CONNECTION.
	// Echoed here read-only so a token row can show where it points without a
	// second lookup; writes go through the connection.
	ConnectionID int64  `json:"connection_id"`
	BaseURL      string `json:"base_url,omitempty"`
	KeyLast4     string `json:"key_last4"`
	// The key the built-in chat spends for this vendor. At most one token per
	// vendor carries it; nobody choosing means chat falls back to the oldest.
	UseForChat bool   `json:"use_for_chat"`
	CreatedAt  string `json:"created_at"`
}

// keyHint returns the last 4 characters of an API key — a non-secret hint shown
// in the edit UI so a user can tell which stored key is which. Short keys yield
// whatever they have (still only a fragment for anything real).
func keyHint(apiKey string) string {
	r := []rune(apiKey)
	if len(r) <= 4 {
		return string(r)
	}
	return string(r[len(r)-4:])
}

// maskKey shows the first and last 4 characters and the length: enough to tell
// which key was pasted, never enough to use it. A key under 16 characters shows
// only its length, so at least half of any key stays hidden.
func maskKey(apiKey string) string {
	r := []rune(apiKey)
	if len(r) < 16 {
		return fmt.Sprintf("(%d chars)", len(r))
	}
	return fmt.Sprintf("%s…%s (%d chars)", string(r[:4]), string(r[len(r)-4:]), len(r))
}

// HandleList returns every known vendor with its tokens for the caller's tenant.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	tokens, err := h.queries.ListVendorTokens(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "ai vendors")
		return
	}
	byProvider := make(map[string][]tokenStatus)
	for _, t := range tokens {
		// NOTE: tokenStatus is built here and in the create/update handlers
		// below. A field added to only some of them half-lands silently, so
		// keep all three in step. All three carry UseForChat.
		byProvider[t.Provider] = append(byProvider[t.Provider], tokenStatus{
			ID: t.ID, Name: t.Name, ConnectionID: t.ConnectionID, BaseURL: t.BaseUrl,
			KeyLast4: t.KeyLast4, UseForChat: t.UseForChat,
			CreatedAt: t.CreatedAt.Time.Format(time.RFC3339),
		})
	}
	conns, err := h.queries.ListAIConnections(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "connections")
		return
	}
	connByProvider := make(map[string][]connectionStatus)
	for _, c := range conns {
		connByProvider[c.Provider] = append(connByProvider[c.Provider], connectionStatusFrom(c))
	}
	out := make([]vendorStatus, 0, len(KnownProviders))
	for _, p := range KnownProviders {
		// Emit an empty slice, never nil — a JSON `null` would break the FE's
		// token iteration.
		toks := sliceutil.NonNil(byProvider[p])
		cs := sliceutil.NonNil(connByProvider[p])
		out = append(out, vendorStatus{
			Provider: p, DisplayName: DisplayName(p), Connected: len(toks) > 0, Tokens: toks, Connections: cs,
			Models:      h.catalog.Models(r.Context(), tenantID, p),
			ModelsError: h.catalog.FailureReason(tenantID, p),
		})
	}
	if h.ext != nil {
		exts, err := h.ext.List(r.Context(), tenantID)
		if err != nil {
			httputil.ServerError(w, "list", "custom providers")
			return
		}
		for _, e := range exts {
			p := e.Provider()
			toks := sliceutil.NonNil(byProvider[p])
			cs := sliceutil.NonNil(connByProvider[p])
			out = append(out, vendorStatus{
				Provider: p, DisplayName: e.Manifest.Name, Extension: extensionInfoOf(e),
				Connected: len(toks) > 0, Tokens: toks, Connections: cs, Models: []Model{},
			})
		}
	}
	httputil.Data(w, out)
}

// logKeyTestFailure logs a failed live key check with the key masked (maskKey),
// so support can tell which key was pasted without the log holding a usable
// key.
func logKeyTestFailure(ctx context.Context, err error, provider, apiKey, baseURL string, connID int64) {
	logging.Warn(ctx, err, "api key check failed", "ai-key-test-failed",
		logging.Provider(provider),
		logging.Debugf("api_key", maskKey(apiKey)),
		logging.Debugf("base_url", baseURL),
		logging.Debugf("connection_id", connID))
}

// keyCheckFailed logs a failed key check and answers 400 with the
// plain-language reason, tagged as the API key field's error.
func (h *Handler) keyCheckFailed(w http.ResponseWriter, r *http.Request, err error, provider, apiKey, baseURL string, connID int64) {
	logKeyTestFailure(r.Context(), err, provider, apiKey, baseURL, connID)
	reason := keyCheckReason(provider, err)
	if inst, ok, _ := h.installedExtension(r, provider); ok {
		reason = extensionKeyReason(inst.Manifest.Name, err)
	}
	httputil.FieldError(w, http.StatusBadRequest, "api_key", reason)
}

// duplicateKeyName is the Name field's error when the vendor already has a key
// by that name.
func duplicateKeyName(name, vendorName string) string {
	return fmt.Sprintf("A key named %q already exists for %s. Choose another name.", name, vendorName)
}

// HandleCreateToken verifies + stores a new API token for a vendor. Owner/admin.
// Body: { name, api_key (required), base_url (optional) }. On a failed live test
// it returns 400; only on success is the (encrypted) key persisted.
func (h *Handler) HandleCreateToken(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	if !h.requireProvider(w, r, provider) {
		return
	}
	var req struct {
		Name   string `json:"name"`
		APIKey string `json:"api_key"`
		// Which connection this key authenticates. Required: a token has no
		// endpoint of its own.
		ConnectionID int64 `json:"connection_id"`
		// Make this the key the built-in chat spends for the vendor.
		UseForChat bool `json:"use_for_chat"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "api_key is required")
		return
	}
	name := strings.TrimSpace(req.Name)
	apiKey := strings.TrimSpace(req.APIKey)
	if name == "" {
		name = DefaultTokenName
	}
	if apiKey == "" {
		httputil.BadRequest(w, "api_key is required")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	conn, err := h.connectionFor(r, tenantID, req.ConnectionID, provider)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	// Test against the CONNECTION's endpoint — the same URL the runtime will use.
	if err := h.testKey(r, provider, apiKey, conn.BaseUrl); err != nil {
		h.keyCheckFailed(w, r, err, provider, apiKey, conn.BaseUrl, conn.ID)
		return
	}
	encrypted, err := h.enc.Encrypt(apiKey)
	if err != nil {
		httputil.ServerError(w, "encrypt", "api key")
		return
	}
	tok, err := h.queries.CreateVendorToken(r.Context(), db.CreateVendorTokenParams{
		VendorID: conn.VendorID, Name: name, ApiKeyEncrypted: encrypted, ConnectionID: conn.ID, KeyLast4: keyHint(apiKey),
	})
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			httputil.FieldError(w, http.StatusConflict, "name", duplicateKeyName(name, h.displayName(r, provider)))
			return
		}
		httputil.ServerError(w, "save", "token")
		return
	}
	if req.UseForChat {
		if err := h.queries.SetChatVendorToken(r.Context(), db.SetChatVendorTokenParams{
			ID: tok.ID, VendorID: conn.VendorID,
		}); err != nil {
			httputil.ServerError(w, "save", "chat token")
			return
		}
	}
	// req.UseForChat, not tok.UseForChat: the row was inserted before the flag
	// was applied, so the returned row still reads false.
	httputil.Created(w, tokenStatus{
		ID: tok.ID, Name: tok.Name, ConnectionID: tok.ConnectionID, BaseURL: conn.BaseUrl,
		KeyLast4: tok.KeyLast4, UseForChat: req.UseForChat,
		CreatedAt: tok.CreatedAt.Time.Format(time.RFC3339),
	})
}

// HandleUpdateToken edits a token's name, connection and chat flag, and
// optionally rotates the key.
// Body: { name, api_key (optional — blank keeps the stored key), connection_id,
// use_for_chat }.
func (h *Handler) HandleUpdateToken(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	if !h.requireProvider(w, r, provider) {
		return
	}
	tokenID, err := httputil.ParseID(r, "tokenID")
	if err != nil {
		httputil.BadRequest(w, "invalid token ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	cur, err := h.queries.GetVendorToken(r.Context(), db.GetVendorTokenParams{ID: tokenID, TenantID: tenantID})
	if err != nil {
		if httputil.DBError(w, err, "token") {
			return
		}
	}
	var req struct {
		Name   string `json:"name"`
		APIKey string `json:"api_key"`
		// Pointer, not value: an ABSENT connection_id keeps the token where it
		// is, while an explicit id moves it. An edit that doesn't resend the
		// field must not silently repoint the token.
		ConnectionID *int64 `json:"connection_id"`
		// Pointer for the same reason: absent leaves the chat choice alone.
		// Setting it true moves the flag off whichever token held it; there is
		// no way to set it FALSE directly, because a vendor with keys always has
		// a chat key — you change it by choosing another.
		UseForChat *bool `json:"use_for_chat"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = DefaultTokenName
	}
	connID := cur.ConnectionID
	if req.ConnectionID != nil {
		connID = *req.ConnectionID
	}
	conn, err := h.connectionFor(r, cur.TenantID, connID, provider)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	baseURL := conn.BaseUrl
	encrypted := cur.ApiKeyEncrypted
	last4 := cur.KeyLast4
	if apiKey := strings.TrimSpace(req.APIKey); apiKey != "" {
		if err := h.testKey(r, provider, apiKey, baseURL); err != nil {
			h.keyCheckFailed(w, r, err, provider, apiKey, baseURL, conn.ID)
			return
		}
		if encrypted, err = h.enc.Encrypt(apiKey); err != nil {
			httputil.ServerError(w, "encrypt", "api key")
			return
		}
		last4 = keyHint(apiKey)
	}
	tok, err := h.queries.UpdateVendorToken(r.Context(), db.UpdateVendorTokenParams{
		ID: tokenID, VendorID: cur.VendorID, Name: name, ApiKeyEncrypted: encrypted, ConnectionID: conn.ID, KeyLast4: last4,
	})
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			httputil.FieldError(w, http.StatusConflict, "name", duplicateKeyName(name, h.displayName(r, provider)))
			return
		}
		httputil.ServerError(w, "update", "token")
		return
	}
	if req.UseForChat != nil && *req.UseForChat {
		if err := h.queries.SetChatVendorToken(r.Context(), db.SetChatVendorTokenParams{
			ID: tok.ID, VendorID: cur.VendorID,
		}); err != nil {
			httputil.ServerError(w, "update", "chat token")
			return
		}
	}
	// Same reason as create: report what was APPLIED, not what the update
	// statement returned before the flag was moved.
	useForChat := cur.UseForChat
	if req.UseForChat != nil {
		useForChat = *req.UseForChat
	}
	httputil.Data(w, tokenStatus{
		ID: tok.ID, Name: tok.Name, ConnectionID: tok.ConnectionID, BaseURL: baseURL,
		KeyLast4: tok.KeyLast4, UseForChat: useForChat,
		CreatedAt: tok.CreatedAt.Time.Format(time.RFC3339),
	})
}

// HandleDeleteToken removes a token; engines that spend it keep their config
// and report unusable.
func (h *Handler) HandleDeleteToken(w http.ResponseWriter, r *http.Request) {
	tokenID, err := httputil.ParseID(r, "tokenID")
	if err != nil {
		httputil.BadRequest(w, "invalid token ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	cur, err := h.queries.GetVendorToken(r.Context(), db.GetVendorTokenParams{ID: tokenID, TenantID: tenantID})
	if err != nil {
		if httputil.DBError(w, err, "token") {
			return
		}
	}
	// Engines that spend this key are NOT a reason to refuse.
	// Both FKs are ON DELETE SET NULL, so each one keeps its name, options and
	// routes and goes "unusable — no API key", which every surface flags.
	if err := h.queries.DeleteVendorToken(r.Context(), db.DeleteVendorTokenParams{ID: tokenID, VendorID: cur.VendorID}); err != nil {
		httputil.ServerError(w, "delete", "token")
		return
	}
	httputil.NoContent(w)
}
