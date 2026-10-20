package publicapi

import (
	"net/http"
	"strings"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/stepdef"
	tmpl "openloc/internal/template"
	"openloc/internal/workflowlib"
)

// Template is a v1 project template: the setup a new project copies.
type Template struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	ProjectName     *string  `json:"project_name"`
	SourceLanguage  *string  `json:"source_language"`
	TargetLanguages []string `json:"target_languages"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

func templateDTO(t db.Template) Template {
	targets := t.TargetLanguages
	if targets == nil {
		targets = []string{}
	}
	return Template{ID: t.ID, Name: t.Name, ProjectName: t.ProjectName, SourceLanguage: t.SourceLanguage,
		TargetLanguages: targets, CreatedAt: timestamp(t.CreatedAt), UpdatedAt: timestamp(t.UpdatedAt)}
}

// Workflow is a v1 library workflow with its ordered steps.
type Workflow struct {
	ID        int64         `json:"id"`
	Name      string        `json:"name"`
	Steps     []LibraryStep `json:"steps"`
	CreatedAt string        `json:"created_at"`
	UpdatedAt string        `json:"updated_at"`
}

// LibraryStep is one step of a library workflow.
type LibraryStep struct {
	StepDefinitionID int64  `json:"step_definition_id"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	DefaultAssignee  *int64 `json:"default_assignee_id"`
}

