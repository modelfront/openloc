package file

import (
	"io"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"openloc/internal/analysis"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/parser"
	"openloc/internal/pipeline"
	projectsvc "openloc/internal/project"
	"openloc/internal/projectevents"
	"openloc/internal/taskedit"
)

const segmentBatchSize = 500 // 500 rows * 5 cols = 2500 params (PG limit: 65535)

const MaxUploadSize = 50 << 20 // 50 MB

// TaskEnqueuer enqueues pipeline tasks for async background processing.
type TaskEnqueuer interface {
	EnqueuePipeline(fileID int64, sourceLang, targetLang string, jobID, tenantID int64, phases pipeline.TranslatePhases, triggeredBy *int64) error
}

// AIStepEnqueuer enqueues a QE+APE run for a job whose first
// workflow step carries a QE+APE AI engine (predict-on-start).
// Satisfied by *worker.Enqueuer; nil when async isn't wired.
type AIStepEnqueuer interface {
	EnqueueAIStep(jobID, tenantID int64, triggeredBy *int64) error
}

type Handler struct {
	queries          db.Querier
	pool             *pgxpool.Pool
	storage          BlobStore
	enqueuer         TaskEnqueuer      // nil if async processing is not available
	aiEnqueuer       AIStepEnqueuer    // nil if async processing is not available
	analysisEnqueuer analysis.Enqueuer // nil skips auto-analysis-on-upload
	guard            *taskedit.Guard   // checks XLIFF re-import against the job's current task
}

// NewHandler takes the store as an interface so a nil *Storage never becomes a
// non-nil BlobStore holding nil — callers pass an untyped nil for "no storage".
func NewHandler(q db.Querier, pool *pgxpool.Pool, s BlobStore, enqueuer TaskEnqueuer) *Handler {
	return &Handler{queries: q, pool: pool, storage: s, enqueuer: enqueuer, guard: taskedit.New(q, pool)}
}

// SetAIStepEnqueuer wires the QE+APE predict-on-start enqueuer for the upload
// path. Optional — nil skips the auto-predict on first-step AI.
func (h *Handler) SetAIStepEnqueuer(e AIStepEnqueuer) { h.aiEnqueuer = e }

// SetAnalysisEnqueuer wires auto-analysis-on-upload for the upload path.
// On projects with no pretranslate enabled, jobs are analysis-ready
// the moment they're created, so the analysis is triggered right after commit.
// Projects that run TM/MT on upload trigger from the pipeline worker instead
// (the helper self-gates). Optional — nil skips the feature.
func (h *Handler) SetAnalysisEnqueuer(e analysis.Enqueuer) { h.analysisEnqueuer = e }

// HandleUpload — POST /api/projects/{projectID}/files (multipart `file`,
// optional `analyze=true`).
func (h *Handler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	res, ok := h.Upload(w, r)
	if !ok {
		return
	}
	resp := map[string]any{"data": res.File}
	if len(res.Warnings) > 0 {
		resp["warnings"] = res.Warnings
	}
	httputil.JSON(w, http.StatusCreated, resp)
}

// Upload reads a multipart upload for the {projectID} route param and ingests
// it. The project is looked up first so a foreign id is a 404 whatever the
// body; the rest is Ingest. An optional `xlsx_layout` field carries the
// column layout of an Excel workbook. It has written the error response when
// ok is false.
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) (IngestResult, bool) {
	up, ok := h.readUpload(w, r)
	if !ok {
		return IngestResult{}, false
	}
	var layout *parser.XLSXLayout
	if raw := r.FormValue("xlsx_layout"); raw != "" {
		l, err := parser.ParseXLSXLayout([]byte(raw))
		if err != nil {
			httputil.BadRequest(w, layoutMessage(err))
			return IngestResult{}, false
		}
		layout = l
	}
	res, err := h.Ingest(r.Context(), up.project, up.filename, up.content, IngestOptions{
		Analyze:    r.FormValue("analyze") == "true",
		XLSXLayout: layout,
	})
	if err != nil {
		httputil.ServiceError(w, err, "upload", "file")
		return IngestResult{}, false
	}
	return res, true
}

