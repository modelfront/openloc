package job

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// taskResponse is one row of the "My Tasks" page. Deadline is a plain RFC3339
// string (or null) rather than leaking pgtype's {Time,Valid} shape to the FE.
type taskResponse struct {
	JobID          int64  `json:"job_id"`
	ProjectID      int64  `json:"project_id"`
	ProjectName    string `json:"project_name"`
	FileID         int64  `json:"file_id"`
	FileName       string `json:"file_name"`
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
	Status         string `json:"status"`
	// CurrentStep is the raw jobs.current_step label. The FE uses it with Status
	// (raw jobs.status) to detect a running QE+APE step (editor lock).
	CurrentStep     string  `json:"current_step"`
	StepName        string  `json:"step_name"`
	CustomerName    *string `json:"customer_name"`
	WordCount       int32   `json:"word_count"`
	TranslatedCount *int32  `json:"translated_count"`
	TotalCount      *int32  `json:"total_count"`
	Deadline        *string `json:"deadline"`
	// Only populated in the owner/admin "everybody's tasks" view. Name, or email
	// when the name is blank.
	AssigneeName *string `json:"assignee_name,omitempty"`
}

// Tasks list page size: 50 by default, at most taskListMaxLimit.
const taskListMaxLimit int32 = 200

// validTaskDeadlineFilters are the ?deadline= buckets ListTasksFiltered knows.
var validTaskDeadlineFilters = map[string]bool{"overdue": true, "24h": true, "week": true, "none": true}

// taskScope is the assignee filter for the caller: nil (every assignee) for
// owner/admin, the caller's own id for everyone else.
func taskScope(r *http.Request) *int64 {
	if auth.IsAdmin(r) {
		return nil
	}
	userID := auth.UserIDFromContext(r.Context())
	return &userID
}

// HandleListMyTasks returns one page of the Tasks page. Owner/admin see
// EVERYONE's tasks in the tenant; project managers + linguists see only their
// own. Always tenant-scoped. Optional filters: q (file or project name), step
// (step name), deadline (overdue|24h|week|none). Paged by an opaque cursor
// string; the response is {data, cursor, has_more}.
func (h *Handler) HandleListMyTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	admin := auth.IsAdmin(r)
	params := db.ListTasksFilteredParams{
		TenantID:   auth.TenantIDFromContext(r.Context()),
		AssignedTo: taskScope(r),
	}

	page := httputil.ParseCursor(r, taskListMaxLimit) // only Limit is used; the cursor is a string
	params.Lim = page.Limit + 1                       // +1 to detect has_more
	if c := strings.TrimSpace(q.Get("cursor")); c != "" {
		cur, err := decodeTaskCursor(c)
		if err != nil {
			httputil.BadRequest(w, "invalid cursor")
			return
		}
		params.After = true
		params.CursorDeadline = cur.Deadline
		params.CursorID = cur.JobID
	}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		params.Q = &s
	}
	if s := strings.TrimSpace(q.Get("step")); s != "" {
		params.StepName = &s
	}
	if s := strings.TrimSpace(q.Get("deadline")); s != "" {
		if !validTaskDeadlineFilters[s] {
			httputil.BadRequest(w, "invalid deadline filter")
			return
		}
		params.Deadline = &s
	}

	rows, err := h.queries.ListTasksFiltered(r.Context(), params)
	if err != nil {
		logging.Error(r.Context(), err, "list tasks failed", "task-list-failed")
		httputil.ServerError(w, "list", "tasks")
		return
	}
	hasMore := len(rows) > int(page.Limit)
	if hasMore {
		rows = rows[:page.Limit]
	}

	tasks := make([]taskResponse, 0, len(rows))
	for _, row := range rows {
		t := taskResponse{
			JobID: row.ID, ProjectID: row.ProjectID, ProjectName: row.ProjectName,
			FileID: row.FileID, FileName: row.FileName, SourceLanguage: row.SourceLanguage,
			TargetLanguage: row.TargetLanguage, Status: row.Status, CurrentStep: row.CurrentStep,
			StepName:     row.StepName,
			CustomerName: row.CustomerName, WordCount: row.WordCount,
			TranslatedCount: row.TranslatedCount, TotalCount: row.TotalCount,
			Deadline: fmtTaskDeadline(row.Deadline),
		}
		if admin {
			name := row.AssigneeName
			t.AssigneeName = &name
		}
		tasks = append(tasks, t)
	}

	next := ""
	if hasMore {
		last := rows[len(rows)-1]
		next = encodeTaskCursor(last.Deadline, last.ID)
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"data": tasks, "cursor": next, "has_more": hasMore})
}

// HandleListTaskSteps returns the distinct step names across the caller's whole
// Tasks scope — the options for the page's Step filter.
func (h *Handler) HandleListTaskSteps(w http.ResponseWriter, r *http.Request) {
	names, err := h.queries.ListTaskStepNames(r.Context(), db.ListTaskStepNamesParams{
		TenantID:   auth.TenantIDFromContext(r.Context()),
		AssignedTo: taskScope(r),
	})
	if err != nil {
		logging.Error(r.Context(), err, "list task steps failed", "task-steps-list-failed")
		httputil.ServerError(w, "list", "task steps")
		return
	}
	if names == nil {
		names = []string{}
	}
	httputil.Data(w, names)
}

func fmtTaskDeadline(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	s := ts.Time.Format(time.RFC3339)
	return &s
}

// HandleAssignJob assigns a user to the job's CURRENT active workflow step so the
// job surfaces on that user's Tasks page. Backs the "Assign" action on the
// project jobs list. Admin/owner-only (gated at the route) + the job group's
// RequireJobOwnership already scopes the job to the tenant; we re-verify here
// (defense-in-depth) and additionally verify the assignee belongs to the same
// tenant so a job can't be assigned to a user from another org.
func (h *Handler) HandleAssignJob(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := httputil.Decode(r, &req); err != nil || req.UserID == 0 {
		httputil.BadRequest(w, "user_id is required")
		return
	}
	err = h.AssignJob(r.Context(), auth.TenantIDFromContext(r.Context()), jobID, req.UserID)
	if httputil.ServiceError(w, err, "assign", "job") {
		return
	}
	httputil.Data(w, map[string]any{"assigned": true, "user_id": req.UserID})
}
