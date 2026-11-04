package filterprofile

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Handler serves /api/filter-profiles. Every member may read; writes are for
// owners, admins and project managers (the routes carry RequireManager).
type Handler struct {
	svc *Service
}

// NewHandler wires the handler.
func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{svc: NewService(q, pool)}
}

// Routes mounts the handlers on r; manager wraps the write routes.
func (h *Handler) Routes(r chi.Router, manager func(http.Handler) http.Handler) {
	r.Get("/", h.HandleList)
	r.Get("/{profileID}", h.HandleGet)
	r.Group(func(r chi.Router) {
		r.Use(manager)
		r.Put("/defaults", h.HandleSetDefault)
		r.Post("/", h.HandleCreate)
		r.Put("/{profileID}", h.HandleUpdate)
		r.Delete("/{profileID}", h.HandleDelete)
	})
}

const resource = "filter profile"

// profileIDParam reads the {profileID} path parameter; false means a 400 was
// written.
func profileIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid filter profile id")
		return 0, false
	}
	return id, true
}

// HandleList handles GET /api/filter-profiles[?format=xlsx].
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	profiles, err := h.svc.List(r.Context(), auth.TenantIDFromContext(r.Context()), r.URL.Query().Get("format"))
	if httputil.ServiceError(w, err, "list", resource+"s") {
		return
	}
	httputil.Data(w, profiles)
}

// HandleGet handles GET /api/filter-profiles/{profileID}.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := profileIDParam(w, r)
	if !ok {
		return
	}
	profile, err := h.svc.Get(r.Context(), auth.TenantIDFromContext(r.Context()), id)
	if httputil.ServiceError(w, err, "get", resource) {
		return
	}
	httputil.Data(w, profile)
}

// HandleCreate handles POST /api/filter-profiles.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	profile, err := h.svc.Create(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), in)
	if httputil.ServiceError(w, err, "create", resource) {
		return
	}
	httputil.Created(w, profile)
}

// HandleUpdate handles PUT /api/filter-profiles/{profileID}: a full replace
// that must send the updated_at it read.
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	id, ok := profileIDParam(w, r)
	if !ok {
		return
	}
	profile, err := h.svc.Update(r.Context(), auth.TenantIDFromContext(r.Context()), id, in)
	if httputil.ServiceError(w, err, "update", resource) {
		return
	}
	httputil.Data(w, profile)
}

// HandleDelete handles DELETE /api/filter-profiles/{profileID}.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := profileIDParam(w, r)
	if !ok {
		return
	}
	err := h.svc.Delete(r.Context(), auth.TenantIDFromContext(r.Context()), id)
	if httputil.ServiceError(w, err, "delete", resource) {
		return
	}
	httputil.NoContent(w)
}

// HandleSetDefault handles PUT /api/filter-profiles/defaults: the org
// default of one format; a null filter_profile_id goes back to the built-in
// Default. The answer lists every format's default.
func (h *Handler) HandleSetDefault(w http.ResponseWriter, r *http.Request) {
	var in DefaultInput
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	defaults, err := h.svc.SetDefault(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), in)
	if httputil.ServiceError(w, err, "set", resource+" default") {
		return
	}
	httputil.Data(w, defaults)
}
