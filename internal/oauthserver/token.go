package oauthserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// The token endpoint (RFC 6749 §3.2, OAuth 2.1 profile): authorization_code
// exchange and refresh_token rotation, public clients, PKCE-verified,
// form-urlencoded in (Claude sends nothing else), JSON out.

// HandleToken dispatches on grant_type.
func (s *Server) HandleToken(w http.ResponseWriter, r *http.Request) {
	// RFC 6749 §4.1.3 mandates form encoding. Some clients omit the charset
	// suffix; accept any application/x-www-form-urlencoded variant.
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "body must be application/x-www-form-urlencoded")
		return
	}
	// A code's stored resource was validated at /authorize, so checking the
	// request's resource here covers both grants.
	if res := r.PostForm.Get("resource"); res != "" && !s.isOurResource(res) {
		oauthError(w, http.StatusBadRequest, "invalid_target", "tokens are only issued for "+s.Resource())
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.exchangeCode(w, r)
	case "refresh_token":
		s.refreshToken(w, r)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "supported: authorization_code, refresh_token")
	}
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request) {
	code := r.PostForm.Get("code")
	verifier := r.PostForm.Get("code_verifier")
	clientID := r.PostForm.Get("client_id")
	redirectURI := r.PostForm.Get("redirect_uri")
	if code == "" || verifier == "" || clientID == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "code, code_verifier and client_id are required")
		return
	}

	// Single-use: the row is deleted by this read. Anything failing past this
	// point burns the code — deliberately, per OAuth 2.1 §4.1.3 (a code that
	// hit a failed exchange must not be retryable).
	row, err := s.q.ConsumeOAuthCode(r.Context(), hashSecret(code))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown, used, or expired code")
			return
		}
		logging.Error(r.Context(), err, "code redemption failed", "oauth-code-redeem-failed")
		oauthError(w, http.StatusInternalServerError, "server_error", "token issuance failed")
		return
	}

	// All bindings verified against the STORED values, not the request's.
	switch {
	case time.Now().After(row.ExpiresAt.Time):
		oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown, used, or expired code")
		return
	case row.ClientID != clientID:
		oauthError(w, http.StatusBadRequest, "invalid_grant", "code was issued to a different client")
		return
	case redirectURI != "" && row.RedirectUri != redirectURI:
		// RFC 6749 §4.1.3: if redirect_uri was in the authorize request it
		// must be repeated identically here.
		oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	case subtle.ConstantTimeCompare([]byte(pkceChallenge(verifier)), []byte(row.CodeChallenge)) != 1:
		oauthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	s.issuePair(w, r.Context(), pgtype.UUID{Bytes: uuid.New(), Valid: true}, row.ClientID, row.UserID, row.TenantID, row.Scope)
}

func (s *Server) refreshToken(w http.ResponseWriter, r *http.Request) {
	refresh := r.PostForm.Get("refresh_token")
	clientID := r.PostForm.Get("client_id")
	if refresh == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	row, err := s.q.GetOAuthTokenByRefreshHash(r.Context(), hashSecret(refresh))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh token")
			return
		}
		logging.Error(r.Context(), err, "refresh lookup failed", "oauth-refresh-lookup-failed")
		oauthError(w, http.StatusInternalServerError, "server_error", "token refresh failed")
		return
	}

	// A REVOKED refresh token being presented is the OAuth 2.1 token-theft
	// signal: rotation already replaced it, so whoever holds it isn't the
	// party that rotated. Revoke the whole family.
	if row.RevokedAt.Valid {
		if _, err := s.q.RevokeOAuthTokenFamily(r.Context(), row.FamilyID); err != nil {
			logging.Error(r.Context(), err, "family revocation failed", "oauth-family-revoke-failed")
		}
		logging.Warn(r.Context(), nil, "revoked refresh token replayed — family revoked", "oauth-refresh-replayed")
		oauthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is no longer valid")
		return
	}
	if time.Now().After(row.RefreshExpiresAt.Time) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "refresh token expired")
		return
	}
	if clientID != "" && clientID != row.ClientID {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "refresh token was issued to a different client")
		return
	}

	// Rotate: revoke the presented pair, then mint a successor in the family.
	if err := s.q.RevokeOAuthToken(r.Context(), row.ID); err != nil {
		logging.Error(r.Context(), err, "rotation revoke failed", "oauth-rotate-revoke-failed")
		oauthError(w, http.StatusInternalServerError, "server_error", "token refresh failed")
		return
	}
	s.issuePair(w, r.Context(), row.FamilyID, row.ClientID, row.UserID, row.TenantID, row.Scope)
}

// issuePair mints and stores an access+refresh pair and writes the RFC 6749
// token response.
func (s *Server) issuePair(w http.ResponseWriter, ctx context.Context, family pgtype.UUID, clientID string, userID, tenantID int64, scope string) {
	access, accessHash, err1 := GenerateAccessToken()
	refresh, refreshHash, err2 := newSecret(RefreshTokenPrefix)
	if err1 != nil || err2 != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "token issuance failed")
		return
	}
	now := time.Now()
	if _, err := s.q.CreateOAuthToken(ctx, db.CreateOAuthTokenParams{
		FamilyID:         family,
		ClientID:         clientID,
		UserID:           userID,
		TenantID:         tenantID,
		Scope:            scope,
		AccessTokenHash:  accessHash,
		RefreshTokenHash: refreshHash,
		AccessExpiresAt:  pgtype.Timestamptz{Time: now.Add(AccessTokenTTL), Valid: true},
		RefreshExpiresAt: pgtype.Timestamptz{Time: now.Add(RefreshTokenTTL), Valid: true},
	}); err != nil {
		logging.Error(ctx, err, "token insert failed", "oauth-token-insert-failed")
		oauthError(w, http.StatusInternalServerError, "server_error", "token issuance failed")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(AccessTokenTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         scope,
	})
}
