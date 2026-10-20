package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Personal access token management.
//
// Tokens are org-scoped: created for the caller's CURRENT tenant, at the
// caller's current role. A user who belongs to three orgs creates three tokens.
// That's deliberate — it keeps tenant_id out of every MCP tool signature, so no
// tool can be argued into crossing a tenant boundary.

const maxTokenNameLen = 100

// tokenResponse is the safe projection of an api_tokens row. There is no field
// for the secret: the plaintext exists only in the CreateAPIToken response and
// is never readable again. A test greps this payload for key material.
type tokenResponse struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	Capability string  `json:"capability"`
	LastUsedAt *string `json:"last_used_at"`
	ExpiresAt  string  `json:"expires_at"`
	RevokedAt  *string `json:"revoked_at"`
	CreatedAt  string  `json:"created_at"`
	// Populated only on the manager-wide listing.
	UserEmail string `json:"user_email,omitempty"`
	UserName  string `json:"user_name,omitempty"`
}

func timestampPtr(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	s := ts.Time.UTC().Format(time.RFC3339)
	return &s
}

func timestampStr(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

// HandleCreateAPIToken mints a personal access token for the caller in the
// current org. The plaintext is returned exactly once.
func (h *Handler) HandleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	// Only a signed-in person creates keys: a key that mints keys would outlive
	// its own expiry and revocation.
	if PATInfoFromContext(r.Context()) != nil {
		httputil.Error(w, http.StatusForbidden, "access keys cannot create access keys")
		return
	}
	var req struct {
		Name          string `json:"name"`
		ExpiresInDays *int   `json:"expires_in_days"`
		Capability    string `json:"capability"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}
	if len(req.Name) > maxTokenNameLen {
		httputil.BadRequest(w, "name is too long")
		return
	}

	// Capability is validated against an allow-list rather than trusted.
	capability := req.Capability
	if capability == "" {
		capability = CapabilityRead
	}
	if capability != CapabilityRead && capability != CapabilityWrite {
		httputil.BadRequest(w, "capability must be 'read' or 'write'")
		return
	}

	lifetime := PATDefaultLifetime
	if req.ExpiresInDays != nil {
		if *req.ExpiresInDays <= 0 {
			httputil.BadRequest(w, "expires_in_days must be positive")
			return
		}
		lifetime = time.Duration(*req.ExpiresInDays) * 24 * time.Hour
		if lifetime > PATMaxLifetime {
			httputil.BadRequest(w, "expires_in_days must not exceed 365")
			return
		}
	}

	raw, hash, prefix, err := GeneratePAT()
	if err != nil {
		logging.Error(r.Context(), err, "access key generation failed", "auth-pat-generate-failed")
		httputil.ServerError(w, "generate", "access key")
		return
	}

	tenantID := TenantIDFromContext(r.Context())
	userID := UserIDFromContext(r.Context())

	token, err := h.queries.CreateAPIToken(r.Context(), db.CreateAPITokenParams{
		TenantID:   tenantID,
		UserID:     userID,
		Name:       req.Name,
		TokenHash:  hash,
		Prefix:     prefix,
		Capability: capability,
		ExpiresAt:  pgtype.Timestamptz{Time: time.Now().Add(lifetime), Valid: true},
	})
	if err != nil {
		httputil.ServerError(w, "create", "access key")
		return
	}

	// `token` is the ONLY response in this file that carries the secret.
	httputil.Created(w, map[string]any{
		"token": raw,
		"api_token": tokenResponse{
			ID:         token.ID,
			Name:       token.Name,
			Prefix:     token.Prefix,
			Capability: token.Capability,
			LastUsedAt: timestampPtr(token.LastUsedAt),
			ExpiresAt:  timestampStr(token.ExpiresAt),
			RevokedAt:  timestampPtr(token.RevokedAt),
			CreatedAt:  timestampStr(token.CreatedAt),
		},
	})
}

// HandleListAPITokens lists the caller's own tokens in the current org.
// Managers may pass ?all=true to see every token in the org — needed to revoke
// a departing teammate's agent access.
func (h *Handler) HandleListAPITokens(w http.ResponseWriter, r *http.Request) {
	tenantID := TenantIDFromContext(r.Context())

	if r.URL.Query().Get("all") == "true" {
		if !IsManager(r) {
			httputil.Error(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		rows, err := h.queries.ListAPITokensByTenant(r.Context(), tenantID)
		if err != nil {
			httputil.ServerError(w, "list", "access keys")
			return
		}
		out := make([]tokenResponse, 0, len(rows))
		for _, t := range rows {
			out = append(out, tokenResponse{
				ID:         t.ID,
				Name:       t.Name,
				Prefix:     t.Prefix,
				Capability: t.Capability,
				LastUsedAt: timestampPtr(t.LastUsedAt),
				ExpiresAt:  timestampStr(t.ExpiresAt),
				RevokedAt:  timestampPtr(t.RevokedAt),
				CreatedAt:  timestampStr(t.CreatedAt),
				UserEmail:  t.UserEmail,
				UserName:   t.UserName,
			})
		}
		httputil.Data(w, out)
		return
	}

	userID := UserIDFromContext(r.Context())
	rows, err := h.queries.ListAPITokensByUser(r.Context(), db.ListAPITokensByUserParams{
		UserID:   userID,
		TenantID: tenantID,
	})
	if err != nil {
		httputil.ServerError(w, "list", "access keys")
		return
	}
	out := make([]tokenResponse, 0, len(rows))
	for _, t := range rows {
		out = append(out, tokenResponse{
			ID:         t.ID,
			Name:       t.Name,
			Prefix:     t.Prefix,
			Capability: t.Capability,
			LastUsedAt: timestampPtr(t.LastUsedAt),
			ExpiresAt:  timestampStr(t.ExpiresAt),
			RevokedAt:  timestampPtr(t.RevokedAt),
			CreatedAt:  timestampStr(t.CreatedAt),
		})
	}
	httputil.Data(w, out)
}

// HandleRevokeAPIToken revokes a token. A caller may always revoke their own;
// managers may revoke anyone's in the org.
//
// Guard order matters: the tenant-scoped fetch runs BEFORE the ownership check,
// so a token ID belonging to another org returns 404 rather than 403. Otherwise
// the response code leaks whether that ID exists elsewhere (same ordering
// principle as handleUpdateMemberRole and the j17/j19 cross-tenant probes).
func (h *Handler) HandleRevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	tokenID, err := httputil.ParseID(r, "tokenID")
	if err != nil {
		httputil.BadRequest(w, "invalid access key ID")
		return
	}

	tenantID := TenantIDFromContext(r.Context())
	token, err := h.queries.GetAPIToken(r.Context(), db.GetAPITokenParams{
		ID:       tokenID,
		TenantID: tenantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.NotFound(w, "access key")
			return
		}
		httputil.ServerError(w, "load", "access key")
		return
	}

	if token.UserID != UserIDFromContext(r.Context()) && !IsManager(r) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	if err := h.queries.RevokeAPIToken(r.Context(), db.RevokeAPITokenParams{
		ID:       tokenID,
		TenantID: tenantID,
	}); err != nil {
		httputil.ServerError(w, "revoke", "access key")
		return
	}
	httputil.NoContent(w)
}
