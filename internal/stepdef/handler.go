package stepdef

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

type Handler struct {
	queries db.Querier
	pool    *pgxpool.Pool
}

func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{queries: q, pool: pool}
}

// HandleList returns the tenant's live definitions with the attached AI
// router's name and how many library workflows use each.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	rows, err := h.queries.ListStepDefinitions(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "step definitions")
		return
	}
	httputil.Data(w, rows)
}

// HandleGet returns one live definition.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "stepDefinitionID")
	if err != nil {
		httputil.BadRequest(w, "invalid step definition ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	sd, err := h.queries.GetStepDefinition(r.Context(), db.GetStepDefinitionParams{ID: id, TenantID: tenantID})
	if httputil.DBError(w, err, "step definition") {
		return
	}
	httputil.Data(w, sd)
}

// HandleCreate inserts a definition. 409 on a case-insensitive duplicate name.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	sd, err := Create(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), in)
	if httputil.ServiceError(w, err, "create", "step definition") {
		return
	}
	httputil.Created(w, sd)
}

// HandleUpdate replaces a definition's identity fields (see Update).
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "stepDefinitionID")
	if err != nil {
		httputil.BadRequest(w, "invalid step definition ID")
		return
	}
	var in Input
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	sd, err := Update(r.Context(), h.queries, tenantID, id, in)
	if err != nil {
		httputil.ServiceError(w, err, "update", "step definition")
		return
	}
	httputil.Data(w, sd)
}

// HandleDelete soft-deletes a definition; 409 while a live library workflow
// still composes it (see Delete).
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "stepDefinitionID")
	if err != nil {
		httputil.BadRequest(w, "invalid step definition ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if err := Delete(r.Context(), h.pool, tenantID, id); err != nil {
		httputil.ServiceError(w, err, "delete", "step definition")
		return
	}
	httputil.NoContent(w)
}