func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}

	// Verify project belongs to current tenant
	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetProject(r.Context(), db.GetProjectParams{
		ID: projectID, TenantID: tenantID,
	}); err != nil {
		httputil.NotFound(w, "project")
		return
	}

	// Parse cursor pagination params (backwards-compatible: no params = all files).
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 32)
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	total, err := h.queries.CountFilesByProject(r.Context(), projectID)
	if err != nil {
		httputil.ServerError(w, "count", "files")
		return
	}

	files, err := h.queries.ListFilesPaginated(r.Context(), db.ListFilesPaginatedParams{
		ProjectID: projectID,
		Cursor:    cursor,
		PageLimit: int32(limit),
	})
	if err != nil {
		httputil.ServerError(w, "list", "files")
		return
	}

	var nextCursor int64
	if len(files) > 0 {
		nextCursor = files[len(files)-1].ID
	}

	httputil.Paginated(w, files, nextCursor, len(files) == int(limit), total)
}

func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	file, err := h.queries.GetFileTenant(r.Context(), db.GetFileTenantParams{
		ID:       id,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "file") {
		return
	}
	httputil.Data(w, file)
}

// HandleRename — PATCH /api/projects/{projectID}/files/{fileID} {name}.
func (h *Handler) HandleRename(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "name is required")
		return
	}
	file, err := Rename(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), id, req.Name)
	if err != nil {
		httputil.ServiceError(w, err, "rename", "file")
		return
	}
	httputil.Data(w, file)
}

func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	// Audit pre-fetch: capture the name before the row is
	// gone. Not-found keeps the historical no-op-delete semantics (204, no
	// event) instead of turning into a 404.
	oldFile, fetchErr := h.queries.GetFileTenant(r.Context(), db.GetFileTenantParams{
		ID: id, TenantID: tenantID,
	})
	// Deleting a file is owner/admin — except on a draft, where the manager
	// drafting it may take back a file they just dropped in.
	// The route group only checks manager; this is the
	// rest of the gate.
	if !auth.IsAdmin(r) {
		if fetchErr != nil {
			httputil.Error(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		project, err := h.queries.GetProject(r.Context(), db.GetProjectParams{ID: oldFile.ProjectID, TenantID: tenantID})
		if err != nil || project.Status != db.ProjectStatusDraft {
			httputil.Error(w, http.StatusForbidden, "insufficient permissions")
			return
		}
	}
	if err := h.queries.DeleteFileTenant(r.Context(), db.DeleteFileTenantParams{
		ID:       id,
		TenantID: tenantID,
	}); err != nil {
		httputil.ServerError(w, "delete", "file")
		return
	}
	if fetchErr == nil {
		actorID, actorName := projectevents.ActorFromContext(r.Context())
		projectevents.Emit(r.Context(), h.queries, projectevents.Event{
			ProjectID: oldFile.ProjectID,
			TenantID:  tenantID,
			Type:      projectevents.TypeFileDeleted,
			ActorID:   actorID,
			ActorName: actorName,
			FileID:    &oldFile.ID,
			Details: map[string]any{
				"file_name":     oldFile.Name,
				"segment_count": oldFile.SegmentCount,
			},
		})

		// jobs.file_id is ON DELETE CASCADE, so this removed every task on the
		// file at once — the remaining ones may now all be complete, or there
		// may be none left, which reopens the project.
		//
		// TODO(soft-delete): once files carry a tombstone
		// this moves to just after the deleted_at write,
		// the soft delete must propagate to the file's jobs, and the restore
		// path needs a call of its own.
		projectsvc.Reevaluate(r.Context(), h.queries, tenantID, oldFile.ProjectID)
	}
	httputil.NoContent(w)
}

// upload is a multipart request's project and file.
type upload struct {
	project  db.Project
	filename string
	content  []byte
}

// readUpload resolves the {projectID} route param tenant-scoped — a foreign id
// is a 404 whatever the body — and reads the multipart `file` field. It has
// written the error response when ok is false.
func (h *Handler) readUpload(w http.ResponseWriter, r *http.Request) (upload, bool) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return upload{}, false
	}
	project, err := h.queries.GetProject(r.Context(), db.GetProjectParams{
		ID: projectID, TenantID: auth.TenantIDFromContext(r.Context()),
	})
	if httputil.DBError(w, err, "project") {
		return upload{}, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadSize)
	if err := r.ParseMultipartForm(MaxUploadSize); err != nil {
		httputil.BadRequest(w, "file too large or invalid multipart form")
		return upload{}, false
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "missing file field")
		return upload{}, false
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil {
		httputil.ServerError(w, "read", "file")
		return upload{}, false
	}
	return upload{project: project, filename: header.Filename, content: content}, true
}
