package comments

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
)

type Handler struct {
	queries db.Querier
	svc     *Service
}

func NewHandler(q db.Querier) *Handler {
	return &Handler{queries: q, svc: NewService(q)}
}

func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.ParseInt(chi.URLParam(r, "translationID"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid translation ID")
		return
	}
	comments, err := h.svc.List(r.Context(), auth.TenantIDFromContext(r.Context()), tid)
	if errors.Is(err, ErrNotFound) {
		httputil.NotFound(w, "translation")
		return
	}
	if err != nil {
		httputil.ServerError(w, "list", "comments")
		return
	}
	httputil.Data(w, comments)
}

func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.ParseInt(chi.URLParam(r, "translationID"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid translation ID")
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if err := httputil.Decode(r, &req); err != nil || req.Content == "" {
		httputil.BadRequest(w, "content is required")
		return
	}
	ctx := r.Context()
	comment, err := h.svc.Create(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), tid, req.Content)
	if errors.Is(err, ErrNotFound) {
		httputil.NotFound(w, "translation")
		return
	}
	if err != nil {
		httputil.ServerError(w, "create", "comment")
		return
	}
	httputil.Created(w, comment)
}

func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.ParseInt(chi.URLParam(r, "translationID"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid translation ID")
		return
	}
	cid, err := strconv.ParseInt(chi.URLParam(r, "commentID"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid comment ID")
		return
	}
	err = h.svc.Delete(r.Context(), auth.TenantIDFromContext(r.Context()), auth.UserIDFromContext(r.Context()), tid, cid)
	if errors.Is(err, ErrNotFound) {
		httputil.NotFound(w, "comment")
		return
	}
	if err != nil {
		httputil.ServerError(w, "delete", "comment")
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) HandleCountByFile(w http.ResponseWriter, r *http.Request) {
	fileID, err := strconv.ParseInt(chi.URLParam(r, "fileID"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}
	// Verify file belongs to current tenant
	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetFileTenant(r.Context(), db.GetFileTenantParams{
		ID: fileID, TenantID: tenantID,
	}); err != nil {
		httputil.NotFound(w, "file")
		return
	}
	lang := langcode.Normalize(r.URL.Query().Get("lang"))
	if lang == "" {
		httputil.BadRequest(w, "lang query parameter required")
		return
	}
	if !auth.LinguistFileLanguageAccess(w, r, h.queries, fileID, lang) {
		return
	}
	counts, err := h.queries.CountCommentsByFile(r.Context(), db.CountCommentsByFileParams{
		FileID:         fileID,
		TargetLanguage: lang,
	})
	if err != nil {
		httputil.ServerError(w, "count", "comments")
		return
	}
	httputil.Data(w, counts)
}
