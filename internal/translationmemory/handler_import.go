package translationmemory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/ptr"
	"openloc/internal/tmx"
)

// TMX Import + Export: the bulk insert, streaming export and TMX date
// helpers.

const maxTMXUploadSize = 4 << 30 // 4 GB — enterprise TMs can be very large

// HandleImportTMX handles POST /api/tms/{tmID}/import: 202 with the import
// record; entries are inserted in the background (poll the imports list).
func (h *Handler) HandleImportTMX(w http.ResponseWriter, r *http.Request) {
	imp, ok := h.Import(w, r)
	if !ok {
		return
	}
	httputil.JSON(w, http.StatusAccepted, map[string]any{"data": imp})
}

// Import reads a multipart TMX upload (field `file`) for the {tmID} route
// param and starts its import. The TM is looked up first, so a foreign id is
// a 404 whatever the body. It has written the error response when ok is false.
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) (db.TmImport, bool) {
	tmID, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return db.TmImport{}, false
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetTMCollection(r.Context(), db.GetTMCollectionParams{
		ID:       tmID,
		TenantID: tenantID,
	}); httputil.DBError(w, err, "TM") {
		return db.TmImport{}, false
	}

	// Read uploaded file — save to temp file to avoid holding large files in memory
	r.Body = http.MaxBytesReader(w, r.Body, maxTMXUploadSize)
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB memory buffer, rest goes to disk
		httputil.BadRequest(w, "file too large or invalid multipart form")
		return db.TmImport{}, false
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "missing file field")
		return db.TmImport{}, false
	}
	defer file.Close()

	imp, err := h.StartImport(r.Context(), tenantID, tmID, header.Filename, file)
	var verr ValidationError
	if errors.As(err, &verr) {
		httputil.BadRequest(w, verr.Msg)
		return db.TmImport{}, false
	}
	if err != nil {
		logging.Error(r.Context(), err, "TMX import could not start", "tm-import-start-failed", logging.TMID(tmID))
		httputil.ServerError(w, "create", "import record")
		return db.TmImport{}, false
	}
	return imp, true
}

