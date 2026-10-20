// Package keyattach connects a vendor key to the engines that lost theirs, and
// lists the engines a key serves before it is deleted. Owner/admin only, the
// same people who manage keys (wired in api.go).
package keyattach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/ai"
	"openloc/internal/aiengine"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Engine kinds: an MT engine (DeepL, Google) or an AI engine (LLM or QE+APE).
const (
	KindMT = "mt"
	KindAI = "ai"
)

// Engine is one engine in a list. Fits and Reason say whether the key can serve
// it: an AI engine's model must be one the key offers.
type Engine struct {
	Kind    string `json:"kind"`
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Purpose string `json:"purpose,omitempty"`
	Fits    bool   `json:"fits"`
	Reason  string `json:"reason,omitempty"`
}

type Handler struct {
	queries db.Querier
	pool    *pgxpool.Pool
	catalog *ai.Catalog
}

func NewHandler(q db.Querier, pool *pgxpool.Pool, catalog *ai.Catalog) *Handler {
	return &Handler{queries: q, pool: pool, catalog: catalog}
}

// token resolves the URL's key, tenant-scoped; a key of another vendor is a 404.
func (h *Handler) token(w http.ResponseWriter, r *http.Request) (db.GetVendorTokenRow, int64, bool) {
	tenantID := auth.TenantIDFromContext(r.Context())
	id, err := httputil.ParseID(r, "tokenID")
	if err != nil {
		httputil.BadRequest(w, "invalid token ID")
		return db.GetVendorTokenRow{}, 0, false
	}
	tok, err := h.queries.GetVendorToken(r.Context(), db.GetVendorTokenParams{ID: id, TenantID: tenantID})
	if err == nil && tok.Provider != chi.URLParam(r, "provider") {
		httputil.NotFound(w, "token")
		return db.GetVendorTokenRow{}, 0, false
	}
	if httputil.DBError(w, err, "token") {
		return db.GetVendorTokenRow{}, 0, false
	}
	return tok, tenantID, true
}

// keyless lists the tenant's engines of the key's vendor that have no key.
func (h *Handler) keyless(ctx context.Context, q db.Querier, tenantID int64, tok db.GetVendorTokenRow) ([]Engine, error) {
	out := []Engine{}
	mt, err := q.ListKeylessMTEngines(ctx, db.ListKeylessMTEnginesParams{TenantID: tenantID, Provider: tok.Provider})
	if err != nil {
		return nil, err
	}
	for _, e := range mt {
		out = append(out, Engine{Kind: KindMT, ID: e.ID, Name: e.Name, Fits: true})
	}
	provider := tok.Provider
	rows, err := q.ListKeylessAIEngineRows(ctx, db.ListKeylessAIEngineRowsParams{TenantID: tenantID, Provider: &provider})
	if err != nil {
		return nil, err
	}
	// An engine is listed once and fits only if the key can serve every one of
	// its keyless rows, because the attach gives the key to all of them.
	aiIndex := map[int64]int{}
	for _, r := range rows {
		var settings map[string]any
		if len(r.Settings) > 0 {
			if err := json.Unmarshal(r.Settings, &settings); err != nil {
				return nil, fmt.Errorf("engine %d settings: %w", r.EngineID, err)
			}
		}
		i, seen := aiIndex[r.EngineID]
		if !seen {
			i = len(out)
			aiIndex[r.EngineID] = i
			out = append(out, Engine{Kind: KindAI, ID: r.EngineID, Name: r.Name, Purpose: r.Purpose, Fits: true})
		}
		if !out[i].Fits {
			continue
		}
		if _, msg := aiengine.ValidateModelSelection(ctx, q, h.catalog, tenantID, tok.ID, r.Model, settings); msg != "" {
			out[i].Fits = false
			out[i].Reason = fmt.Sprintf("This key cannot use %s.", r.Model)
		}
	}
	return out, nil
}

// HandleKeyless: GET /api/ai/vendors/{provider}/tokens/{tokenID}/keyless-engines.
func (h *Handler) HandleKeyless(w http.ResponseWriter, r *http.Request) {
	tok, tenantID, ok := h.token(w, r)
	if !ok {
		return
	}
	engines, err := h.keyless(r.Context(), h.queries, tenantID, tok)
	if err != nil {
		logging.Error(r.Context(), err, "failed to list keyless engines", "keyless-engines-list-failed", logging.Provider(tok.Provider))
		httputil.ServerError(w, "list", "engines")
		return
	}
	httputil.Data(w, engines)
}

