package workflowlib

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/systemworkflow"
)

type Handler struct {
	queries db.Querier
	pool    *pgxpool.Pool
}

func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{queries: q, pool: pool}
}

// stepSummary is what the list page shows per workflow step: identity, the
// position's default assignee, and the definition's AI router.
type stepSummary struct {
	StepDefinitionID int64   `json:"step_definition_id"`
	Name             string  `json:"name"`
	StepType         string  `json:"step_type"`
	AiRouterID       *int64  `json:"ai_router_id"`
	AiRouterName     *string `json:"ai_router_name"`
	DefaultAssignee  *int64  `json:"default_assignee"`
	AssigneeName     *string `json:"assignee_name"`
}

type listItem struct {
	db.Workflow
	Steps []stepSummary `json:"steps"`
}

// HandleList returns the tenant's live workflows, each with its step chips.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	wfs, err := h.queries.ListWorkflows(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "workflows")
		return
	}
	steps, err := h.queries.ListWorkflowDefinitionStepsForTenant(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "workflow steps")
		return
	}
	byWorkflow := make(map[int64][]stepSummary, len(wfs))
	for _, s := range steps {
		byWorkflow[s.WorkflowID] = append(byWorkflow[s.WorkflowID], stepSummary{
			StepDefinitionID: s.StepDefinitionID, Name: s.Name, StepType: s.StepType,
			AiRouterID: s.AiRouterID, AiRouterName: s.AiRouterName,
			DefaultAssignee: s.DefaultAssignee, AssigneeName: s.AssigneeName,
		})
	}
	out := make([]listItem, 0, len(wfs))
	for _, wf := range wfs {
		items := byWorkflow[wf.ID]
		if items == nil {
			items = []stepSummary{}
		}
		out = append(out, listItem{Workflow: wf, Steps: items})
	}
	httputil.Data(w, out)
}

// HandleGet returns one workflow with its ordered steps.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "workflowID")
	if err != nil {
		httputil.BadRequest(w, "invalid workflow ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	h.respondDetail(w, r, id, tenantID)
}

// HandleGetPretranslate returns the tenant's built-in Pre-translation
// workflow with its step, in the same shape as HandleGet. The library hides
// it, so this is how the Workflow card's Raw MT mode finds it.
func (h *Handler) HandleGetPretranslate(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	wf, err := systemworkflow.Pretranslate(r.Context(), h.queries, tenantID)
	if httputil.DBError(w, err, "workflow") {
		return
	}
	h.writeDetail(w, r, wf, tenantID)
}

func (h *Handler) respondDetail(w http.ResponseWriter, r *http.Request, id, tenantID int64) {
	wf, err := h.queries.GetWorkflow(r.Context(), db.GetWorkflowParams{ID: id, TenantID: tenantID})
	if httputil.DBError(w, err, "workflow") {
		return
	}
	h.writeDetail(w, r, wf, tenantID)
}

func (h *Handler) writeDetail(w http.ResponseWriter, r *http.Request, wf db.Workflow, tenantID int64) {
	d, err := LoadDetail(r.Context(), h.queries, tenantID, wf)
	if httputil.ServiceError(w, err, "list", "workflow steps") {
		return
	}
	httputil.Data(w, d)
}

// HandleCreate inserts a workflow + steps in one transaction. 409 on a
// case-insensitive duplicate name.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	d, err := CreateValidated(r.Context(), h.queries, h.pool, tenantID, in)
	if httputil.ServiceError(w, err, "create", "workflow") {
		return
	}
	httputil.Created(w, d)
}

// HandleUpdate renames and/or replaces the step list (see Update).
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "workflowID")
	if err != nil {
		httputil.BadRequest(w, "invalid workflow ID")
		return
	}
	var in Input
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	d, err := Update(r.Context(), h.queries, h.pool, tenantID, id, in)
	if httputil.ServiceError(w, err, "update", "workflow") {
		return
	}
	httputil.Data(w, d)
}

// HandleDelete soft-deletes a workflow (see Delete).
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "workflowID")
	if err != nil {
		httputil.BadRequest(w, "invalid workflow ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if err := Delete(r.Context(), h.queries, tenantID, id); err != nil {
		httputil.ServiceError(w, err, "delete", "workflow")
		return
	}
	httputil.NoContent(w)
}
