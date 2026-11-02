package segmentationapi

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/segmentation"
)

// Handler serves /api/segmentation-profiles. Every member may read and preview;
// writes are for owners, admins and project managers (the routes carry
// RequireManager).
type Handler struct {
	svc *Service
	// previews limits each user's preview requests: a preview runs rules the
	// caller wrote, up to the engine's work budget.
	previews *httputil.KeyedLimiter[int64]
}

// Previews per user per second, and the burst a user may type in.
const (
	previewRate  = 3
	previewBurst = 10
)

// NewHandler wires the handler.
func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{svc: NewService(q, pool), previews: httputil.NewKeyedLimiter[int64](previewRate, previewBurst, 0, "segmentation-preview")}
}

// Routes mounts the handlers on r; manager wraps the write routes.
func (h *Handler) Routes(r chi.Router, manager func(http.Handler) http.Handler) {
	r.Get("/", h.HandleList)
	r.Get("/{profileID}", h.HandleGet)
	r.Get("/{profileID}/srx", h.HandleExportSRX)
	r.Post("/preview", h.HandlePreview)
	r.Group(func(r chi.Router) {
		r.Use(manager)
		r.Post("/", h.HandleCreate)
		r.Put("/{profileID}", h.HandleUpdate)
		r.Delete("/{profileID}", h.HandleDelete)
		r.Post("/srx/parse", h.HandleParseSRX)
	})
}

const resource = "segmentation profile"

// profileIDParam reads the {profileID} path parameter; false means a 400 was
// written.
func profileIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid segmentation profile id")
		return 0, false
	}
	return id, true
}

// HandleList handles GET /api/segmentation-profiles[?source_language=fr].
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	profiles, err := h.svc.List(r.Context(), auth.TenantIDFromContext(r.Context()), r.URL.Query().Get("source_language"))
	if httputil.ServiceError(w, err, "list", resource+"s") {
		return
	}
	httputil.Data(w, profiles)
}

// HandleGet handles GET /api/segmentation-profiles/{profileID}.
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

// HandleCreate handles POST /api/segmentation-profiles.
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

// HandleUpdate handles PUT /api/segmentation-profiles/{profileID}: a full replace
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

// HandleDelete handles DELETE /api/segmentation-profiles/{profileID}.
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

// HandlePreview handles POST /api/segmentation-profiles/preview.
func (h *Handler) HandlePreview(w http.ResponseWriter, r *http.Request) {
	if ok, wait := h.previews.Take(auth.UserIDFromContext(r.Context())); !ok {
		httputil.TooManyRequests(w, wait, "Too many previews at once. Wait a moment.")
		return
	}
	var in PreviewInput
	r.Body = http.MaxBytesReader(w, r.Body, previewBodyLimit)
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	p, err := h.svc.Preview(r.Context(), auth.TenantIDFromContext(r.Context()), in)
	if httputil.ServiceError(w, err, "preview", resource) {
		return
	}
	httputil.Data(w, p)
}

// previewBodyLimit fits the largest rule set and sample text.
const previewBodyLimit = 4 << 20

// HandleParseSRX handles POST /api/segmentation-profiles/srx/parse: a
// multipart upload ("file") read into unsaved drafts and warnings. Nothing is
// stored; the client saves the drafts it keeps through POST
// /api/segmentation-profiles.
func (h *Handler) HandleParseSRX(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, segmentation.MaxSRXSize+64<<10)
	if err := r.ParseMultipartForm(segmentation.MaxSRXSize); err != nil {
		httputil.BadRequest(w, "The file is too large or the form is invalid.")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "missing file field")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, segmentation.MaxSRXSize+1))
	if err != nil {
		httputil.BadRequest(w, "The file could not be read.")
		return
	}
	imp, err := ParseSRX(data)
	if httputil.ServiceError(w, err, "read", "SRX file") {
		return
	}
	httputil.Data(w, imp)
}

// HandleExportSRX handles GET /api/segmentation-profiles/{profileID}/srx.
func (h *Handler) HandleExportSRX(w http.ResponseWriter, r *http.Request) {
	id, ok := profileIDParam(w, r)
	if !ok {
		return
	}
	name, data, err := h.svc.ExportSRX(r.Context(), auth.TenantIDFromContext(r.Context()), id)
	if httputil.ServiceError(w, err, "export", resource) {
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	httputil.SetAttachmentFilename(w, name)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
