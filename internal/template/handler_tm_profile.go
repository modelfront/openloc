package template

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/settings"
	"openloc/internal/tmprofile"
)

// TM profile link management for a template — the same two directions as the
// project endpoints (linking itself goes through PATCH /{templateID}).
// Manager-level like the rest of /api/templates.

// HandleDetachTMProfile copies the linked profile's TM rows + match scoring
// onto the template's own settings and clears the link.
// POST /api/templates/{templateID}/tms/detach
func (h *Handler) HandleDetachTMProfile(w http.ResponseWriter, r *http.Request) {
	tmpl, ok := h.resolveTemplate(w, r)
	if !ok {
		return
	}
	after, err := settings.DetachTMProfileTx(r.Context(), h.pool, tmpl.SettingsProfileID)
	if err != nil {
		if msg, ok := httputil.AsClientError(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		logging.Error(r.Context(), err, "detach TM profile from template", "tm-profile-detach-failed")
		httputil.ServerError(w, "detach", "TM profile")
		return
	}
	httputil.Data(w, h.newTemplateResponse(r.Context(), tmpl, after))
}

// HandleSaveTMProfile creates a TM profile from the template's current TM scope
// and, with link=true, points the template at it in the same transaction.
// POST /api/templates/{templateID}/save-tm-profile {name, description?, link?}
func (h *Handler) HandleSaveTMProfile(w http.ResponseWriter, r *http.Request) {
	tmpl, ok := h.resolveTemplate(w, r)
	if !ok {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Link        bool   `json:"link"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "start", "transaction")
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)

	createdBy := auth.UserIDPtr(auth.UserIDFromContext(r.Context()))
	tp, err := tmprofile.CreateFromSettingsProfile(r.Context(), q, tenantID, createdBy, req.Name, req.Description, tmpl.SettingsProfileID)
	if err != nil {
		writeTMProfileErr(w, err)
		return
	}
	if req.Link {
		if _, err := settings.LinkTMProfile(r.Context(), q, tenantID, tmpl.SettingsProfileID, tp.ID); err != nil {
			writeTMProfileErr(w, err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "commit", "TM profile")
		return
	}
	httputil.Created(w, map[string]any{"tm_profile": settings.TMProfileRef{ID: tp.ID, Name: tp.Name}, "linked": req.Link})
}

// resolveTemplate parses the template ID and verifies tenant ownership.
func (h *Handler) resolveTemplate(w http.ResponseWriter, r *http.Request) (db.Template, bool) {
	id, err := httputil.ParseID(r, "templateID")
	if err != nil {
		httputil.BadRequest(w, "invalid template ID")
		return db.Template{}, false
	}
	tmpl, err := h.queries.GetTemplate(r.Context(), db.GetTemplateParams{ID: id, TenantID: auth.TenantIDFromContext(r.Context())})
	if httputil.DBError(w, err, "template") {
		return db.Template{}, false
	}
	return tmpl, true
}

func writeTMProfileErr(w http.ResponseWriter, err error) {
	if msg, ok := httputil.AsClientError(err); ok {
		httputil.BadRequest(w, msg)
		return
	}
	if httputil.IsUniqueViolation(err) {
		httputil.Error(w, http.StatusConflict, "a TM profile with this name already exists")
		return
	}
	httputil.ServerError(w, "save", "TM profile")
}
