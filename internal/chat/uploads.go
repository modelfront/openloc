package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Assistant uploads: the user attaches a file in the panel,
// the model passes its upload_id to an import tool. The id is only usable by
// the user who uploaded it, in their tenant, for 24 hours.

// SetStorage gives the handler somewhere to keep uploads. Without it the
// upload route answers 503.
func (h *Handler) SetStorage(s file.BlobStore) {
	h.storage = s
}

type uploadView struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	ExpiresAt string `json:"expires_at"`
}

// HandleUpload is POST /api/chat/uploads (multipart field "file").
func (h *Handler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.storage == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "File storage is not available.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, file.MaxUploadSize)
	if err := r.ParseMultipartForm(file.MaxUploadSize); err != nil {
		httputil.BadRequest(w, "The file is too large (max 50 MB) or the upload is malformed.")
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "A file is required.")
		return
	}
	defer f.Close()

	name := cleanUploadName(header.Filename)
	key, err := uploadKey(claims.TenantID, claims.UserID, name)
	if err != nil {
		httputil.ServerError(w, "store", "file")
		return
	}
	if err := h.storage.Put(r.Context(), key, f); err != nil {
		logging.Error(r.Context(), err, "storing an assistant upload failed", "chat-upload-store-failed")
		httputil.ServerError(w, "store", "file")
		return
	}
	row, err := h.q.InsertChatUpload(r.Context(), db.InsertChatUploadParams{
		TenantID: claims.TenantID, UserID: claims.UserID,
		Filename: name, SizeBytes: header.Size, StorageKey: key,
	})
	if err != nil {
		logging.Error(r.Context(), err, "recording an assistant upload failed", "chat-upload-record-failed")
		// Without a row the purge never sees the key, so the blob goes now.
		if derr := h.storage.DeleteKeys(context.WithoutCancel(r.Context()), []string{key}); derr != nil {
			logging.Error(r.Context(), derr, "removing an unrecorded assistant upload failed", "chat-upload-orphan-delete-failed")
		}
		httputil.ServerError(w, "save", "upload")
		return
	}
	httputil.Created(w, uploadView{
		ID:        row.ID.String(),
		Filename:  row.Filename,
		SizeBytes: row.SizeBytes,
		ExpiresAt: row.ExpiresAt.Time.UTC().Format(time.RFC3339),
	})
}

// cleanUploadName keeps the base name only, without path separators or
// control characters, capped in length. It is shown to the model and used in
// the storage key.
func cleanUploadName(raw string) string {
	name := filepath.Base(strings.ReplaceAll(raw, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' {
			return -1
		}
		return r
	}, name)
	if len(name) > 200 {
		name = name[:200]
	}
	if name == "" || name == "." {
		name = "upload"
	}
	return name
}

func uploadKey(tenantID, userID int64, name string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("chat-uploads/%d/%d/%s-%s", tenantID, userID, hex.EncodeToString(b), name), nil
}
