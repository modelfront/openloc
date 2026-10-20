package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/issues"
	"openloc/internal/logging"
)

// A screenshot the user took for the assistant: the browser captures the page,
// the user approves it, and it is stored as an assistant upload. The message
// that sends it names the upload; the model sees the image in that message
// only, and its text marker afterwards.

// maxScreenshotBytes is the largest screenshot a model gets: a fixed cap,
// below what each vendor takes for one image, and what a report attaches.
const maxScreenshotBytes = issues.MaxScreenshotSize

// errNotScreenshot: the upload is gone, someone else's, too large or not an
// image. One answer for all, like an unknown upload id.
var errNotScreenshot = errors.New("not a screenshot")

// withScreenshotMarker is a message as stored with its screenshot: the marker
// after a blank line. The model reads the marker in every turn and passes the
// id on to the issue report; the panel leaves it out of the user's bubble
// (chat-history.ts). testdata/screenshot_message.json pins the shape for both.
func withScreenshotMarker(message, uploadID string) string {
	return message + "\n\n" + fmt.Sprintf("[Screenshot from the user (upload_id: %s). You see the image only in "+
		"the message that brought it; in later messages this note stands for it.]", uploadID)
}

// loadUploadImage reads one of the caller's unexpired uploads when it is an
// image of at most maxScreenshotBytes.
func (h *Handler) loadUploadImage(ctx context.Context, claims *auth.Claims, uploadID string) (*Image, error) {
	if h.storage == nil {
		return nil, errNotScreenshot
	}
	id, err := uuid.Parse(uploadID)
	if err != nil {
		return nil, errNotScreenshot
	}
	row, err := h.q.GetChatUpload(ctx, db.GetChatUploadParams{
		ID: pgtype.UUID{Bytes: id, Valid: true}, TenantID: claims.TenantID, UserID: claims.UserID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotScreenshot
	}
	if err != nil {
		return nil, fmt.Errorf("get upload: %w", err)
	}
	if row.SizeBytes > maxScreenshotBytes {
		return nil, errNotScreenshot
	}
	body, err := h.storage.Download(ctx, row.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("download upload: %w", err)
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxScreenshotBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}
	// The image types the app shows are the ones every vendor takes.
	mediaType, image := issues.SniffImage(data)
	if len(data) > maxScreenshotBytes || !image {
		return nil, errNotScreenshot
	}
	return &Image{MediaType: mediaType, Data: data}, nil
}

// HandleUploadImage is GET /api/chat/uploads/{uploadID}: one of the caller's
// own unexpired uploads, when it is an image — what the report form shows
// and attaches for a screenshot the assistant names. Anything else is 404.
func (h *Handler) HandleUploadImage(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	img, err := h.loadUploadImage(r.Context(), claims, chi.URLParam(r, "uploadID"))
	if errors.Is(err, errNotScreenshot) {
		httputil.Error(w, http.StatusNotFound, "The screenshot is gone. Uploads are kept for 24 hours.")
		return
	}
	if err != nil {
		logging.Error(r.Context(), err, "reading an assistant upload failed", "chat-upload-read-failed")
		httputil.ServerError(w, "load", "screenshot")
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", img.MediaType)
	hd.Set("Content-Length", strconv.Itoa(len(img.Data)))
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	hd.Set("Cache-Control", "private, max-age=3600")
	if _, err := w.Write(img.Data); err != nil {
		logging.Warn(r.Context(), err, "sending an assistant upload failed", "chat-upload-send-failed")
	}
}
