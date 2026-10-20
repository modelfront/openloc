package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// HandleAcceptInvite creates a password account from an invite link:
// POST /api/auth/accept-invite {token, password, name?}.
//
// NEW identities only — if the invited email already has an account, this
// returns 409 account_exists and the user signs in normally instead (any
// sign-in materializes the pending invite). The token reaches only the invited
// mailbox — no tenant API returns it; only a platform operator's hand-over link
// for a new org's owner does — so the invite delivery itself is the address
// verification, and the account is created with the invite's email — never a
// client-supplied one. It still never sets a password on an EXISTING account:
// a forwarded or leaked link would then take that account over.
func (h *Handler) HandleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Token == "" {
		httputil.BadRequest(w, "token is required")
		return
	}

	// Unknown, revoked, accepted, and expired tokens are indistinguishable —
	// a flat 404 keeps the token from being an oracle.
	inv, err := h.queries.GetPendingUnexpiredInviteByToken(r.Context(), req.Token)
	if err != nil {
		httputil.NotFound(w, "invite")
		return
	}

	if err := ValidatePassword(req.Password); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if nameTooLong(w, name) {
		return
	}

	if _, err := h.queries.GetUserByEmail(r.Context(), inv.Email); err == nil {
		httputil.ErrorCode(w, http.StatusConflict, "account_exists",
			"an account for this email already exists — sign in to accept the invitation")
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		httputil.ServerError(w, "load", "user")
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		httputil.ServerError(w, "hash", "password")
		return
	}
	if name == "" {
		name = inv.Email
	}
	user, err := h.queries.CreateUser(r.Context(), db.CreateUserParams{
		Email:        inv.Email,
		PasswordHash: &hash,
		Name:         name,
	})
	if err != nil {
		// A concurrent signup for the same email lost the race on the unique
		// index — same outcome as the pre-check above.
		httputil.ErrorCode(w, http.StatusConflict, "account_exists",
			"an account for this email already exists — sign in to accept the invitation")
		return
	}

	// Materialize this invite (plus any others for the email) and log in.
	h.materializeMemberships(r.Context(), user.ID, user.Email, req.Token)
	h.respondLoggedIn(w, r.Context(), user)
}