// HandleAttach: POST /api/ai/vendors/{provider}/tokens/{tokenID}/attach with
// {mt_engine_ids, ai_engine_ids}. All or nothing: every engine must still be
// keyless, of the key's vendor, and fit the key.
func (h *Handler) HandleAttach(w http.ResponseWriter, r *http.Request) {
	tok, tenantID, ok := h.token(w, r)
	if !ok {
		return
	}
	var req struct {
		MTEngineIDs []int64 `json:"mt_engine_ids"`
		AIEngineIDs []int64 `json:"ai_engine_ids"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		httputil.ServerError(w, "attach", "key")
		return
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			logging.Warn(ctx, rbErr, "failed to roll back key attach", "key-attach-rollback-failed")
		}
	}()
	qtx := db.New(tx)
	current, err := h.keyless(ctx, qtx, tenantID, tok)
	if err != nil {
		logging.Error(ctx, err, "failed to list keyless engines", "keyless-engines-list-failed", logging.Provider(tok.Provider))
		httputil.ServerError(w, "attach", "key")
		return
	}
	if msg := checkRequest(current, req.MTEngineIDs, req.AIEngineIDs); msg != "" {
		httputil.Error(w, http.StatusConflict, msg)
		return
	}
	tokenID := tok.ID
	provider := tok.Provider
	for _, id := range req.MTEngineIDs {
		if _, err := qtx.AttachTokenToKeylessMTEngine(ctx, db.AttachTokenToKeylessMTEngineParams{TokenID: &tokenID, ID: id, TenantID: tenantID, Provider: provider}); err != nil {
			logging.Error(ctx, err, "failed to attach key to MT engine", "key-attach-failed", logging.Provider(provider))
			httputil.ServerError(w, "attach", "key")
			return
		}
	}
	for _, id := range req.AIEngineIDs {
		if _, err := qtx.AttachTokenToKeylessAIEngine(ctx, db.AttachTokenToKeylessAIEngineParams{TokenID: &tokenID, EngineID: id, TenantID: tenantID, Provider: &provider}); err != nil {
			logging.Error(ctx, err, "failed to attach key to AI engine", "key-attach-failed", logging.Provider(provider), logging.AIEngineID(id))
			httputil.ServerError(w, "attach", "key")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		logging.Error(ctx, err, "failed to commit key attach", "key-attach-failed", logging.Provider(provider))
		httputil.ServerError(w, "attach", "key")
		return
	}
	httputil.Data(w, map[string]int{"attached": len(req.MTEngineIDs) + len(req.AIEngineIDs)})
}

// checkRequest returns why the request cannot be applied, or "".
func checkRequest(current []Engine, mtIDs, aiIDs []int64) string {
	byKey := map[string]Engine{}
	for _, e := range current {
		byKey[fmt.Sprintf("%s:%d", e.Kind, e.ID)] = e
	}
	check := func(kind string, ids []int64) string {
		for _, id := range ids {
			e, found := byKey[fmt.Sprintf("%s:%d", kind, id)]
			if !found {
				return fmt.Sprintf("Engine %d no longer needs a key from this provider. Reload and try again.", id)
			}
			if !e.Fits {
				return fmt.Sprintf("%s: %s", e.Name, e.Reason)
			}
		}
		return ""
	}
	if msg := check(KindMT, mtIDs); msg != "" {
		return msg
	}
	return check(KindAI, aiIDs)
}

// HandleUsing: GET /api/ai/vendors/{provider}/tokens/{tokenID}/engines — the
// engines that stop working if this key is deleted.
func (h *Handler) HandleUsing(w http.ResponseWriter, r *http.Request) {
	tok, tenantID, ok := h.token(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	tokenID := tok.ID
	out := []Engine{}
	mt, err := h.queries.ListMTEnginesUsingToken(ctx, db.ListMTEnginesUsingTokenParams{TenantID: tenantID, TokenID: &tokenID})
	if err != nil {
		logging.Error(ctx, err, "failed to list engines using key", "key-engines-list-failed", logging.Provider(tok.Provider))
		httputil.ServerError(w, "list", "engines")
		return
	}
	for _, e := range mt {
		out = append(out, Engine{Kind: KindMT, ID: e.ID, Name: e.Name, Fits: true})
	}
	aiRows, err := h.queries.ListAIEnginesUsingToken(ctx, db.ListAIEnginesUsingTokenParams{TenantID: tenantID, TokenID: &tokenID})
	if err != nil {
		logging.Error(ctx, err, "failed to list engines using key", "key-engines-list-failed", logging.Provider(tok.Provider))
		httputil.ServerError(w, "list", "engines")
		return
	}
	for _, e := range aiRows {
		out = append(out, Engine{Kind: KindAI, ID: e.ID, Name: e.Name, Purpose: e.Purpose, Fits: true})
	}
	httputil.Data(w, out)
}
