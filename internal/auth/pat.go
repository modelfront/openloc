package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"

	"openloc/internal/logging"
)

// Personal access tokens — the credential machine clients present
// instead of the browser's 24h JWT.
//
// Format: "eltm_pat_" + 43 chars of base64url(32 random bytes).
//
// The prefix serves three purposes: it lets auth.Middleware route a bearer value
// to the PAT path without attempting JWT parsing, it makes tokens greppable by
// secret scanners, and it gives the UI a stable fragment to display.
const (
	// PATPrefix marks a bearer value as a personal access token.
	PATPrefix = "eltm_pat_"

	// patRandomBytes is the entropy behind each token. 32 bytes (256 bits) is
	// far beyond guessable, which is why a plain SHA-256 (not bcrypt) is the
	// correct hash here — there is no low-entropy secret to protect against
	// offline brute force.
	patRandomBytes = 32

	// patDisplayChars is how much of the random part is kept in plaintext for
	// UI display ("eltm_pat_a1b2c3…").
	patDisplayChars = 6

	// patTouchInterval coarsens last_used_at writes. Without this, every single
	// request from a busy agent would be a row UPDATE on the hot path.
	patTouchInterval = time.Minute

	// PATMaxLifetime caps how long a token may live. No immortal credentials.
	PATMaxLifetime = 365 * 24 * time.Hour

	// PATDefaultLifetime is used when the caller doesn't specify one.
	PATDefaultLifetime = 90 * 24 * time.Hour
)

// ErrInvalidPAT is returned when a presented token doesn't resolve to a live,
// unrevoked, unexpired credential belonging to an active member.
//
// Callers MUST treat this as a flat 401 and MUST NOT fall back to JWT
// validation — a token carrying PATPrefix is a PAT claim, and a failed PAT is a
// failed request, not an invitation to try another credential type.
var ErrInvalidPAT = errors.New("auth: invalid personal access token")

// ErrInvalidOAuthToken is the OAuthResolver's answer for an access token that
// does not resolve to a live grant. Any other error means the check could not
// run.
var ErrInvalidOAuthToken = errors.New("auth: invalid OAuth access token")

// IsPAT reports whether a bearer value claims to be a personal access token.
// This is a *claim*, not a validation — it only decides which verifier runs.
func IsPAT(token string) bool { return strings.HasPrefix(token, PATPrefix) }

