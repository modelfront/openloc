package publicapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/job"
	"openloc/internal/logging"
	"openloc/internal/project"
)

func (h Handlers) listProjects(w http.ResponseWriter, r *http.Request) {
	page := httputil.ParseCursor(r, listMaxLimit)
	params, err := project.ListFilterParams(r, auth.TenantIDFromContext(r.Context()), page)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	rows, err := h.Queries.ListProjectsFiltered(r.Context(), params)
	if err != nil {
		httputil.ServerError(w, "list", "projects")
		return
	}
	writePage(w, rows, page.Limit, func(p db.ListProjectsFilteredRow) int64 { return p.ID }, projectListDTO)
}

// customerFieldsMessage answers a create or update that sets the app's
// customer, client or domain fields: in v1 those are labels.
const customerFieldsMessage = "customer and domain are labels: set them with PUT /projects/{projectID}/labels"

// projectCreate is the v1 create body. There is no status: v1 creates live
// projects only; drafts are the app's New project form. Customer, Client and
// Domain are decoded only to be refused (customerFieldsMessage).
type projectCreate struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	SourceLanguage  string          `json:"source_language"`
	TargetLanguages []string        `json:"target_languages"`
	Deadline        string          `json:"deadline"`
	TemplateID      *int64          `json:"template_id"`
	WorkflowID      *int64          `json:"workflow_id"`
	CustomerID      json.RawMessage `json:"customer_id"`
	Client          json.RawMessage `json:"client"`
	Domain          json.RawMessage `json:"domain"`
}

func (h Handlers) createProject(w http.ResponseWriter, r *http.Request) {
	var req projectCreate
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.CustomerID != nil || req.Client != nil || req.Domain != nil {
		httputil.BadRequest(w, customerFieldsMessage)
		return
	}
	params := project.CreateParams{
		Name:            req.Name,
		Description:     req.Description,
		SourceLanguage:  req.SourceLanguage,
		TargetLanguages: req.TargetLanguages,
		Deadline:        req.Deadline,
	}
	var templateID *int64
	if req.TemplateID != nil && *req.TemplateID != 0 {
		templateID = req.TemplateID
	}
	ctx := r.Context()
	p, err := project.CreateWithSetup(ctx, h.Pool, h.Queries, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx),
		params, templateID, project.SetupConfig{WorkflowID: req.WorkflowID})
	if err != nil {
		project.WriteCreateError(w, r, err)
		return
	}
	httputil.Created(w, projectDTO(p))
}

func (h Handlers) getProject(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	p, err := h.Queries.GetProject(r.Context(), db.GetProjectParams{ID: id, TenantID: auth.TenantIDFromContext(r.Context())})
	if httputil.DBError(w, err, "project") {
		return
	}
	httputil.Data(w, projectDTO(p))
}

func (h Handlers) updateProject(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	patch, err := project.DecodeMetadataPatch(r)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	if patch.CustomerID != nil || patch.Client != nil || patch.Domain != nil {
		httputil.BadRequest(w, customerFieldsMessage)
		return
	}
	p, err := project.UpdateMetadata(r.Context(), h.Queries, auth.TenantIDFromContext(r.Context()), id, patch)
	if err != nil {
		project.WriteServiceError(w, err, "update", "project")
		return
	}
	httputil.Data(w, projectDTO(p))
}

func (h Handlers) listProjectJobs(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	page := httputil.ParseCursor(r, listMaxLimit)
	views, err := h.Jobs.ListPageByProject(r.Context(), auth.TenantIDFromContext(r.Context()), id, page.Cursor, page.Limit+1) // +1 to detect has_more
	if errors.Is(err, pgx.ErrNoRows) {
		httputil.NotFound(w, "project")
		return
	}
	if err != nil {
		logging.Error(r.Context(), err, "v1 job list failed", "v1-job-list-failed", logging.ProjectID(id))
		httputil.ServerError(w, "list", "jobs")
		return
	}
	writePage(w, views, page.Limit, func(v job.JobView) int64 { return v.ID }, jobDTO)
}

// projectExists writes a 404 for a project the caller's tenant does not have.
func (h Handlers) projectExists(w http.ResponseWriter, r *http.Request, id int64) bool {
	_, err := h.Queries.GetProject(r.Context(), db.GetProjectParams{ID: id, TenantID: auth.TenantIDFromContext(r.Context())})
	return !httputil.DBError(w, err, "project")
}