// StepDefinition is a v1 reusable workflow step.
type StepDefinition struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	AIRouterID *int64 `json:"ai_router_id"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func stepDefinitionDTO(s db.StepDefinition) StepDefinition {
	return StepDefinition{ID: s.ID, Name: s.Name, Type: s.StepType, AIRouterID: s.AiRouterID,
		CreatedAt: timestamp(s.CreatedAt), UpdatedAt: timestamp(s.UpdatedAt)}
}

// --- Templates ---

func (h Handlers) listTemplates(w http.ResponseWriter, r *http.Request) {
	page := httputil.ParseCursor(r, listMaxLimit)
	params := db.ListTemplatesFilteredParams{TenantID: auth.TenantIDFromContext(r.Context()), Cursor: page.Cursor, Lim: page.Limit + 1}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		params.Q = &q
	}
	rows, err := h.Queries.ListTemplatesFiltered(r.Context(), params)
	if err != nil {
		httputil.ServerError(w, "list", "templates")
		return
	}
	writePage(w, rows, page.Limit, func(t db.Template) int64 { return t.ID }, templateDTO)
}

func (h Handlers) getTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "templateID")
	if err != nil {
		httputil.BadRequest(w, "invalid template ID")
		return
	}
	t, err := h.Queries.GetTemplate(r.Context(), db.GetTemplateParams{ID: id, TenantID: auth.TenantIDFromContext(r.Context())})
	if httputil.DBError(w, err, "template") {
		return
	}
	httputil.Data(w, templateDTO(t))
}

func (h Handlers) duplicateTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "templateID")
	if err != nil {
		httputil.BadRequest(w, "invalid template ID")
		return
	}
	t, _, err := tmpl.Duplicate(r.Context(), h.Queries, h.Pool, auth.TenantIDFromContext(r.Context()), id)
	if httputil.ServiceError(w, err, "duplicate", "template") {
		return
	}
	httputil.Created(w, templateDTO(t))
}

// --- Workflows ---

func (h Handlers) writeWorkflow(w http.ResponseWriter, r *http.Request, wf db.Workflow) {
	d, err := workflowlib.LoadDetail(r.Context(), h.Queries, wf.TenantID, wf)
	if httputil.ServiceError(w, err, "list", "workflow steps") {
		return
	}
	httputil.Data(w, workflowDTO(d.Workflow, d.Steps))
}

func workflowDTO(wf db.Workflow, steps []db.ListWorkflowDefinitionStepsRow) Workflow {
	out := Workflow{ID: wf.ID, Name: wf.Name, Steps: make([]LibraryStep, len(steps)),
		CreatedAt: timestamp(wf.CreatedAt), UpdatedAt: timestamp(wf.UpdatedAt)}
	for i, s := range steps {
		out.Steps[i] = LibraryStep{StepDefinitionID: s.StepDefinitionID, Name: s.Name, Type: s.StepType, DefaultAssignee: s.DefaultAssignee}
	}
	return out
}

func (h Handlers) listWorkflows(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	wfs, err := h.Queries.ListWorkflows(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "workflows")
		return
	}
	steps, err := h.Queries.ListWorkflowDefinitionStepsForTenant(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "workflow steps")
		return
	}
	byWorkflow := map[int64][]db.ListWorkflowDefinitionStepsRow{}
	for _, s := range steps {
		byWorkflow[s.WorkflowID] = append(byWorkflow[s.WorkflowID], db.ListWorkflowDefinitionStepsRow{
			StepDefinitionID: s.StepDefinitionID, Name: s.Name, StepType: s.StepType, DefaultAssignee: s.DefaultAssignee,
		})
	}
	writeAll(w, wfs, func(wf db.Workflow) Workflow { return workflowDTO(wf, byWorkflow[wf.ID]) })
}

// workflowInput is the create and replace body: a name and the steps in order.
type workflowInput struct {
	Name  string `json:"name"`
	Steps []struct {
		StepDefinitionID int64  `json:"step_definition_id"`
		DefaultAssignee  *int64 `json:"default_assignee_id"`
	} `json:"steps"`
}

func (in workflowInput) lib() workflowlib.Input {
	out := workflowlib.Input{Name: in.Name}
	for _, s := range in.Steps {
		out.Steps = append(out.Steps, workflowlib.StepInput{StepDefinitionID: s.StepDefinitionID, DefaultAssignee: s.DefaultAssignee})
	}
	return out
}

func (h Handlers) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var req workflowInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	d, err := workflowlib.CreateValidated(r.Context(), h.Queries, h.Pool, auth.TenantIDFromContext(r.Context()), req.lib())
	if httputil.ServiceError(w, err, "create", "workflow") {
		return
	}
	httputil.Created(w, workflowDTO(d.Workflow, d.Steps))
}

func (h Handlers) getWorkflow(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "workflowID")
	if err != nil {
		httputil.BadRequest(w, "invalid workflow ID")
		return
	}
	wf, err := h.Queries.GetWorkflow(r.Context(), db.GetWorkflowParams{ID: id, TenantID: auth.TenantIDFromContext(r.Context())})
	if httputil.DBError(w, err, "workflow") {
		return
	}
	h.writeWorkflow(w, r, wf)
}

// replaceWorkflow renames the workflow and replaces its steps. Projects that
// copied it keep their own steps.
func (h Handlers) replaceWorkflow(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "workflowID")
	if err != nil {
		httputil.BadRequest(w, "invalid workflow ID")
		return
	}
	var req workflowInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	d, err := workflowlib.Update(r.Context(), h.Queries, h.Pool, auth.TenantIDFromContext(r.Context()), id, req.lib())
	if httputil.ServiceError(w, err, "update", "workflow") {
		return
	}
	httputil.Data(w, workflowDTO(d.Workflow, d.Steps))
}

func (h Handlers) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "workflowID")
	if err != nil {
		httputil.BadRequest(w, "invalid workflow ID")
		return
	}
	if err := workflowlib.Delete(r.Context(), h.Queries, auth.TenantIDFromContext(r.Context()), id); httputil.ServiceError(w, err, "delete", "workflow") {
		return
	}
	httputil.NoContent(w)
}

// --- Step definitions ---

func (h Handlers) listStepDefinitions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Queries.ListStepDefinitions(r.Context(), auth.TenantIDFromContext(r.Context()))
	if err != nil {
		httputil.ServerError(w, "list", "workflow steps")
		return
	}
	writeAll(w, rows, func(s db.ListStepDefinitionsRow) StepDefinition {
		return stepDefinitionDTO(db.StepDefinition{ID: s.ID, Name: s.Name, StepType: s.StepType, AiRouterID: s.AiRouterID, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt})
	})
}

// stepInput is the create and replace body of a step definition.
type stepInput struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	AIRouterID *int64 `json:"ai_router_id"`
}

func (in stepInput) lib() stepdef.Input {
	return stepdef.Input{Name: in.Name, StepType: in.Type, AIRouterID: in.AIRouterID}
}

func (h Handlers) createStepDefinition(w http.ResponseWriter, r *http.Request) {
	var req stepInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	sd, err := stepdef.Create(r.Context(), h.Queries, auth.TenantIDFromContext(r.Context()), req.lib())
	if httputil.ServiceError(w, err, "create", "workflow step") {
		return
	}
	httputil.Created(w, stepDefinitionDTO(sd))
}

func (h Handlers) getStepDefinition(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "stepDefinitionID")
	if err != nil {
		httputil.BadRequest(w, "invalid workflow step ID")
		return
	}
	sd, err := h.Queries.GetStepDefinition(r.Context(), db.GetStepDefinitionParams{ID: id, TenantID: auth.TenantIDFromContext(r.Context())})
	if httputil.DBError(w, err, "workflow step") {
		return
	}
	httputil.Data(w, stepDefinitionDTO(sd))
}

func (h Handlers) replaceStepDefinition(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "stepDefinitionID")
	if err != nil {
		httputil.BadRequest(w, "invalid workflow step ID")
		return
	}
	var req stepInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	sd, err := stepdef.Update(r.Context(), h.Queries, auth.TenantIDFromContext(r.Context()), id, req.lib())
	if httputil.ServiceError(w, err, "update", "workflow step") {
		return
	}
	httputil.Data(w, stepDefinitionDTO(sd))
}

func (h Handlers) deleteStepDefinition(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "stepDefinitionID")
	if err != nil {
		httputil.BadRequest(w, "invalid workflow step ID")
		return
	}
	if err := stepdef.Delete(r.Context(), h.Pool, auth.TenantIDFromContext(r.Context()), id); httputil.ServiceError(w, err, "delete", "workflow step") {
		return
	}
	httputil.NoContent(w)
}
