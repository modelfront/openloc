// Package label is the HTTP handler for user-defined project labels.
//
// A label is a tenant-named classifier ("Country") with a fixed set of allowed
// values ("Germany", "Japan"); a project picks at most one value per label.
// Definitions and values are tenant-scoped and soft-deletable, so every query
// filters by tenant_id and `deleted_at IS NULL`.
//
// A label name is fixed once created: projects already carry the classification,
// so there is no rename endpoint — retire the label and add another.
//
// Customer and Domain are built-in labels (`system_key`): their values are
// managed like any other label's, but the label itself cannot be deleted.
//
// Role gating: reading and adding is manager (owner/admin/project_manager);
// removing anything — a value or a whole label — is admin-or-above. Wired in
// api.go.
package label

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// maxNameLen / maxValueLen cap the two free-text fields. Postgres TEXT has no
// limit of its own, and an unbounded string spills out of every picker that
// renders it.
const (
	maxNameLen  = 200
	maxValueLen = 200
	// maxValuesPerCreate bounds the list a single create may carry, so one
	// request cannot open thousands of inserts in one transaction.
	maxValuesPerCreate = 200
)

type Handler struct {
	queries db.Querier
	pool    *pgxpool.Pool
}

func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{queries: q, pool: pool}
}

// The handlers parse the request and map errors; the rules live in
// service.go, shared with the MCP label tools.

// HandleList returns every live label in the caller's tenant with its values.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	out, err := List(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()))
	if err != nil {
		httputil.ServiceError(w, err, "list", "labels")
		return
	}
	httputil.Data(w, out)
}

// HandleCreate inserts a label and, optionally, its first values.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string   `json:"name"`
		Values []string `json:"values"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "name is required")
		return
	}
	out, err := Create(r.Context(), h.pool, auth.TenantIDFromContext(r.Context()), req.Name, req.Values)
	if err != nil {
		httputil.ServiceError(w, err, "create", "label")
		return
	}
	httputil.Created(w, out)
}

// HandleDelete soft-deletes a label while no project carries one of its
// values. 204 even when the label was already gone; 409 names what holds it up.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "labelID")
	if err != nil {
		httputil.BadRequest(w, "invalid label ID")
		return
	}
	if err := Delete(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), id); err != nil {
		httputil.ServiceError(w, err, "delete", "label")
		return
	}
	httputil.NoContent(w)
}

// HandleCreateValue adds one allowed value to a label.
func (h *Handler) HandleCreateValue(w http.ResponseWriter, r *http.Request) {
	labelID, err := httputil.ParseID(r, "labelID")
	if err != nil {
		httputil.BadRequest(w, "invalid label ID")
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "value is required")
		return
	}
	out, err := AddValue(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), labelID, req.Value)
	if err != nil {
		httputil.ServiceError(w, err, "create", "label")
		return
	}
	httputil.Created(w, out)
}

// HandleDeleteValue soft-deletes one value and unsets it on the projects and
// templates that picked it. 204 even when the value was already gone.
func (h *Handler) HandleDeleteValue(w http.ResponseWriter, r *http.Request) {
	valueID, err := httputil.ParseID(r, "valueID")
	if err != nil {
		httputil.BadRequest(w, "invalid value ID")
		return
	}
	if err := DeleteValue(r.Context(), h.pool, auth.TenantIDFromContext(r.Context()), valueID); err != nil {
		httputil.ServiceError(w, err, "delete", "label value")
		return
	}
	httputil.NoContent(w)
}

// HandleValueProjectCount reports how many projects use a value, so the UI can
// warn before it is deleted.
func (h *Handler) HandleValueProjectCount(w http.ResponseWriter, r *http.Request) {
	valueID, err := httputil.ParseID(r, "valueID")
	if err != nil {
		httputil.BadRequest(w, "invalid value ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	count, err := h.queries.CountProjectsForLabelValue(r.Context(), db.CountProjectsForLabelValueParams{
		LabelValueID: valueID, TenantID: tenantID,
	})
	if err != nil {
		httputil.ServerError(w, "count", "projects")
		return
	}
	httputil.Data(w, map[string]int64{"project_count": count})
}
