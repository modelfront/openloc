package oauthserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Settings → "Connected apps": what the USER has authorized, with revocation.
// Mounted inside the authenticated /api group (browser session), scoped to
// the calling user — you see and disconnect only your own grants.

type connectionOut struct {
	FamilyID   string     `json:"family_id"`
	ClientName string     `json:"client_name"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// HandleListConnections lists the caller's live OAuth grants in this org.
func (s *Server) HandleListConnections(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	rows, err := s.q.ListOAuthConnections(r.Context(), db.ListOAuthConnectionsParams{
		UserID: claims.UserID, TenantID: claims.TenantID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "listing oauth connections failed", "oauth-connections-list-failed")
		httputil.ServerError(w, "list", "connected apps")
		return
	}
	out := make([]connectionOut, 0, len(rows))
	for _, c := range rows {
		item := connectionOut{
			FamilyID:   uuid.UUID(c.FamilyID.Bytes).String(),
			ClientName: c.ClientName,
			Scope:      c.Scope,
			CreatedAt:  c.CreatedAt.Time,
		}
		if c.LastUsedAt.Valid {
			t := c.LastUsedAt.Time
			item.LastUsedAt = &t
		}
		out = append(out, item)
	}
	httputil.Data(w, out)
}

// HandleRevokeConnection disconnects one grant (the whole token family).
func (s *Server) HandleRevokeConnection(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	family, err := uuid.Parse(chi.URLParam(r, "familyID"))
	if err != nil {
		httputil.BadRequest(w, "invalid connection id")
		return
	}
	n, err := s.q.RevokeOAuthConnection(r.Context(), db.RevokeOAuthConnectionParams{
		FamilyID: pgtype.UUID{Bytes: family, Valid: true},
		UserID:   claims.UserID,
		TenantID: claims.TenantID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "revoking oauth connection failed", "oauth-connection-revoke-failed")
		httputil.ServerError(w, "revoke", "connected app")
		return
	}
	if n == 0 {
		httputil.NotFound(w, "connection")
		return
	}
	httputil.NoContent(w)
}
