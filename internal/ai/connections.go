package ai

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// AI connections — WHERE we talk to a vendor, separate from
// WHAT authenticates. A connection is {vendor, name, endpoint}; tokens belong to
// one, so two keys for the same Azure resource share one URL and a key
// rotation cannot lose it.
//
// A connection may exist with NO tokens: configure the endpoint now, add the key
// later. `token_count` is what lets the UI say "configured, not authenticated".

type connectionStatus struct {
	ID       int64  `json:"id"`
	Provider string `json:"provider"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	// A connection is "live" when TokenCount > 0 — there is no separate switch.
	// Nothing can call an endpoint it has no credential for, so a second flag
	// would only ever be a way for the two to disagree.
	TokenCount int32  `json:"token_count"`
	CreatedAt  string `json:"created_at"`
}

// connectionFor resolves a connection id for a write, enforcing both guards a
// caller could otherwise skip: it must belong to THIS tenant, and it must be a
// connection for the vendor whose route we're on — otherwise a token could be
// attached to another vendor's endpoint and every call would 404 at runtime.
func (h *Handler) connectionFor(r *http.Request, tenantID, id int64, provider string) (db.GetAIConnectionRow, error) {
	if id <= 0 {
		return db.GetAIConnectionRow{}, fmt.Errorf("connection_id is required — create a connection first")
	}
	conn, err := h.queries.GetAIConnection(r.Context(), db.GetAIConnectionParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.GetAIConnectionRow{}, fmt.Errorf("connection not found")
	}
	if conn.Provider != provider {
		return db.GetAIConnectionRow{}, fmt.Errorf("connection %q belongs to %s, not %s", conn.Name, conn.Provider, provider)
	}
	return conn, nil
}

// connectionStatusFrom builds the wire DTO from a list row.
//
// It exists because this DTO is assembled from the SAME query in two places —
// the standalone connections endpoint below and the vendor-status roll-up in
// handler.go — and a field added to only one of them half-lands silently.
// One constructor makes that structural.
func connectionStatusFrom(c db.ListAIConnectionsRow) connectionStatus {
	return connectionStatus{
		ID: c.ID, Provider: c.Provider, Name: c.Name, BaseURL: c.BaseUrl,
		TokenCount: c.TokenCount, CreatedAt: c.CreatedAt.Time.Format(time.RFC3339),
	}
}

func (h *Handler) HandleListConnections(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	rows, err := h.queries.ListAIConnections(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "connections")
		return
	}
	out := make([]connectionStatus, 0, len(rows))
	for _, c := range rows {
		out = append(out, connectionStatusFrom(c))
	}
	httputil.Data(w, out)
}

// HandleCreateConnection is POST /api/ai/vendors/{provider}/connections.
// Body: { name, base_url (optional — blank = the vendor's public API) }.
//
// Deliberately does NOT test the endpoint: there is no key to test with yet,
// and requiring one would defeat the point of standalone connections. The live
// test happens when a token is added to it.
func (h *Handler) HandleCreateConnection(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	if !h.requireProvider(w, r, provider) {
		return
	}
	var req struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = h.displayName(r, provider)
	}
	baseURL := NormalizeBaseURL(provider, req.BaseURL)
	if msg := ValidateBaseURL(baseURL); msg != "" {
		httputil.BadRequest(w, msg)
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	vendor, err := h.queries.UpsertAIVendor(r.Context(), db.UpsertAIVendorParams{TenantID: tenantID, Provider: provider})
	if err != nil {
		httputil.ServerError(w, "save", "ai vendor")
		return
	}
	conn, err := h.queries.CreateAIConnection(r.Context(), db.CreateAIConnectionParams{
		VendorID: vendor.ID, Name: name, BaseUrl: baseURL,
	})
	if err != nil {
		httputil.ServerError(w, "save", "connection")
		return
	}
	// TokenCount is 0 by construction: a connection is created empty and the key
	// is attached afterwards.
	httputil.Created(w, connectionStatus{
		ID: conn.ID, Provider: provider, Name: conn.Name, BaseURL: conn.BaseUrl,
		TokenCount: 0,
		CreatedAt:  conn.CreatedAt.Time.Format(time.RFC3339),
	})
}

// HandleUpdateConnection edits a connection's name/endpoint. Changing the
// endpoint repoints every token on it at once — which is the reason the concept
// exists.
func (h *Handler) HandleUpdateConnection(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	if !h.requireProvider(w, r, provider) {
		return
	}
	id, err := httputil.ParseID(r, "connectionID")
	if err != nil {
		httputil.BadRequest(w, "invalid connection ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	cur, err := h.connectionFor(r, tenantID, id, provider)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	var req struct {
		Name string `json:"name"`
		// Pointer: an absent base_url keeps the stored endpoint, an explicit ""
		// clears it back to the vendor's public API.
		BaseURL *string `json:"base_url"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = cur.Name
	}
	baseURL := cur.BaseUrl
	if req.BaseURL != nil {
		baseURL = NormalizeBaseURL(provider, *req.BaseURL)
	}
	if msg := ValidateBaseURL(baseURL); msg != "" {
		httputil.BadRequest(w, msg)
		return
	}
	conn, err := h.queries.UpdateAIConnection(r.Context(), db.UpdateAIConnectionParams{
		ID: id, VendorID: cur.VendorID, Name: name, BaseUrl: baseURL,
	})
	if err != nil {
		httputil.ServerError(w, "update", "connection")
		return
	}
	// token_count is what tells a caller whether this connection is live,
	// so it has to be the real number here — a hardcoded
	// zero would report an endpoint with keys as unusable.
	count, err := h.queries.CountTokensForConnection(r.Context(), id)
	if err != nil {
		httputil.ServerError(w, "count keys for", "connection")
		return
	}
	httputil.Data(w, connectionStatus{
		ID: conn.ID, Provider: provider, Name: conn.Name, BaseURL: conn.BaseUrl,
		TokenCount: count,
		CreatedAt:  conn.CreatedAt.Time.Format(time.RFC3339),
	})
}

// HandleDeleteConnection removes a connection, 409ing while tokens still hang
// off it (the FK refuses the delete too, so this is a clean message instead of a
// constraint violation).
func (h *Handler) HandleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	if !h.requireProvider(w, r, provider) {
		return
	}
	id, err := httputil.ParseID(r, "connectionID")
	if err != nil {
		httputil.BadRequest(w, "invalid connection ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	cur, err := h.connectionFor(r, tenantID, id, provider)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	refs, err := h.queries.CountTokensForConnection(r.Context(), id)
	if err != nil {
		httputil.ServerError(w, "check references", "connection")
		return
	}
	if refs > 0 {
		httputil.Error(w, http.StatusConflict,
			fmt.Sprintf("cannot delete: %d API token(s) still use this connection", refs))
		return
	}
	if err := h.queries.DeleteAIConnection(r.Context(), db.DeleteAIConnectionParams{ID: id, VendorID: cur.VendorID}); err != nil {
		httputil.ServerError(w, "delete", "connection")
		return
	}
	httputil.NoContent(w)
}
