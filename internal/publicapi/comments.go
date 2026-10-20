package publicapi

import (
	"errors"
	"net/http"
	"strings"

	"openloc/internal/auth"
	"openloc/internal/comments"
	"openloc/internal/httputil"
)

const maxCommentLength = 5000

func (h Handlers) listComments(w http.ResponseWriter, r *http.Request) {
	ref := segmentFromContext(r)
	rows, err := h.Comments.List(r.Context(), auth.TenantIDFromContext(r.Context()), ref.translation.ID)
	if errors.Is(err, comments.ErrNotFound) {
		httputil.NotFound(w, "segment")
		return
	}
	if err != nil {
		httputil.ServerError(w, "list", "comments")
		return
	}
	writeAll(w, rows, commentDTO)
}

func (h Handlers) addComment(w http.ResponseWriter, r *http.Request) {
	ref := segmentFromContext(r)
	var req struct {
		Content string `json:"content"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" || len(content) > maxCommentLength {
		httputil.FieldError(w, http.StatusBadRequest, "content", "content must be 1 to 5000 characters")
		return
	}
	ctx := r.Context()
	c, err := h.Comments.Create(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), ref.translation.ID, content)
	if errors.Is(err, comments.ErrNotFound) {
		httputil.NotFound(w, "segment")
		return
	}
	if err != nil {
		httputil.ServerError(w, "create", "comment")
		return
	}
	httputil.Created(w, Comment{ID: c.ID, Content: c.Content, AuthorID: c.UserID, CreatedAt: timestamp(c.CreatedAt)})
}

// deleteComment removes the caller's own comment, or one an AI step wrote.
func (h Handlers) deleteComment(w http.ResponseWriter, r *http.Request) {
	ref := segmentFromContext(r)
	commentID, err := httputil.ParseID(r, "commentID")
	if err != nil {
		httputil.BadRequest(w, "invalid comment ID")
		return
	}
	ctx := r.Context()
	err = h.Comments.Delete(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), ref.translation.ID, commentID)
	if errors.Is(err, comments.ErrNotFound) {
		httputil.NotFound(w, "comment")
		return
	}
	if err != nil {
		httputil.ServerError(w, "delete", "comment")
		return
	}
	httputil.NoContent(w)
}
