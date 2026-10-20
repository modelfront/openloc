package project

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/projectevents"
	"openloc/internal/settings"
	"openloc/internal/tmprofile"
)

// TM profile link management for a project. Linking happens through
// PATCH /settings (tm_profile_id); these two are the other directions:
// leaving a profile (detach) and turning the current TM setup into one.
// Both are manager-level like the rest of the /api/projects group.

// HandleDetachTMProfile copies the linked profile's TM rows + match scoring
// onto the project's own settings and clears the link.
// POST /api/projects/{projectID}/tms/detach
func (h *Handler) HandleDetachTMProfile(w http.ResponseWriter, r *http.Request) {
	project, ok := h.resolveProjectProfile(w, r)
	if !ok {
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	before, err := h.queries.GetSettingsProfile(r.Context(), project.SettingsProfileID)
	if err != nil {
		httputil.ServerError(w, "get", "settings profile")
		return
	}
	after, err := settings.DetachTMProfileTx(r.Context(), h.pool, project.SettingsProfileID)
	if err != nil {
		if msg, ok := httputil.AsClientError(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		logging.Error(r.Context(), err, "detach TM profile", "tm-profile-detach-failed", logging.ProjectID(project.ID))
		httputil.ServerError(w, "detach", "TM profile")
		return
	}
	h.emitSettingsDiff(r, project, tenantID, before, after)
	httputil.Data(w, h.newProjectSettings(r.Context(), tenantID, project, after))
}

// HandleSaveTMProfile creates a TM profile from the project's current TM scope
// (its own rows, or the profile it already follows) and, with link=true, points
// the project at the new profile in the same transaction.
// POST /api/projects/{projectID}/save-tm-profile {name, description?, link?}
func (h *Handler) HandleSaveTMProfile(w http.ResponseWriter, r *http.Request) {
	project, ok := h.resolveProjectProfile(w, r)
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
	before, err := h.queries.GetSettingsProfile(r.Context(), project.SettingsProfileID)
	if err != nil {
		httputil.ServerError(w, "get", "settings profile")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "start", "transaction")
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)

	createdBy := auth.UserIDPtr(auth.UserIDFromContext(r.Context()))
	tp, err := tmprofile.CreateFromSettingsProfile(r.Context(), q, tenantID, createdBy, req.Name, req.Description, project.SettingsProfileID)
	if err != nil {
		writeTMProfileErr(w, err)
		return
	}
	after := before
	if req.Link {
		if after, err = settings.LinkTMProfile(r.Context(), q, tenantID, project.SettingsProfileID, tp.ID); err != nil {
			writeTMProfileErr(w, err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "commit", "TM profile")
		return
	}
	h.emitSettingsDiff(r, project, tenantID, before, after)
	httputil.Created(w, map[string]any{"tm_profile": settings.TMProfileRef{ID: tp.ID, Name: tp.Name}, "linked": req.Link})
}

// emitSettingsDiff records a settings_updated event for whatever changed on
// the settings row (tm_profile_id, threshold, penalties…); no-ops emit nothing.
func (h *Handler) emitSettingsDiff(r *http.Request, project db.Project, tenantID int64, before, after db.SettingsProfile) {
	changes := projectevents.Diff(settingsMap(before), settingsMap(after))
	if changes == nil {
		return
	}
	actorID, actorName := projectevents.ActorFromContext(r.Context())
	projectevents.Emit(r.Context(), h.queries, projectevents.Event{
		ProjectID: project.ID,
		TenantID:  tenantID,
		Type:      projectevents.TypeSettingsUpdated,
		ActorID:   actorID,
		ActorName: actorName,
		Details:   map[string]any{"changes": changes},
	})
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
