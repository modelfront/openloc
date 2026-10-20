package auth

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// The shared demo organization: one tenant of kind 'demo'
// that any signed-in user may enter. A visitor acts as the demo org's guide
// (its owner), gets no membership, and cannot change anything: the barrier in
// checkTenantActive refuses every write, whatever the route.

// TenantKindDemo is tenants.kind for the demo organization.
const TenantKindDemo = "demo"

// demoWriteAllowed are the POST routes that only read: tour voice, the
// editor's spell-check underlines, and the prompt preview (the context body
// and output schema of a draft, built without the database). Everything else that is not GET/HEAD is
// refused, and /mcp is refused outright (its reads are POSTs that write an
// audit row).
var demoWriteAllowed = []*regexp.Regexp{
	regexp.MustCompile(`^/api/chat/speech$`),
	regexp.MustCompile(`^/api/spellcheck$`),
	regexp.MustCompile(`^/api/jobs/\d+/spellcheck$`),
	regexp.MustCompile(`^/api/ai/prompts/preview$`),
}

func demoRequestAllowed(r *http.Request) bool {
	if r.URL.Path == "/mcp" || r.URL.Path == "/mcp/" {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	case http.MethodPost:
		for _, re := range demoWriteAllowed {
			if re.MatchString(r.URL.Path) {
				return true
			}
		}
	}
	return false
}

// demoIdentityAllowed limits a demo token on the identity routes, which the
// tenant barrier does not cover.
func demoIdentityAllowed(r *http.Request) bool {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/auth/me":
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/api/auth/demo/leave":
		return true
	}
	return false
}

func writeDemoReadOnly(w http.ResponseWriter) {
	httputil.ErrorCode(w, http.StatusForbidden, "demo_read_only",
		"This is the Playground. Changes won’t be saved.")
}

func writeDemoUnavailable(w http.ResponseWriter) {
	httputil.ErrorCode(w, http.StatusServiceUnavailable, "demo_unavailable", "The Playground is not available right now.")
}

type demoKey struct{}

func withDemo(ctx context.Context) context.Context { return context.WithValue(ctx, demoKey{}, true) }

// IsDemo reports a request inside the demo organization. Handlers that write
// on a GET (lazy defaults) skip the write when it is set.
func IsDemo(ctx context.Context) bool {
	v, _ := ctx.Value(demoKey{}).(bool)
	return v
}

// HandleEnterDemo is POST /api/auth/demo/enter: any signed-in user, from any
// org or none, gets a demo token. Nothing is written. A demo token never gets
// here (demoIdentityAllowed), so a visit cannot nest.
func (h *Handler) HandleEnterDemo(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !h.demoOrg {
		writeDemoUnavailable(w)
		return
	}
	demo, err := h.queries.GetDemoTenant(r.Context())
	if errors.Is(err, pgx.ErrNoRows) {
		writeDemoUnavailable(w)
		return
	}
	if err != nil {
		logging.Error(r.Context(), err, "loading the demo organization failed", "demo-enter-failed")
		httputil.ServerError(w, "load", "the Playground")
		return
	}
	guide, err := h.queries.GetTenantOwner(r.Context(), demo.ID)
	if err != nil {
		logging.Error(r.Context(), err, "loading the demo guide failed", "demo-enter-failed")
		httputil.ServerError(w, "load", "the Playground")
		return
	}
	token, err := GenerateDemoToken(guide.ID, demo.ID, claims.UserID, claims.TenantID, h.jwtSecret)
	if err != nil {
		httputil.ServerError(w, "generate", "token")
		return
	}
	setTokenCookie(w, token)
	logging.Info(r.Context(), "entered the demo organization", "demo-entered")
	httputil.Data(w, map[string]any{
		"token":  token,
		"tenant": map[string]any{"id": demo.ID, "name": demo.Name, "slug": demo.Slug},
	})
}

// HandleLeaveDemo is POST /api/auth/demo/leave: back to the org the visitor
// came from, if they are still a member; otherwise to the org picker.
func (h *Handler) HandleLeaveDemo(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok || !claims.Demo {
		httputil.ErrorCode(w, http.StatusConflict, "not_in_demo", "you are not in the Playground")
		return
	}
	if claims.RealTenantID != 0 {
		m, err := h.queries.GetMembership(r.Context(), db.GetMembershipParams{UserID: claims.RealUserID, TenantID: claims.RealTenantID})
		switch {
		case err == nil:
			token, err := GenerateToken(claims.RealUserID, claims.RealTenantID, m.Role, h.jwtSecret, tokenExpiry)
			if err != nil {
				httputil.ServerError(w, "generate", "token")
				return
			}
			setTokenCookie(w, token)
			httputil.Data(w, map[string]any{"token": token, "needs_org_selection": false})
			return
		case !errors.Is(err, pgx.ErrNoRows):
			logging.Error(r.Context(), err, "loading the visitor's membership failed", "demo-leave-failed")
			httputil.ServerError(w, "load", "membership")
			return
		}
	}
	token, err := GenerateIdentityToken(claims.RealUserID, h.jwtSecret)
	if err != nil {
		httputil.ServerError(w, "generate", "token")
		return
	}
	setTokenCookie(w, token)
	httputil.Data(w, map[string]any{"token": token, "needs_org_selection": true})
}
