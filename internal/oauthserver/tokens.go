// Package oauthserver is OpenLoc's OAuth 2.1 authorization server:
// the piece that lets Claude and ChatGPT connect to /mcp
// on a user's behalf through a consent screen instead of a pasted PAT.
//
// Role inversion relative to internal/auth/oauth.go: there WE are the OAuth
// client and Google/Microsoft are the authorization servers; here Claude /
// ChatGPT are the clients and we are the authorization server. Identity comes
// from the normal browser session /oauth/authorize requires, obtained through
// any existing sign-in method. This package only mints
// DELEGATED credentials for accounts that already exist.
//
// Every artifact is an opaque 256-bit random value stored as SHA-256, exactly
// like PATs (no low-entropy secret ⇒ no slow hash needed). Access tokens
// resolve to the same Claims + read/write capability a PAT resolves to, so
// MCP handlers treat both alike.
package oauthserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"openloc/internal/auth"
)

const (
	// AccessTokenPrefix routes a bearer value to the OAuth resolver in
	// auth.Middleware — same trick as auth.PATPrefix, same anti-probing rule:
	// a value carrying this prefix is ONLY ever tried as an OAuth token.
	// Aliased from auth so the middleware and this package can't drift.
	AccessTokenPrefix = auth.OAuthAccessPrefix
	// RefreshTokenPrefix marks refresh tokens. Never valid on /mcp.
	RefreshTokenPrefix = "eltm_ort_"
	// codePrefix marks authorization codes. Only ever seen inside redirects.
	codePrefix = "eltm_oac_"

	randomBytes = 32

	// AccessTokenTTL is deliberately short: Claude/ChatGPT refresh
	// transparently (reactively on 401 + proactively before expiry), so a
	// leaked access token has a one-hour blast radius.
	AccessTokenTTL = time.Hour
	// RefreshTokenTTL bounds an idle connection's life. Rotation renews it,
	// so an actively used connection lives indefinitely; an abandoned one
	// dies after 30 days.
	RefreshTokenTTL = 30 * 24 * time.Hour
	// codeTTL is the authorize→token window. The exchange is immediate in
	// practice; 5 minutes is the spec-customary ceiling.
	codeTTL = 5 * time.Minute
)

// newSecret mints prefix + base64url(32 random bytes) and its storage hash.
func newSecret(prefix string) (plain, hash string, err error) {
	buf := make([]byte, randomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("oauthserver: entropy: %w", err)
	}
	plain = prefix + base64.RawURLEncoding.EncodeToString(buf)
	return plain, hashSecret(plain), nil
}

// GenerateAccessToken mints an access token and its storage hash, the pair
// issuePair stores. Exported so test fixtures seed tokens the resolver accepts.
func GenerateAccessToken() (plain, hash string, err error) {
	return newSecret(AccessTokenPrefix)
}

// hashSecret is the at-rest form. Hash the WHOLE value (prefix included) so a
// database leak reveals nothing usable and lookups are a single indexed hit.
func hashSecret(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// pkceChallenge computes the S256 challenge for a verifier — used at the
// token endpoint to check the presented verifier against the stored
// challenge. Constant-time comparison is unnecessary: both sides derive from
// values the caller already holds.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// PKCEChallengeForTest exposes the S256 computation to the integration suite
// so tests derive challenges the same way real clients do.
func PKCEChallengeForTest(verifier string) string { return pkceChallenge(verifier) }