// batchUpsertTM inserts multiple TM entries in a single multi-row INSERT.
// No deduplication — every TMX <tu> is stored as-is. Returns rows inserted.
func (s *Service) batchUpsertTM(ctx context.Context, tmID int64, entries []tmx.Entry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	// Columns: tm_id, source_language, target_language, source_text, target_text,
	//          created_by, updated_by, tu_id, created_at, updated_at,
	//          source_rich_text, target_rich_text
	// (source_hash + target_hash are computed inline via md5() — no extra args.)
	const paramsPerRow = 12
	var b strings.Builder
	b.WriteString("INSERT INTO translation_memory (tm_id, source_language, target_language, source_text, target_text, source_hash, target_hash, created_by, updated_by, tu_id, created_at, updated_at, source_rich_text, target_rich_text) VALUES ")

	now := time.Now()
	args := make([]any, 0, len(entries)*paramsPerRow)
	for i, e := range entries {
		if i > 0 {
			b.WriteString(", ")
		}
		base := i * paramsPerRow
		// Positional placeholders: scalars, then md5() of the text positions
		// already used (4=source_text, 5=target_text), then the rich-text
		// positions (11=source_rich, 12=target_rich) wrapped in NULLIF for
		// "" → NULL conversion (matches the InsertTM single-row query).
		fmt.Fprintf(&b, "($%d, $%d, $%d, $%d, $%d, md5($%d), md5($%d), $%d, $%d, $%d, $%d, $%d, NULLIF($%d, ''), NULLIF($%d, ''))",
			base+1, base+2, base+3, base+4, base+5, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11, base+12)
		creator := e.Creator
		if creator == "" {
			creator = e.ChangeUser
		}
		updater := e.ChangeUser
		if updater == "" {
			updater = creator
		}
		createdAt := parseTMXDate(e.CreationDate, now)
		updatedAt := parseTMXDate(e.ChangeDate, createdAt)
		args = append(args,
			tmID, e.SourceLang, e.TargetLang, e.SourceText, e.TargetText,
			creator, updater, e.TUId, createdAt, updatedAt,
			e.SourceRichText, e.TargetRichText,
		)
	}

	tag, err := s.db.Exec(ctx, b.String(), args...)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// parseTMXDate parses TMX ISO 8601 dates like "20260115T120000Z". Falls back to the provided default.
func parseTMXDate(s string, fallback time.Time) time.Time {
	if s == "" {
		return fallback
	}
	// TMX uses compact ISO 8601: YYYYMMDDTHHmmSSZ
	for _, layout := range []string{
		"20060102T150405Z",
		"20060102T150405",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return fallback
}

// HandleListImports handles GET /api/tms/{tmID}/imports.
func (h *Handler) HandleListImports(w http.ResponseWriter, r *http.Request) {
	tmID, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetTMCollection(r.Context(), db.GetTMCollectionParams{
		ID:       tmID,
		TenantID: tenantID,
	}); httputil.DBError(w, err, "TM") {
		return
	}

	imports, err := h.queries.ListTMImports(r.Context(), tmID)
	if err != nil {
		httputil.ServerError(w, "list", "imports")
		return
	}
	httputil.Data(w, imports)
}

// HandleExportTMX handles GET /api/tms/{tmID}/export.
// Streams a TMX file as an HTTP download using cursor-based DB iteration
// to avoid loading all entries into memory at once.
func (h *Handler) HandleExportTMX(w http.ResponseWriter, r *http.Request) {
	tmID, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	tm, err := h.queries.GetTMCollection(r.Context(), db.GetTMCollectionParams{
		ID:       tmID,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "TM") {
		return
	}

	// Check if TM has any entries before starting the stream
	count, err := h.queries.CountTMEntries(r.Context(), db.CountTMEntriesParams{
		TmID:           tmID,
		SourceLanguage: "",
		TargetLanguage: "",
	})
	if err != nil {
		httputil.ServerError(w, "count", "TM entries")
		return
	}
	if count == 0 {
		httputil.Error(w, http.StatusNotFound, "TM has no entries to export")
		return
	}

	filename := tm.Name + ".tmx"
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	httputil.SetAttachmentFilename(w, filename)

	// Determine source language from first entry
	firstBatch, err := h.queries.ListTMEntriesBatch(r.Context(), db.ListTMEntriesBatchParams{
		TmID:       tmID,
		Cursor:     0,
		BatchLimit: 1,
	})
	if err != nil || len(firstBatch) == 0 {
		logging.Error(r.Context(), err, "TMX export: first entry read failed", "tm-export-first-entry-failed", logging.TMID(tmID))
		return
	}
	srcLang := firstBatch[0].SourceLanguage

	// Stream TMX: header, then batches of entries, then footer
	if err := tmx.WriteHeader(w, srcLang); err != nil {
		logging.Error(r.Context(), err, "TMX export: header write failed", "tm-export-header-failed", logging.TMID(tmID))
		return
	}

	const exportBatchSize = 1000
	var cursor int64
	for {
		dbEntries, err := h.queries.ListTMEntriesBatch(r.Context(), db.ListTMEntriesBatchParams{
			TmID:       tmID,
			Cursor:     cursor,
			BatchLimit: exportBatchSize,
		})
		if err != nil {
			logging.Error(r.Context(), err, "TMX export: batch read failed", "tm-export-batch-failed", logging.TMID(tmID))
			return
		}
		if len(dbEntries) == 0 {
			break
		}

		entries := make([]tmx.Entry, len(dbEntries))
		for i, e := range dbEntries {
			entries[i] = tmx.Entry{
				SourceLang:     e.SourceLanguage,
				TargetLang:     e.TargetLanguage,
				SourceText:     e.SourceText,
				TargetText:     e.TargetText,
				SourceRichText: ptr.Deref(e.SourceRichText),
				TargetRichText: ptr.Deref(e.TargetRichText),
				TUId:           e.TuID,
				CreationDate:   formatTMXDate(e.CreatedAt),
				Creator:        e.CreatedBy,
				ChangeDate:     formatTMXDate(e.UpdatedAt),
				ChangeUser:     e.UpdatedBy,
			}
		}
		if err := tmx.WriteEntries(w, entries); err != nil {
			logging.Error(r.Context(), err, "TMX export: entry write failed", "tm-export-entry-failed", logging.TMID(tmID))
			return
		}

		cursor = dbEntries[len(dbEntries)-1].ID
		if len(dbEntries) < exportBatchSize {
			break
		}
	}

	if err := tmx.WriteFooter(w); err != nil {
		logging.Error(r.Context(), err, "TMX export: footer write failed", "tm-export-footer-failed", logging.TMID(tmID))
	}
}

// formatTMXDate converts a pgtype.Timestamptz to TMX compact ISO 8601 format: YYYYMMDDTHHmmSSZ
func formatTMXDate(t pgtype.Timestamptz) string {
	if !t.Valid || t.Time.IsZero() {
		return ""
	}
	return t.Time.UTC().Format("20060102T150405Z")
}
