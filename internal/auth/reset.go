package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Password reset. Also the sanctioned way to ADD a password to an
// OAuth-only account: the reset email proves address ownership, the same
// invariant OAuth sign-in and the invite email rest on.
const resetTokenExpiry = time.Hour

// HandleForgotPassword requests a reset link: POST /api/auth/forgot-password
// {email}. Always 200 with the same body, whether or not an account exists —
// the endpoint must not be an account oracle. A new request invalidates any
// earlier tokens for the user.
func (h *Handler) HandleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	email := normalizeEmail(req.Email)
	if email == "" {
		httputil.BadRequest(w, "email is required")
		return
	}

	// Everything past validation is best-effort behind the uniform 200: a DB
	// or email failure is logged, never surfaced (it would leak existence).
	if user, err := h.queries.GetUserByEmail(r.Context(), email); err == nil && user.IsActive {
		h.issueResetToken(r.Context(), user)
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// issueResetToken rotates the user's reset token and emails the link.
func (h *Handler) issueResetToken(ctx context.Context, user db.User) {
	if err := h.queries.DeletePasswordResetTokensForUser(ctx, user.ID); err != nil {
		logging.Error(ctx, err, "password reset: clearing old tokens failed", "auth-reset-clear-failed", logging.UserID(user.ID))
		return
	}
	raw, err := newAuthToken()
	if err != nil {
		logging.Error(ctx, err, "password reset: token generation failed", "auth-reset-generate-failed", logging.UserID(user.ID))
		return
	}
	if _, err := h.queries.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
		UserID:    user.ID,
		TokenHash: HashPAT(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(resetTokenExpiry), Valid: true},
	}); err != nil {
		logging.Error(ctx, err, "password reset: storing the token failed", "auth-reset-store-failed", logging.UserID(user.ID))
		return
	}
	h.sendPasswordResetEmail(ctx, user.Email, raw)
}

// sendPasswordResetEmail delivers the reset link. No-op without a sender.
func (h *Handler) sendPasswordResetEmail(ctx context.Context, email, rawToken string) {
	if h.sender == nil {
		return
	}
	link := h.appLink("/reset-password/" + rawToken)
	subject := "Reset your OpenLoc password"
	content := emaillayout.Content{
		Heading: "Reset your password",
		Body:    emaillayout.P("Someone requested a password reset for your OpenLoc account."),
		Button:  &emaillayout.Button{Label: "Set a new password", URL: link},
		Notes: emaillayout.Join(
			emaillayout.Note("This link expires in 1 hour and can be used once. If you didn't request this, you can ignore this email."),
			emaillayout.LinkFallback(link),
		),
	}
	h.sendLayoutEmail(ctx, email, subject, content, "password-reset")
}

// HandleResetPassword redeems a reset link: POST /api/auth/reset-password
// {token, password}. Single-use; on success the new password is set and the
// user is logged in (the email round-trip just proved their identity).
func (h *Handler) HandleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if err := ValidatePassword(req.Password); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	// Unknown, used, and expired tokens are indistinguishable.
	row, err := h.queries.GetValidPasswordResetToken(r.Context(), HashPAT(req.Token))
	if err != nil {
		httputil.ErrorCode(w, http.StatusBadRequest, "invalid_token",
			"this reset link is invalid or has expired — request a new one")
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		httputil.ServerError(w, "hash", "password")
		return
	}

	// Set + consume atomically so a crash between the two can't leave a live
	// token for an already-changed password.
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "update", "password")
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)
	if err := q.SetUserPassword(r.Context(), db.SetUserPasswordParams{ID: row.UserID, PasswordHash: &hash}); err != nil {
		httputil.ServerError(w, "update", "password")
		return
	}
	if err := q.MarkPasswordResetTokenUsed(r.Context(), row.ID); err != nil {
		httputil.ServerError(w, "update", "password")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "update", "password")
		return
	}

	user, err := h.queries.GetUser(r.Context(), row.UserID)
	if err != nil {
		httputil.ServerError(w, "load", "user")
		return
	}
	h.materializeMemberships(r.Context(), user.ID, user.Email, "")
	h.respondLoggedIn(w, r.Context(), user)
}
