package file

import (
	"io"
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/projectevents"
)

// HandleDownloadSource serves the file exactly as it was uploaded — the
// customer's original document, no reconstruction.
//
// Backs the "Source file" item in the Download menu. It
// lives on the /api/files/{fileID} route group rather than the project group so
// linguists and vendors working in the CAT editor can reach it: that group
// already applies RequireFileOwnership + RequireLinguistFileAccess, and
// GetFileTenant below re-checks the tenant (defense in depth, matching the
// convention in the rest of this package).
//
// The body is buffered before any header is written: uploads are capped at
// MAX_UPLOAD_MB, so this is bounded, and a storage failure then returns a clean
// 500 instead of a silently truncated document landing in the customer's
// Downloads folder.
func (h *Handler) HandleDownloadSource(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	file, err := h.queries.GetFileTenant(r.Context(), db.GetFileTenantParams{
		ID: id, TenantID: tenantID,
	})
	if httputil.DBError(w, err, "file") {
		return
	}

	// Uploads made while object storage was unconfigured have no storage path
	// (HandleUpload skips the S3 write when h.storage is nil), so there is
	// genuinely nothing to serve. Say so plainly instead of nil-dereferencing.
	if h.storage == nil {
		httputil.Error(w, http.StatusServiceUnavailable, "file storage is not configured")
		return
	}
	if file.StoragePath == "" {
		httputil.Error(w, http.StatusNotFound, "the original upload was not stored for this file")
		return
	}

	reader, err := h.storage.Download(r.Context(), file.StoragePath)
	if err != nil {
		logging.Error(r.Context(), err, "source file download failed", "file-source-download-failed", logging.FileID(file.ID))
		httputil.ServerError(w, "download", "file")
		return
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		logging.Error(r.Context(), err, "source file read failed", "file-source-read-failed", logging.FileID(file.ID))
		httputil.ServerError(w, "download", "file")
		return
	}

	// Audit: emit only after the bytes are in hand, so a
	// failed download never shows up in project history as a download.
	actorID, actorName := projectevents.ActorFromContext(r.Context())
	projectevents.Emit(r.Context(), h.queries, projectevents.Event{
		ProjectID: file.ProjectID,
		TenantID:  tenantID,
		Type:      projectevents.TypeFileDownloaded,
		ActorID:   actorID,
		ActorName: actorName,
		FileID:    &file.ID,
		Details:   map[string]any{"file_name": file.Name},
	})

	w.Header().Set("Content-Type", "application/octet-stream")
	httputil.SetAttachmentFilename(w, file.Name)
	w.Write(data)
}
