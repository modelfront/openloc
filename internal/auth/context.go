package auth

import (
	"context"

	"openloc/internal/ptr"
)

type contextKey struct{}
type projectKey struct{}

// WithClaims stores auth claims in the context.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, contextKey{}, claims)
}

// ClaimsFromContext retrieves auth claims from the context.
// Returns nil, false if no claims are present.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(contextKey{}).(*Claims)
	return claims, ok
}

// TenantIDFromContext returns the tenant ID from the context.
// Panics if no claims are present (only call from authenticated routes).
func TenantIDFromContext(ctx context.Context) int64 {
	claims, ok := ClaimsFromContext(ctx)
	if !ok {
		panic("auth: no claims in context — middleware missing?")
	}
	return claims.TenantID
}

// UserIDFromContext returns the user ID from the context.
// Panics if no claims are present (only call from authenticated routes).
func UserIDFromContext(ctx context.Context) int64 {
	claims, ok := ClaimsFromContext(ctx)
	if !ok {
		panic("auth: no claims in context — middleware missing?")
	}
	return claims.UserID
}

// UserIDPtr converts a user ID into the nullable form the DB wants for
// created_by / updated_by columns. A zero ID means "no authenticated user"
// (system, worker, or seed paths) and maps to NULL rather than a dangling FK.
func UserIDPtr(id int64) *int64 { return ptr.NilIfZero(id) }

// RoleFromContext returns the user's role from the context.
// Panics if no claims are present (only call from authenticated routes).
func RoleFromContext(ctx context.Context) string {
	claims, ok := ClaimsFromContext(ctx)
	if !ok {
		panic("auth: no claims in context — middleware missing?")
	}
	return claims.Role
}

type userEmailKey struct{}

// WithUserEmail stores the authenticated user's email in the context. Set by
// Middleware/RequireIdentity from the UserExists lookup (the row is already
// fetched — this costs no extra query). Consumed by the api package's
// attachIdentity, which copies it onto the logging.Scope for error notices.
func WithUserEmail(ctx context.Context, email string) context.Context {
	if email == "" {
		return ctx
	}
	return context.WithValue(ctx, userEmailKey{}, email)
}

// UserEmailFromContext returns the email stored by WithUserEmail, or "".
// Unlike the claims getters this never panics — the email is observability
// enrichment, not an auth invariant.
func UserEmailFromContext(ctx context.Context) string {
	email, _ := ctx.Value(userEmailKey{}).(string)
	return email
}

type writeGrantKey struct{}

// WithWriteGrant marks a context as carrying a HUMAN-CONFIRMED write
// authorization. Set ONLY by the chat confirm executor, on a fresh
// in-process MCP session, for exactly the tool calls a user just approved —
// never on a request's normal path. The MCP write gate accepts it as an
// alternative to PAT write capability.
func WithWriteGrant(ctx context.Context) context.Context {
	return context.WithValue(ctx, writeGrantKey{}, true)
}

// HasWriteGrant reports whether ctx carries a confirmed write grant.
func HasWriteGrant(ctx context.Context) bool {
	granted, _ := ctx.Value(writeGrantKey{}).(bool)
	return granted
}
