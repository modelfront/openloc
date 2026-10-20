// Package userpair handles user language-pair qualifications.
//
// Tenant isolation is INHERITED via user_id → users.tenant_id. Every query
// joins through users and filters tenant_id. Trusting user_id from a URL
// path alone (without the tenant join) would be a cross-tenant leak.
//
// Role gating:
//   - GET /api/users/{userID}/language-pairs — any authenticated tenant member
//     can read any tenant member's pairs. Pairs are not sensitive PII (they
//     describe what languages a vendor handles, the same information that
//     surfaces in workflow assignment dropdowns); within a tenant, this is
//     intentionally readable. Tenant isolation prevents cross-tenant reads.
//   - PUT /api/users/{userID}/language-pairs — admin-only via route middleware.
//   - GET /api/users/by-language-pair — any authenticated tenant member.
//
// All routes wired in api.go.
package userpair

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/logging"
)

// maxPairs caps how many pairs one user can have. 100 is well above any
// realistic translator profile (top-tier human translators handle ~5-10
// pairs at most) and keeps PUT requests bounded.
const maxPairs = 100

type Handler struct {
	queries db.Querier
	pool    *pgxpool.Pool
}

func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{queries: q, pool: pool}
}

// Pair is the wire shape for a single pair (in + out). Languages are
// normalized with langcode.Normalize before storing.
type Pair struct {
	SourceLang string `json:"source_lang"`
	TargetLang string `json:"target_lang"`
	// RowGroup records which editor row this pair came from, so the client can
	// reconstruct the rows on read. Optional on input (defaults to 0).
	RowGroup int `json:"row_group"`
}

// InputError is a pair set the caller must fix. Its message is written for
// the caller.
type InputError struct{ Msg string }

func (e InputError) Error() string { return e.Msg }

// List returns a user's pairs. The query joins through the tenant, so a user
// of another tenant reads as an empty list.
func List(ctx context.Context, q db.Querier, tenantID, userID int64) ([]Pair, error) {
	pairs, err := q.ListUserLanguagePairs(ctx, db.ListUserLanguagePairsParams{UserID: userID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("list language pairs: %w", err)
	}
	out := make([]Pair, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, Pair{SourceLang: p.SourceLang, TargetLang: p.TargetLang, RowGroup: int(p.RowGroup)})
	}
	return out, nil
}

// Replace swaps a user's whole pair set in one transaction (DELETE + INSERT,
// so a mid-way failure leaves the old set) and returns the stored set.
// Languages are normalized and duplicates keep their first row group. A user
// outside the tenant is pgx.ErrNoRows.
func Replace(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID, userID int64, pairs []Pair) ([]Pair, error) {
	if _, err := q.GetUserInTenant(ctx, db.GetUserInTenantParams{ID: userID, TenantID: tenantID}); err != nil {
		return nil, err
	}
	if len(pairs) > maxPairs {
		return nil, InputError{fmt.Sprintf("too many pairs (max %d per user)", maxPairs)}
	}
	type normalizedPair struct{ s, t string }
	seen := make(map[normalizedPair]int, len(pairs))
	var order []normalizedPair
	for _, p := range pairs {
		s := langcode.Normalize(p.SourceLang)
		t := langcode.Normalize(p.TargetLang)
		if s == "" || t == "" {
			return nil, InputError{"source_lang and target_lang are required for every pair"}
		}
		if s == t {
			return nil, InputError{"source_lang and target_lang must differ"}
		}
		np := normalizedPair{s, t}
		if _, ok := seen[np]; !ok {
			seen[np] = p.RowGroup
			order = append(order, np)
		}
	}
	if pool == nil {
		return nil, errors.New("language pairs need a database pool")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	qtx := db.New(tx)
	if err := qtx.DeleteUserLanguagePairs(ctx, db.DeleteUserLanguagePairsParams{UserID: userID, TenantID: tenantID}); err != nil {
		return nil, fmt.Errorf("clear language pairs: %w", err)
	}
	for _, np := range order {
		if err := qtx.InsertUserLanguagePair(ctx, db.InsertUserLanguagePairParams{
			UserID: userID, SourceLang: np.s, TargetLang: np.t, RowGroup: int32(seen[np]), TenantID: tenantID,
		}); err != nil {
			return nil, fmt.Errorf("insert language pair: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit language pairs: %w", err)
	}
	return List(ctx, q, tenantID, userID)
}

// HandleList returns all pairs for a given user, scoped to caller's tenant.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	userID, err := httputil.ParseID(r, "userID")
	if err != nil {
		httputil.BadRequest(w, "invalid user ID")
		return
	}
	out, err := List(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), userID)
	if err != nil {
		httputil.ServerError(w, "list", "language pairs")
		return
	}
	httputil.Data(w, out)
}

// HandleReplace replaces the full pair set for a user (PUT semantics).
// Admin-only via route middleware. A user outside the tenant is a 404 rather
// than a silent no-op.
func (h *Handler) HandleReplace(w http.ResponseWriter, r *http.Request) {
	userID, err := httputil.ParseID(r, "userID")
	if err != nil {
		httputil.BadRequest(w, "invalid user ID")
		return
	}
	var req struct {
		Pairs []Pair `json:"pairs"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	out, err := Replace(r.Context(), h.queries, h.pool, auth.TenantIDFromContext(r.Context()), userID, req.Pairs)
	var inputErr InputError
	switch {
	case err == nil:
		httputil.Data(w, out)
	case errors.As(err, &inputErr):
		httputil.BadRequest(w, inputErr.Msg)
	case errors.Is(err, pgx.ErrNoRows):
		httputil.NotFound(w, "user")
	default:
		logging.Error(r.Context(), err, "replacing language pairs failed", "userpair.replace_failed")
		httputil.ServerError(w, "update", "language pairs")
	}
}

// HandleListUsersForPair returns users in the caller's tenant qualified for
// a given source→target pair. Used by the workflow assignment dropdown.
//
// Both `source` and `target` are required query params; missing either
// returns 400 (rather than silently returning all users).
func (h *Handler) HandleListUsersForPair(w http.ResponseWriter, r *http.Request) {
	src := langcode.Normalize(r.URL.Query().Get("source"))
	tgt := langcode.Normalize(r.URL.Query().Get("target"))
	if src == "" || tgt == "" {
		httputil.BadRequest(w, "source and target query params are required")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())

	users, err := h.queries.ListUsersForLanguagePair(r.Context(), db.ListUsersForLanguagePairParams{
		TenantID:   tenantID,
		SourceLang: src,
		TargetLang: tgt,
	})
	if err != nil {
		httputil.ServerError(w, "list", "qualified users")
		return
	}
	httputil.Data(w, users)
}
