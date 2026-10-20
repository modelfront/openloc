// Package appearance stores per-user UI appearance preferences:
// which palette is active in light vs dark mode, plus the user's custom
// palettes. It is keyed by user_id, so the choice follows the user across every
// org they belong to (unlike tenant-scoped settings).
package appearance

import (
	"encoding/json"
	"net/http"
	"strings"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Handler serves GET/PUT /api/appearance for the authenticated user.
type Handler struct{ queries db.Querier }

func NewHandler(q db.Querier) *Handler { return &Handler{queries: q} }

type appearanceResponse struct {
	LightPalette   string          `json:"light_palette"`
	DarkPalette    string          `json:"dark_palette"`
	CustomPalettes json.RawMessage `json:"custom_palettes"`
}

// HandleGet returns the caller's appearance, or built-in defaults when no row
// exists yet (brand-new user).
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	a, err := h.queries.GetUserAppearance(r.Context(), userID)
	if err != nil {
		httputil.Data(w, appearanceResponse{
			LightPalette:   DefaultLightPalette,
			DarkPalette:    DefaultDarkPalette,
			CustomPalettes: json.RawMessage("[]"),
		})
		return
	}
	custom := a.CustomPalettes
	if len(custom) == 0 {
		custom = json.RawMessage("[]")
	}
	httputil.Data(w, appearanceResponse{
		LightPalette:   a.LightPalette,
		DarkPalette:    a.DarkPalette,
		CustomPalettes: custom,
	})
}

// HandleUpdate upserts the caller's appearance. Every UI constraint is enforced
// here too (never trust the client): palette ids non-empty, custom palettes
// well-formed with hex anchors, count/name bounded.
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())

	var req struct {
		LightPalette   string          `json:"light_palette"`
		DarkPalette    string          `json:"dark_palette"`
		CustomPalettes json.RawMessage `json:"custom_palettes"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if strings.TrimSpace(req.LightPalette) == "" || strings.TrimSpace(req.DarkPalette) == "" {
		httputil.BadRequest(w, "light_palette and dark_palette are required")
		return
	}

	customs, msg := validateCustoms(req.CustomPalettes)
	if msg != "" {
		httputil.BadRequest(w, msg)
		return
	}
	raw, err := json.Marshal(customs)
	if err != nil {
		httputil.ServerError(w, "encode", "custom palettes")
		return
	}

	a, err := h.queries.UpsertUserAppearance(r.Context(), db.UpsertUserAppearanceParams{
		UserID:         userID,
		LightPalette:   req.LightPalette,
		DarkPalette:    req.DarkPalette,
		CustomPalettes: raw,
	})
	if err != nil {
		httputil.ServerError(w, "save", "appearance")
		return
	}
	httputil.Data(w, appearanceResponse{
		LightPalette:   a.LightPalette,
		DarkPalette:    a.DarkPalette,
		CustomPalettes: a.CustomPalettes,
	})
}

// validateCustoms parses the custom palettes array and validates it with the
// shared rules in palettes.go, returning the cleaned slice (never nil) or a
// user-facing message.
func validateCustoms(raw json.RawMessage) ([]Palette, string) {
	if len(raw) == 0 {
		return []Palette{}, ""
	}
	var customs []Palette
	if err := json.Unmarshal(raw, &customs); err != nil {
		return nil, "custom_palettes must be a JSON array"
	}
	if err := ValidateCustomPalettes(customs); err != nil {
		return nil, err.Error()
	}
	return customs, ""
}
