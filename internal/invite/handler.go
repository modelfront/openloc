package invite

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

const inviteExpiry = 7 * 24 * time.Hour // 7 days

// Handler handles invite endpoints.
type Handler struct {
	queries   db.Querier
	jwtSecret string
	svc       *Service
}

// NewHandler serves the invite routes through svc, the one Service the
// process shares with the MCP tools.
func NewHandler(q db.Querier, jwtSecret string, svc *Service) *Handler {
	return &Handler{queries: q, jwtSecret: jwtSecret, svc: svc}
}

// HandleCreate creates an invite (managers; see Service.Create for the rules).
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	inv, err := h.svc.Create(r.Context(), auth.ActorFromContext(r.Context()), req.Email, req.Role)
	if err != nil {
		writeError(w, r, err, "create", "invite.create_failed")
		return
	}
	httputil.Created(w, inv)
}

// writeError answers a service error: a refused rule with its own status, an
// invite that is missing or out of the caller's reach with 404, anything else
// with a 500, logged with attrs.
func writeError(w http.ResponseWriter, r *http.Request, err error, verb, action string, attrs ...slog.Attr) {
	var rule RuleError
	switch {
	case errors.As(err, &rule):
		if rule.Status == http.StatusTooManyRequests {
			httputil.TooManyRequests(w, rule.RetryAfter, rule.Msg)
			return
		}
		httputil.Error(w, rule.Status, rule.Msg)
	case errors.Is(err, pgx.ErrNoRows):
		httputil.NotFound(w, "invite")
	default:
		logging.Error(r.Context(), err, verb+" invite failed", action, attrs...)
		httputil.ServerError(w, verb, "invite")
	}
}

// HandleList returns the current tenant's invites the caller may handle
// (managers; see mayHandle).
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	invites, err := List(r.Context(), h.queries, auth.ActorFromContext(r.Context()))
	if err != nil {
		logging.Error(r.Context(), err, "listing invites failed", "invite.list_failed")
		httputil.ServerError(w, "list", "invites")
		return
	}
	httputil.Data(w, invites)
}

// HandleDelete revokes an invite the caller may handle (managers; see
// mayHandle).
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "inviteID")
	if err != nil {
		httputil.BadRequest(w, "invalid invite ID")
		return
	}
	if err := Revoke(r.Context(), h.queries, auth.ActorFromContext(r.Context()), id); err != nil {
		writeError(w, r, err, "delete", "invite.delete_failed", logging.InviteID(id))
		return
	}
	httputil.NoContent(w)
}

// HandleResend emails an invite the caller may handle again, with a new link
// (managers; see Service.Resend).
func (h *Handler) HandleResend(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "inviteID")
	if err != nil {
		httputil.BadRequest(w, "invalid invite ID")
		return
	}
	inv, err := h.svc.Resend(r.Context(), auth.ActorFromContext(r.Context()), id)
	if err != nil {
		writeError(w, r, err, "resend", "invite.resend_failed", logging.InviteID(id))
		return
	}
	httputil.Data(w, inv)
}

// HandleLookup validates an invite token for the accept page:
// GET /api/invites/lookup/{token} — public, rate-limited. Unknown, revoked,
// accepted, and expired tokens all return a flat 404 so the endpoint isn't a
// token oracle. `user_exists` tells the page whether to offer the set-password
// form (new identity) or a "sign in to accept" CTA (existing identity — the
// accept endpoint never touches existing accounts, see auth.HandleAcceptInvite).
func (h *Handler) HandleLookup(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	inv, err := h.queries.GetPendingUnexpiredInviteByToken(r.Context(), token)
	if err != nil {
		httputil.NotFound(w, "invite")
		return
	}
	tenant, err := h.queries.GetTenant(r.Context(), inv.TenantID)
	if err != nil {
		httputil.ServerError(w, "load", "organization")
		return
	}
	userExists, hasPassword := false, false
	if u, err := h.queries.GetUserByEmail(r.Context(), inv.Email); err == nil {
		userExists = true
		hasPassword = u.PasswordHash != nil && *u.PasswordHash != ""
	}
	httputil.Data(w, map[string]any{
		"email":        inv.Email,
		"org_name":     tenant.Name,
		"role":         inv.Role,
		"user_exists":  userExists,
		"has_password": hasPassword,
	})
}

// NOTE: invites are accepted by signing in via
// the invite link — Google/Microsoft (`/api/auth/{provider}?invite=TOKEN`,
// resolved in the OAuth callback via auth.materializeMemberships) — or, for a
// brand-new identity, by setting a password (auth.HandleAcceptInvite). The
// `invites` row is the durable grant either way.

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}
