package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Token scopes. A "tenant" token carries a selected org + role and
// grants access to org-scoped routes. An "identity" token proves who you are
// but has NOT selected an org yet — it can only reach the org-list / org-select
// endpoints (the Slack-style "pick your workspace" step).
const (
	ScopeTenant   = "tenant"
	ScopeIdentity = "identity"
)

// identityTokenExpiry bounds how long the un-selected identity token lives. It's
// short — the user is expected to pick an org promptly after authenticating.
const identityTokenExpiry = 15 * time.Minute

// Claims extends JWT standard claims with tenant, user, and role identity.
// For an identity-scoped token, TenantID is 0 and Role is empty.
type Claims struct {
	jwt.RegisteredClaims
	TenantID int64  `json:"tenant_id"`
	UserID   int64  `json:"user_id"`
	Role     string `json:"role"`
	Scope    string `json:"scope"`
	// Impersonated marks a platform-operator "log in as org" token:
	// the caller is inside an org they don't belong to. The platform operator
	// surface is hidden/refused for such tokens so the operator experiences the
	// org as its owner would, and can't manage the platform mid-impersonation.
	Impersonated bool `json:"imp,omitempty"`
	// Demo marks a visit to the shared demo organization.
	// UserID is then the demo org's guide; RealUserID and RealTenantID are the
	// visitor and the org they came from, which leaving the demo returns to.
	Demo         bool  `json:"demo,omitempty"`
	RealUserID   int64 `json:"ruid,omitempty"`
	RealTenantID int64 `json:"rtid,omitempty"`
}

// GenerateToken creates a signed HS256 tenant-scoped JWT (org selected).
func GenerateToken(userID, tenantID int64, role, secret string, expiry time.Duration) (string, error) {
	return signClaims(Claims{
		TenantID: tenantID,
		UserID:   userID,
		Role:     role,
		Scope:    ScopeTenant,
	}, secret, expiry)
}

// GenerateImpersonationToken is GenerateToken for a platform operator entering
// an org they're not a member of. Identical to a normal tenant token
// but flagged Impersonated so the platform surface is suppressed.
func GenerateImpersonationToken(userID, tenantID int64, role, secret string, expiry time.Duration) (string, error) {
	return signClaims(Claims{
		TenantID:     tenantID,
		UserID:       userID,
		Role:         role,
		Scope:        ScopeTenant,
		Impersonated: true,
	}, secret, expiry)
}

// demoTokenExpiry bounds a demo visit; the visitor enters again after it.
const demoTokenExpiry = 4 * time.Hour

// GenerateDemoToken is a tenant token for the demo organization, acting as its
// guide on behalf of realUserID, who came from realTenantID (0: no org chosen).
func GenerateDemoToken(guideID, demoTenantID, realUserID, realTenantID int64, secret string) (string, error) {
	return signClaims(Claims{
		TenantID:     demoTenantID,
		UserID:       guideID,
		Role:         RoleOwner,
		Scope:        ScopeTenant,
		Demo:         true,
		RealUserID:   realUserID,
		RealTenantID: realTenantID,
	}, secret, demoTokenExpiry)
}

// GenerateIdentityToken creates a short-lived identity-scoped JWT: the user is
// authenticated but has not yet chosen an org. It only unlocks the org-picker
// endpoints (see RequireIdentity).
func GenerateIdentityToken(userID int64, secret string) (string, error) {
	return signClaims(Claims{
		UserID: userID,
		Scope:  ScopeIdentity,
	}, secret, identityTokenExpiry)
}

func signClaims(claims Claims, secret string, expiry time.Duration) (string, error) {
	now := time.Now()
	claims.RegisteredClaims = jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ValidateToken parses and validates a JWT, returning the embedded claims.
func ValidateToken(tokenString, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

// SessionClaims validates the BROWSER session on a request — the HttpOnly
// cookie set at login, or a bearer JWT. Exists for handlers that live outside
// the authenticated /api group but still need to know who the browser is
// (the OAuth consent screen). PATs and OAuth access tokens
// are deliberately NOT accepted here: machine credentials must never drive a
// consent approval.
func SessionClaims(r *http.Request, secret string) (*Claims, error) {
	token := ""
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		token = c.Value
	} else if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token = strings.TrimPrefix(h, "Bearer ")
	}
	if token == "" || IsPAT(token) || strings.HasPrefix(token, OAuthAccessPrefix) {
		return nil, errors.New("auth: no browser session")
	}
	return ValidateToken(token, secret)
}
