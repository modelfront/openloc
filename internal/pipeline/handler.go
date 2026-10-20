package pipeline

import (
	"errors"
	"fmt"
	"net/http"
	"slices"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/logging"
)

type Handler struct {
	queries db.Querier
	service *Service
}

func NewHandler(q db.Querier, svc *Service) *Handler {
	return &Handler{queries: q, service: svc}
}

var errBadID = errors.New("invalid ID")

// resolveFile fetches the file and its project with tenant isolation.
func (h *Handler) resolveFile(r *http.Request) (db.File, db.Project, error) {
	fileID, err := httputil.ParseID(r, "fileID")
	if err != nil {
		return db.File{}, db.Project{}, errBadID
	}

	tenantID := auth.TenantIDFromContext(r.Context())

	file, err := h.queries.GetFileTenant(r.Context(), db.GetFileTenantParams{
		ID: fileID, TenantID: tenantID,
	})
	if err != nil {
		return db.File{}, db.Project{}, err
	}

	project, err := h.queries.GetProject(r.Context(), db.GetProjectParams{
		ID: file.ProjectID, TenantID: tenantID,
	})
	if err != nil {
		return db.File{}, db.Project{}, err
	}

	return file, project, nil
}

// HandleExportFile exports the translated file for a given target language.
//
// Default behavior (no `format` param): mirrors the source format —
// reconstructs DOCX/XLSX/JSON/etc. with translations injected, intended
// for the customer to use as the deliverable.
//
// `?format=xliff`: emits a bilingual XLIFF 1.2 document carrying both
// <source> and <target> for every segment, regardless of source format.
// Used for handing off to translators in external CAT tools. The trans-unit
// `id` equals `segments.segment_key` so the round-trip via
// POST /api/files/{id}/reimport-xliff matches.
func (h *Handler) HandleExportFile(w http.ResponseWriter, r *http.Request) {
	file, project, err := h.resolveFile(r)
	if err != nil {
		if errors.Is(err, errBadID) {
			httputil.BadRequest(w, "invalid file ID")
			return
		}
		httputil.NotFound(w, "file")
		return
	}

	lang := langcode.Normalize(r.URL.Query().Get("lang"))
	if lang == "" {
		httputil.BadRequest(w, "lang query parameter is required")
		return
	}
	if !slices.Contains(project.TargetLanguages, lang) {
		httputil.BadRequest(w, "lang must be one of the project's target languages")
		return
	}
	if !auth.LinguistFileLanguageAccess(w, r, h.queries, file.ID, lang) {
		return
	}

	format := r.URL.Query().Get("format")
	switch format {
	case "", "source":
		// Default: source-format mirror.
		h.exportSourceFormat(w, r, file, lang)
	case "xliff":
		h.exportBilingualXLIFF(w, r, file, project.SourceLanguage, lang)
	default:
		httputil.BadRequest(w, fmt.Sprintf("unsupported format %q (expected: source, xliff)", format))
	}
}

func (h *Handler) exportSourceFormat(w http.ResponseWriter, r *http.Request, file db.File, lang string) {
	ctx := r.Context()
	data, filename, err := h.service.ExportFile(ctx, auth.TenantIDFromContext(ctx), file.ID, lang)
	if err != nil {
		if msg, ok := UserExportMessage(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		logging.Error(ctx, err, "file export failed", "file-export-failed", logging.FileID(file.ID), logging.TargetLang(lang))
		httputil.ServerError(w, "export", "file")
		return
	}
	h.service.RecordFileExported(ctx, file, lang, "source")
	w.Header().Set("Content-Type", "application/octet-stream")
	httputil.SetAttachmentFilename(w, SourceExportFilename(filename, lang))
	w.Write(data)
}

func (h *Handler) exportBilingualXLIFF(w http.ResponseWriter, r *http.Request, file db.File, sourceLang, targetLang string) {
	ctx := r.Context()
	data, filename, err := h.service.ExportBilingualXLIFF(ctx, auth.TenantIDFromContext(ctx), file.ID, sourceLang, targetLang)
	if err != nil {
		logging.Error(ctx, err, "bilingual XLIFF export failed", "file-xliff-export-failed", logging.FileID(file.ID),
			logging.SourceLang(sourceLang), logging.TargetLang(targetLang))
		httputil.ServerError(w, "export", "bilingual XLIFF")
		return
	}
	h.service.RecordFileExported(ctx, file, targetLang, "xliff")
	// application/x-xliff+xml is the conventional MIME type for XLIFF.
	// Most browsers will save it; desktop CAT tools register a handler for it.
	w.Header().Set("Content-Type", "application/x-xliff+xml")
	httputil.SetAttachmentFilename(w, filename)
	w.Write(data)
}
