package pipeline

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/projectevents"
)

// Batch download for the project Files tab.
//
// The single-item cases are deliberately NOT routed here: one job's deliverable
// goes through GET /api/jobs/{jobID}/export and one file's original through
// GET /api/files/{fileID}/download, both of which sit on linguist-reachable
// route groups. This endpoint lives under the manager-gated /api/projects group
// and exists to turn a multi-select into ONE download instead of N browser
// downloads (which Chrome/Safari prompt on or block). It still handles a
// one-item request correctly — a direct API caller shouldn't get a pointless
// single-entry ZIP — the UI just never needs that path.

// maxDownloadItems caps a batch. Every entry is built in memory before the
// response starts (see below), so this bounds peak allocation; it is also well
// past any realistic project selection.
const maxDownloadItems = 100

type downloadRequest struct {
	// Format: "source" (deliverable in the uploaded format), "xliff"
	// (bilingual hand-off) or "original" (the untouched upload).
	Format string `json:"format"`
	// JobIDs drives source/xliff (a deliverable is per file+target language).
	JobIDs []int64 `json:"job_ids"`
	// FileIDs drives original (the upload has no target language).
	FileIDs []int64 `json:"file_ids"`
}

// downloadEntry is one built artifact awaiting delivery.
type downloadEntry struct {
	name string
	data []byte
	// event is emitted only once the whole batch succeeded.
	event projectevents.Event
}

// HandleProjectDownload builds every requested artifact and returns either the
// single file (one entry) or a ZIP of all of them.
//
// The entire batch is built in memory BEFORE the first header is written. A
// failure halfway through a streamed ZIP would leave the client with a
// truncated archive under a 200 — for a customer deliverable that is worse than
// a clean error, so the memory cost (bounded by maxDownloadItems) is the right
// trade.
func (h *Handler) HandleProjectDownload(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}

	var req downloadRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	project, err := h.queries.GetProject(r.Context(), db.GetProjectParams{
		ID: projectID, TenantID: tenantID,
	})
	if httputil.DBError(w, err, "project") {
		return
	}

	entries, err := h.buildDownloadEntries(r, project, req)
	if err != nil {
		writeDownloadError(w, err)
		return
	}

	// Audit: the batch succeeded, so every artifact really
	// did leave the system. Emitting per item (rather than one batch event)
	// keeps the History tab rendering unchanged for job.exported.
	actorID, actorName := projectevents.ActorFromContext(r.Context())
	for _, e := range entries {
		ev := e.event
		ev.ActorID, ev.ActorName = actorID, actorName
		projectevents.Emit(r.Context(), h.queries, ev)
	}

	if len(entries) == 1 {
		w.Header().Set("Content-Type", downloadContentType(req.Format))
		httputil.SetAttachmentFilename(w, entries[0].name)
		w.Write(entries[0].data)
		return
	}

	archive, err := zipEntries(entries)
	if err != nil {
		logging.Error(r.Context(), err, "download archive build failed", "download-zip-failed", logging.ProjectID(project.ID))
		httputil.ServerError(w, "build", "download archive")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	httputil.SetAttachmentFilename(w, fmt.Sprintf("%s_%s.zip", project.Name, req.Format))
	w.Write(archive)
}

// downloadError carries the HTTP status a build failure should map to, so
// buildDownloadEntries can report "you asked for someone else's job" (404)
// separately from "the export blew up" (500).
type downloadError struct {
	status  int
	message string
}

func (e *downloadError) Error() string { return e.message }

func writeDownloadError(w http.ResponseWriter, err error) {
	var de *downloadError
	if errors.As(err, &de) {
		httputil.Error(w, de.status, de.message)
		return
	}
	httputil.ServerError(w, "build", "download")
}

