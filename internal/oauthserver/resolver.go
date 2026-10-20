package oauthserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
)

// Resolver bridges OAuth access tokens into auth.Middleware, exactly the way
// PATs plug in: prefix-routed, exclusive (a failed OAuth token is a flat 401,
// never retried as anything else), and resolving to the SAME Claims +
// capability shape a PAT does, so MCP handlers treat both alike.
//
// Two deliberate semantics mirror the PAT resolver:
//   - Role is read from the membership at REQUEST time, so removing a member
//     kills their delegated access instantly, not at token expiry.
//   - The token is MCP-only (audience-bound): auth.Middleware rejects an
//     OAuth-prefixed bearer on any request path other than /mcp.

// ErrInvalidOAuthToken mirrors auth.ErrInvalidPAT. A lookup that fails for
// any other reason than a missing row is returned wrapped, so the middleware
// answers 503 rather than telling the connector its token is invalid.
var ErrInvalidOAuthToken = auth.ErrInvalidOAuthToken

// lookupErr maps a failed lookup: a missing row is an invalid token, anything
// else is the check failing.
func lookupErr(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidOAuthToken
	}
	return fmt.Errorf("oauthserver: look up %s: %w", what, err)
}

// IsAccessToken reports whether a bearer value claims to be ours.
func IsAccessToken(token string) bool {
	return strings.HasPrefix(token, AccessTokenPrefix)
}

// touchInterval coarsens last_used_at writes, same as PATs.
const touchInterval = time.Minute

// NewResolver returns the middleware hook.
func NewResolver(q db.Querier) func(ctx context.Context, token string) (*auth.Claims, *auth.PATInfo, error) {
	return func(ctx context.Context, token string) (*auth.Claims, *auth.PATInfo, error) {
		row, err := q.GetOAuthTokenByAccessHash(ctx, hashSecret(token))
		if err != nil {
			return nil, nil, lookupErr(err, "access token")
		}
		if row.RevokedAt.Valid || time.Now().After(row.AccessExpiresAt.Time) {
			return nil, nil, ErrInvalidOAuthToken
		}
		// Live membership check: role comes from NOW, not issuance time.
		m, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: row.UserID, TenantID: row.TenantID})
		if err != nil {
			return nil, nil, lookupErr(err, "membership")
		}
		u, err := q.GetUser(ctx, row.UserID)
		if err != nil {
			return nil, nil, lookupErr(err, "user")
		}
		if u.DeactivatedAt.Valid {
			return nil, nil, ErrInvalidOAuthToken
		}

		if !row.LastUsedAt.Valid || time.Since(row.LastUsedAt.Time) > touchInterval {
			// Best-effort bookkeeping; never blocks the request.
			_ = q.TouchOAuthToken(ctx, row.ID)
		}

		claims := &auth.Claims{TenantID: row.TenantID, UserID: row.UserID, Role: m.Role}
		info := &auth.PATInfo{
			Capability: row.Scope, // "read" | "write" — same vocabulary as PATs
			UserEmail:  u.Email,
			Kind:       auth.CredentialOAuth,
			ClientName: row.ClientName,
			// TokenID stays 0 (OAuth tokens aren't api_tokens rows — the audit
			// writer inserts NULL for us). The rate-limit bucket is the negated
			// oauth_tokens id so each grant gets its own limiter and never
			// collides with a positive PAT id.
			RateLimitKey: -row.ID,
		}
		return claims, info, nil
	}
}