// GeneratePAT mints a new token, returning the plaintext (shown to the user
// exactly once), the SHA-256 hash to store, and the display prefix.
func GeneratePAT() (raw, hash, prefix string, err error) {
	buf := make([]byte, patRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("generate token entropy: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(buf)
	raw = PATPrefix + secret
	return raw, HashPAT(raw), PATPrefix + secret[:patDisplayChars], nil
}

// HashPAT returns the stored representation of a token. Hashing the full raw
// value (prefix included) means a stored hash can only ever match a
// correctly-prefixed presentation.
func HashPAT(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// PATInfo carries per-token metadata that isn't part of the JWT claim set.
//
// It lives in its own context value rather than being bolted onto Claims:
// Claims is a JWT DTO, and TokenID/Capability have no business being
// serializable into a signed token.
type PATInfo struct {
	// TokenID is the api_tokens row id — ONLY meaningful for CredentialPAT.
	// OAuth tokens live in a different table and leave this zero; the audit
	// writer keys off Kind, not this value, so a 0 here is never inserted
	// against the api_tokens FK.
	TokenID    int64
	Capability string
	// UserEmail is the token owner's email, from the resolver's users JOIN.
	// Observability enrichment only (user_email on error notices) — never an
	// auth input.
	UserEmail string
	// Kind distinguishes the credential that produced this info: CredentialPAT
	// (default, zero-value-safe for pre-OAuth code) or CredentialOAuth
	// (delegated tokens, which are MCP-only).
	Kind string
	// ClientName is the OAuth client's display name ("Claude"), for audit rows
	// and error notices. Empty for PATs.
	ClientName string
	// RateLimitKey is the per-credential bucket key for the MCP rate limiter.
	// It must be unique per credential and must NOT collide across credential
	// kinds: PATs use +TokenID (api_tokens ids are positive), OAuth uses the
	// negated oauth_tokens id. Zero means "no distinct bucket" (never set by a
	// real credential).
	RateLimitKey int64
}

// Credential kinds for PATInfo.Kind.
const (
	CredentialPAT   = "pat"
	CredentialOAuth = "oauth"
)

// OAuthAccessPrefix marks a bearer value as an OAuth access token.
// The constant lives HERE, not in oauthserver, because
// the middleware routes on it and oauthserver already imports auth.
const OAuthAccessPrefix = "eltm_oat_"

// CapabilityWrite is the capability a token needs for any mutating operation.
// Read-only tokens carry CapabilityRead. Mutating MCP tools require
// CapabilityWrite.
const (
	CapabilityRead  = "read"
	CapabilityWrite = "write"
)

type patContextKey struct{}

// WithPATInfo stores PAT metadata in the context.
func WithPATInfo(ctx context.Context, info *PATInfo) context.Context {
	return context.WithValue(ctx, patContextKey{}, info)
}

// PATInfoFromContext returns PAT metadata, or nil when the caller authenticated
// with something else (a browser JWT). Unlike TenantIDFromContext this does NOT
// panic — "authenticated, but not via PAT" is a legitimate state.
func PATInfoFromContext(ctx context.Context) *PATInfo {
	info, _ := ctx.Value(patContextKey{}).(*PATInfo)
	return info
}

// PATResolver verifies a raw personal access token and returns the claims it
// grants. Injected into Middleware so the auth package's request path doesn't
// hard-depend on a database handle (mirrors the UserExists indirection).
//
// A nil PATResolver disables PAT authentication entirely.
type PATResolver func(ctx context.Context, raw string) (*Claims, *PATInfo, error)

// TokenQuerier is the slice of db.Querier the PAT resolver needs. Narrow
// interface so tests can stub two methods instead of three hundred.
type TokenQuerier interface {
	GetAPITokenAuth(ctx context.Context, tokenHash string) (db.GetAPITokenAuthRow, error)
	TouchAPIToken(ctx context.Context, arg db.TouchAPITokenParams) error
}

// NewPATResolver builds the production PAT resolver.
//
// Role is deliberately read from the JOIN in GetAPITokenAuth (i.e. from
// memberships) on every request rather than stored on the token row. A demotion
// from admin to linguist, or removal from the org entirely, therefore takes
// effect on the caller's very next request. Note this differs from the browser
// JWT path, where role is frozen at mint time and a demotion only lands after
// re-login (up to 24h) for why the two paths are intentionally
// not unified.
func NewPATResolver(q TokenQuerier) PATResolver {
	return func(ctx context.Context, raw string) (*Claims, *PATInfo, error) {
		row, err := q.GetAPITokenAuth(ctx, HashPAT(raw))
		if errors.Is(err, pgx.ErrNoRows) {
			// Covers every rejection reason at once — unknown hash, revoked,
			// expired, membership gone, user deactivated. Deliberately
			// indistinguishable to the caller: a probing client must not learn
			// *why* a token failed.
			return nil, nil, ErrInvalidPAT
		}
		if err != nil {
			return nil, nil, fmt.Errorf("auth: look up access key: %w", err)
		}

		claims := &Claims{
			TenantID: row.TenantID,
			UserID:   row.UserID,
			Role:     row.Role,
			Scope:    ScopeTenant,
		}
		info := &PATInfo{TokenID: row.TokenID, Capability: row.Capability, UserEmail: row.UserEmail, Kind: CredentialPAT, RateLimitKey: row.TokenID}

		touchPAT(ctx, q, row)
		return claims, info, nil
	}
}

// touchPAT updates last_used_at at most once per patTouchInterval per token.
//
// The pre-check is a cheap in-Go comparison so the common case (a token used
// seconds ago) costs zero database work. The SQL repeats the cutoff guard so
// concurrent requests can't both write. Errors are logged, never surfaced: a
// failed bookkeeping write must not fail an otherwise-valid request.
func touchPAT(ctx context.Context, q TokenQuerier, row db.GetAPITokenAuthRow) {
	cutoff := time.Now().Add(-patTouchInterval)
	if row.LastUsedAt.Valid && row.LastUsedAt.Time.After(cutoff) {
		return
	}
	if err := q.TouchAPIToken(ctx, db.TouchAPITokenParams{
		ID:     row.TokenID,
		Cutoff: pgtype.Timestamptz{Time: cutoff, Valid: true},
	}); err != nil {
		logging.Warn(ctx, err, "api token last-used touch failed", "auth-pat-touch-failed")
	}
}