func (h *Handler) buildDownloadEntries(r *http.Request, project db.Project, req downloadRequest) ([]downloadEntry, error) {
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)

	switch req.Format {
	case "source", "xliff":
		if len(req.JobIDs) == 0 {
			return nil, &downloadError{http.StatusBadRequest, "job_ids is required for format " + req.Format}
		}
		if len(req.JobIDs) > maxDownloadItems {
			return nil, &downloadError{http.StatusBadRequest, fmt.Sprintf("too many items: %d (max %d)", len(req.JobIDs), maxDownloadItems)}
		}
	case "original":
		if len(req.FileIDs) == 0 {
			return nil, &downloadError{http.StatusBadRequest, "file_ids is required for format original"}
		}
		if len(req.FileIDs) > maxDownloadItems {
			return nil, &downloadError{http.StatusBadRequest, fmt.Sprintf("too many items: %d (max %d)", len(req.FileIDs), maxDownloadItems)}
		}
	default:
		return nil, &downloadError{http.StatusBadRequest, fmt.Sprintf("unsupported format %q (expected: source, xliff, original)", req.Format)}
	}

	if req.Format == "original" {
		entries := make([]downloadEntry, 0, len(req.FileIDs))
		for _, fileID := range req.FileIDs {
			file, err := h.fileInProject(r, project, fileID)
			if err != nil {
				return nil, err
			}
			data, _, err := h.service.ExportOriginal(ctx, tenantID, file.ID)
			if err != nil {
				logging.Error(ctx, err, "original file download failed", "download-original-failed", logging.FileID(file.ID))
				return nil, &downloadError{http.StatusInternalServerError, "failed to download " + file.Name}
			}
			fileID := file.ID
			entries = append(entries, downloadEntry{
				name: file.Name,
				data: data,
				event: projectevents.Event{
					ProjectID: project.ID,
					TenantID:  tenantID,
					Type:      projectevents.TypeFileDownloaded,
					FileID:    &fileID,
					Details:   map[string]any{"file_name": file.Name},
				},
			})
		}
		return entries, nil
	}

	entries := make([]downloadEntry, 0, len(req.JobIDs))
	for _, jobID := range req.JobIDs {
		job, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
		if err != nil {
			return nil, &downloadError{http.StatusNotFound, "job " + strconv.FormatInt(jobID, 10) + " not found"}
		}
		// The job resolved inside the tenant, but the client also named a
		// project — never take its word for the pairing.
		file, err := h.fileInProject(r, project, job.FileID)
		if err != nil {
			return nil, err
		}

		var (
			data     []byte
			filename string
			expErr   error
		)
		if req.Format == "xliff" {
			data, filename, expErr = h.service.ExportBilingualXLIFF(ctx, tenantID, file.ID, project.SourceLanguage, job.TargetLanguage)
		} else {
			var base string
			data, base, expErr = h.service.ExportFile(ctx, tenantID, file.ID, job.TargetLanguage)
			filename = job.TargetLanguage + "_" + base
		}
		if expErr != nil {
			// One file the user must fix stops the whole download; the message
			// names the file so they know which one.
			if msg, ok := UserExportMessage(expErr); ok {
				return nil, &downloadError{http.StatusBadRequest, file.Name + ": " + msg}
			}
			logging.Error(ctx, expErr, "download export failed", "download-export-failed", logging.JobID(job.ID), logging.Label("format", req.Format))
			return nil, &downloadError{http.StatusInternalServerError, "failed to export " + file.Name}
		}

		jobID := job.ID
		entries = append(entries, downloadEntry{
			name:  filename,
			data:  data,
			event: jobExportedEvent(tenantID, file, &jobID, job.TargetLanguage, req.Format),
		})
	}
	return entries, nil
}

// fileInProject resolves a file inside the tenant AND asserts it belongs to the
// project named in the URL. A file id from another project is a 404, not a
// silent skip — the caller asked for something it can't have.
func (h *Handler) fileInProject(r *http.Request, project db.Project, fileID int64) (db.File, error) {
	tenantID := auth.TenantIDFromContext(r.Context())
	file, err := h.queries.GetFileTenant(r.Context(), db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil || file.ProjectID != project.ID {
		return db.File{}, &downloadError{http.StatusNotFound, "file " + strconv.FormatInt(fileID, 10) + " not found in this project"}
	}
	return file, nil
}

func downloadContentType(format string) string {
	if format == "xliff" {
		return "application/x-xliff+xml"
	}
	return "application/octet-stream"
}

// zipEntries packs the built artifacts. Entry names are sanitized (they end up
// as paths inside the archive) and de-duplicated — two files can share a name
// within one project, and a ZIP with two identical entry names extracts to one
// file, silently losing a deliverable.
func zipEntries(entries []downloadEntry) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	used := make(map[string]int, len(entries))

	for _, e := range entries {
		name := uniqueEntryName(httputil.SanitizeFilename(e.name), used)
		f, err := zw.Create(name)
		if err != nil {
			return nil, fmt.Errorf("create zip entry %q: %w", name, err)
		}
		if _, err := f.Write(e.data); err != nil {
			return nil, fmt.Errorf("write zip entry %q: %w", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}
	return buf.Bytes(), nil
}

// uniqueEntryName turns a second "report.docx" into "report (2).docx".
func uniqueEntryName(name string, used map[string]int) string {
	n, seen := used[name]
	if !seen {
		used[name] = 1
		return name
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for {
		n++
		candidate := fmt.Sprintf("%s (%d)%s", base, n, ext)
		if _, taken := used[candidate]; !taken {
			used[name] = n
			used[candidate] = 1
			return candidate
		}
	}
}
