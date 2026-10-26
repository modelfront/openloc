package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/parsers"
	"openloc/internal/parsers/common"
	"openloc/internal/parsers/xlsx"
)

// The column layout of an Excel workbook without language codes in row 1
// (xlsx.XLSXLayout): the upload's inspect step and later changes. These
// routes sit with the upload, in the project route group managers may use.

// layoutLockedMessage refuses a layout change once the file's segments sit
// under jobs.
const layoutLockedMessage = "The Excel columns can change only while the project is a draft. Upload the file again to read it differently."

// layoutMessage is what the uploader sees for a layout error.
func layoutMessage(err error) string {
	var le *common.XLSXLayoutError
	if errors.As(err, &le) {
		return le.Msg
	}
	return "invalid Excel column layout"
}

// HandleInspectXLSX — POST /api/projects/{projectID}/files/inspect (multipart
// `file`). Reads an .xlsx without storing it: whether it is bilingual, the
// columns of each visible sheet that hold text, and a suggested layout.
func (h *Handler) HandleInspectXLSX(w http.ResponseWriter, r *http.Request) {
	up, ok := h.readUpload(w, r)
	if !ok {
		return
	}
	if common.DetectFormat(up.filename, up.content) != "xlsx" {
		httputil.BadRequest(w, "only .xlsx files can be inspected")
		return
	}
	in, err := xlsx.InspectXLSX(up.content)
	if err != nil {
		logging.Warn(r.Context(), err, "xlsx inspect failed", "upload-xlsx-inspect-failed")
		httputil.BadRequest(w, "could not read the xlsx file — check that the file is well-formed")
		return
	}
	httputil.Data(w, in)
}

// HandleSetXLSXLayout — PUT /api/projects/{projectID}/files/{fileID}/xlsx-layout.
// Only while the project is a draft: the new layout replaces the stored one
// and the file is segmented again. Once published the segments sit under
// jobs, so a different layout means uploading the file again.
func (h *Handler) HandleSetXLSXLayout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	project, f, ok := h.xlsxFile(w, r)
	if !ok {
		return
	}
	if project.Status != db.ProjectStatusDraft {
		httputil.Error(w, http.StatusConflict, layoutLockedMessage)
		return
	}

	var raw json.RawMessage
	if err := httputil.Decode(r, &raw); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	layout, err := xlsx.ParseXLSXLayout(raw)
	if err != nil {
		httputil.BadRequest(w, layoutMessage(err))
		return
	}
	content, err := h.storedContent(ctx, f)
	if err != nil {
		logging.Error(ctx, err, "read stored xlsx failed", "file-xlsx-layout-read-failed", logging.FileID(f.ID))
		httputil.ServerError(w, "read", "file")
		return
	}
	stored, err := checkXLSXLayout(content, layout)
	if err != nil {
		httputil.ServiceError(w, err, "save", "Excel column layout")
		return
	}

	updated, err := h.relayoutDraftFile(r, f, content, layout, stored, tenantID)
	if err != nil {
		httputil.ServiceError(w, err, "save", "Excel column layout")
		return
	}
	httputil.Data(w, updated)
}

// HandleGetXLSXLayout — GET /api/projects/{projectID}/files/{fileID}/xlsx-layout.
// The stored layout (null for a workbook read cell by cell or by its language
// codes) and the stored workbook's columns to choose from.
func (h *Handler) HandleGetXLSXLayout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, f, ok := h.xlsxFile(w, r)
	if !ok {
		return
	}
	content, err := h.storedContent(ctx, f)
	if err != nil {
		logging.Error(ctx, err, "read stored xlsx failed", "file-xlsx-layout-read-failed", logging.FileID(f.ID))
		httputil.ServerError(w, "read", "file")
		return
	}
	in, err := xlsx.InspectXLSX(content)
	if err != nil {
		logging.Warn(ctx, err, "stored xlsx inspect failed", "file-xlsx-inspect-failed", logging.FileID(f.ID))
		httputil.ServerError(w, "read", "file")
		return
	}
	httputil.Data(w, xlsxLayoutView{Layout: f.XlsxLayout, Inspection: in})
}

type xlsxLayoutView struct {
	Layout     json.RawMessage     `json:"layout"`
	Inspection xlsx.XLSXInspection `json:"inspection"`
}

// xlsxFile resolves the route's project and its .xlsx file, tenant-scoped,
// and writes the error response when there is none.
func (h *Handler) xlsxFile(w http.ResponseWriter, r *http.Request) (db.Project, db.File, bool) {
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return db.Project{}, db.File{}, false
	}
	fileID, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return db.Project{}, db.File{}, false
	}
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if httputil.DBError(w, err, "project") {
		return db.Project{}, db.File{}, false
	}
	f, err := h.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if httputil.DBError(w, err, "file") {
		return db.Project{}, db.File{}, false
	}
	if f.ProjectID != project.ID {
		httputil.NotFound(w, "file")
		return db.Project{}, db.File{}, false
	}
	if f.Format != db.FileFormatXlsx {
		httputil.BadRequest(w, "an Excel column layout applies to .xlsx files only")
		return db.Project{}, db.File{}, false
	}
	return project, f, true
}

// storedContent reads a file's source back from storage.
func (h *Handler) storedContent(ctx context.Context, f db.File) ([]byte, error) {
	if h.storage == nil || f.StoragePath == "" {
		return nil, fmt.Errorf("source file is not in storage")
	}
	rc, err := h.storage.Download(ctx, f.StoragePath)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// relayoutDraftFile stores a draft file's new layout and replaces its
// segments with the parse it gives, in one transaction.
func (h *Handler) relayoutDraftFile(r *http.Request, f db.File, content []byte, layout *xlsx.XLSXLayout, stored []byte, tenantID int64) (db.File, error) {
	ctx := r.Context()
	p, err := parsers.GetParserWith(string(f.Format), parsers.ParseOptions{InlineHTML: f.InlineHtml, XLSXLayout: layout})
	if err != nil {
		return db.File{}, err
	}
	segments, err := p.Parse(content)
	if err != nil {
		if msg, ok := common.ImportMessage(err); ok {
			return db.File{}, httputil.NewClientError(msg)
		}
		logging.Warn(ctx, err, "xlsx relayout parse failed", "file-xlsx-layout-parse-failed", logging.FileID(f.ID))
		return db.File{}, httputil.NewClientError("could not read the xlsx file — check that the file is well-formed")
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return db.File{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	txq := db.New(tx)
	// The handler's draft check ran before the lock; a publish may have
	// created the file's jobs since.
	if _, err := txq.LockFileTenant(ctx, db.LockFileTenantParams{ID: f.ID, TenantID: tenantID}); err != nil {
		return db.File{}, fmt.Errorf("lock file: %w", err)
	}
	editable, err := txq.FileLayoutEditable(ctx, db.FileLayoutEditableParams{ID: f.ID, TenantID: tenantID})
	if err != nil {
		return db.File{}, fmt.Errorf("check file: %w", err)
	}
	if !editable {
		return db.File{}, httputil.NewConflictError(layoutLockedMessage)
	}
	updated, err := txq.UpdateFileXLSXLayout(ctx, db.UpdateFileXLSXLayoutParams{ID: f.ID, XlsxLayout: stored, TenantID: tenantID})
	if err != nil {
		return db.File{}, fmt.Errorf("store layout: %w", err)
	}
	if _, err := h.replaceDraftSegments(ctx, tx, f.ID, segments); err != nil {
		return db.File{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.File{}, fmt.Errorf("commit: %w", err)
	}
	updated.SegmentCount = int32(len(segments))
	return updated, nil
}
