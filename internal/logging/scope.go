package logging

import (
	"context"
	"log/slog"
	"sort"
	"sync"
)

// Scope is a mutable bag of fields carried in a context for the lifetime of one
// request or one background task. Every log line emitted with that context
// inherits them, and internal/errtrack reads the same bag when building a
// Honeybadger notice — so identity and entity IDs are attached once and appear
// in both places without duplicated plumbing.
//
// This exists because of a chi/context constraint. A middleware registered at
// the top of the stack sees the final response status, but cannot read auth
// claims: auth.Middleware installs claims by *replacing* the request further
// down the chain, so the outer middleware still holds the original context.
// A pointer in the context sidesteps that — the outer middleware creates the
// Scope, and a thin middleware after each claim-setter fills identity in.
// The alternative was teaching internal/auth about observability, which would
// couple two things that have no business knowing about each other.
//
// Safe for concurrent use: request handlers in this codebase fan out into
// goroutines (see translationmemory/handler_concordance.go), which would
// otherwise race with a middleware still writing identity fields.
type Scope struct {
	mu     sync.RWMutex
	fields map[string]slog.Attr

	// userEmail is deliberately a dedicated slot, NOT a field in `fields`:
	// Honeybadger notices carry it as the SDK's user_email convention,
	// but it must never reach the ECS log
	// output — logs are retained in-cluster for 90 days, and the index
	// template is dynamic:false anyway (an unmapped field would land in
	// _source un-indexed). Keeping it out of Attrs()/Fields() makes that
	// constraint structural rather than conventional.
	userEmail string

	// impersonatingUserEmail is the address of the user being impersonated — the
	// owner of the org a platform operator is viewing ("log in as org").
	// Empty on every ordinary session. Notice-only for the same reason as
	// userEmail, and the same structural exclusion applies.
	impersonatingUserEmail string

	// impersonating marks the session as a "log in as org" one. It
	// pairs with impersonatingUserEmail rather than being inferred from it,
	// because an org with no owner row resolves no address — and that is exactly
	// the session a reader must not mistake for an ordinary one. Notice-only
	// like its pair, so the ECS index template needs no new mapping.
	impersonating bool
}

// NewScope returns an empty Scope. Prefer WithScope, which also installs it.
func NewScope() *Scope {
	return &Scope{fields: make(map[string]slog.Attr, 8)}
}

type scopeKey struct{}

// WithScope installs a fresh Scope on ctx and returns both. If ctx already
// carries one it is returned unchanged, so nesting is harmless — a background
// task started from a request context keeps the request's fields.
func WithScope(ctx context.Context) (context.Context, *Scope) {
	if s := ScopeFromContext(ctx); s != nil {
		return ctx, s
	}
	s := NewScope()
	return context.WithValue(ctx, scopeKey{}, s), s
}

// ScopeFromContext returns the Scope on ctx, or nil. Callers must tolerate nil:
// unit tests, CLI commands (cmd/seed), and worker tasks constructed from
// context.Background() all legitimately have no scope.
func ScopeFromContext(ctx context.Context) *Scope {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(scopeKey{}).(*Scope)
	return s
}

// Attach records attributes on ctx's Scope, if it has one. A no-op otherwise,
// so a handler can enrich the request's context unconditionally without caring
// whether it is running under the HTTP middleware, a worker task, or a test.
//
// Use it wherever a handler has just loaded an entity that the URL did not name:
// /api/jobs/{jobID}/mt-apply reveals the job, but the project and file only
// become known once something has queried for them, and those are what a log
// line or an error notice needs to be actionable.
func Attach(ctx context.Context, attrs ...slog.Attr) {
	ScopeFromContext(ctx).Set(attrs...)
}

// Set records an attribute, replacing any previous value for the same key.
// Nil-receiver safe so call sites don't need to guard on a missing scope.
func (s *Scope) Set(attrs ...slog.Attr) {
	if s == nil || len(attrs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range attrs {
		if a.Key == "" {
			continue
		}
		s.fields[a.Key] = a
	}
}

// Attrs returns the scope's attributes sorted by key. Sorted rather than
// insertion-ordered so log output and tests are deterministic.
func (s *Scope) Attrs() []slog.Attr {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.fields) == 0 {
		return nil
	}
	out := make([]slog.Attr, 0, len(s.fields))
	for _, a := range s.fields {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// SetUserEmail records the authenticated user's email for error notices only.
// It is excluded from Attrs() and Fields() on purpose — see the field comment.
// Nil-receiver safe like Set.
func (s *Scope) SetUserEmail(email string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userEmail = email
}

// UserEmail returns the email recorded by SetUserEmail, or "".
func (s *Scope) UserEmail() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.userEmail
}

// SetImpersonatingUserEmail records the impersonated user's address for error
// notices only. Set exclusively on impersonated sessions; the same exclusion
// from Attrs()/Fields() applies as for SetUserEmail.
func (s *Scope) SetImpersonatingUserEmail(email string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.impersonatingUserEmail = email
}

// ImpersonatingUserEmail returns the address recorded by
// SetImpersonatingUserEmail, or "".
func (s *Scope) ImpersonatingUserEmail() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.impersonatingUserEmail
}

// SetImpersonating marks the session as impersonated, for error notices only.
// Independent of SetImpersonatingUserEmail: the address is best-effort, the
// flag is not.
func (s *Scope) SetImpersonating() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.impersonating = true
}

// Impersonating reports whether SetImpersonating was called.
func (s *Scope) Impersonating() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.impersonating
}

// Fields returns a flat copy of the scope for consumers that want plain values
// rather than slog.Attr — internal/errtrack builds Honeybadger context from it.
func (s *Scope) Fields() map[string]any {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(s.fields))
	for k, a := range s.fields {
		out[k] = a.Value.Any()
	}
	return out
}
