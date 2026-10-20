// Package customer is the HTTP handler for the customer entity.
//
// Customers are tenant-scoped, soft-deletable, and uniquely named per tenant
// (case-insensitive). Every query in this package filters by tenant_id and
// `deleted_at IS NULL`.
//
// Role gating: every route is manager-tier (RequireManager); delete is
// owner-only. Wired in api.go.
package customer

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// pgUniqueViolationCode is the SQLSTATE for unique_violation. Used to map
// duplicate-name attempts to a clean 409 instead of a 500.
const pgUniqueViolationCode = "23505"

// maxNameLen caps customer name length defensively. Postgres TEXT has no
// hard limit but we don't want abuse + UI spillover.
const maxNameLen = 200

type Handler struct {
	queries db.Querier
}

func NewHandler(q db.Querier) *Handler {
	return &Handler{queries: q}
}

// HandleList returns all live customers for the caller's tenant, sorted by
// name (case-insensitive). Frontend uses this to populate the customer
// dropdown on project create / edit.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	customers, err := h.queries.ListCustomers(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "customers")
		return
	}
	httputil.Data(w, customers)
}

// HandleGet returns one customer by ID, scoped to the caller's tenant.
// Returns 404 if not found OR soft-deleted (the query filters both).
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "customerID")
	if err != nil {
		httputil.BadRequest(w, "invalid customer ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	c, err := h.queries.GetCustomer(r.Context(), db.GetCustomerParams{
		ID: id, TenantID: tenantID,
	})
	if httputil.DBError(w, err, "customer") {
		return
	}
	httputil.Data(w, c)
}

// HandleCreate inserts a new customer.
//
// Validates: name non-empty after trim, length <= maxNameLen.
// Returns 409 on case-insensitive duplicate within the tenant (caught via
// the partial unique index).
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "name is required")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}
	if len(name) > maxNameLen {
		httputil.BadRequest(w, "name too long (max 200 characters)")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	c, err := h.queries.CreateCustomer(r.Context(), db.CreateCustomerParams{
		TenantID: tenantID,
		Name:     name,
	})
	if err != nil {
		if isUniqueViolation(err) {
			httputil.Error(w, http.StatusConflict, "a customer with this name already exists")
			return
		}
		httputil.ServerError(w, "create", "customer")
		return
	}
	httputil.Created(w, c)
}

// HandleUpdate renames a customer.
//
// `name` is required.
// Returns 409 on duplicate, 404 if not found or already soft-deleted.
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "customerID")
	if err != nil {
		httputil.BadRequest(w, "invalid customer ID")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "name is required")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}
	if len(name) > maxNameLen {
		httputil.BadRequest(w, "name too long (max 200 characters)")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	c, err := h.queries.UpdateCustomer(r.Context(), db.UpdateCustomerParams{
		ID: id, TenantID: tenantID, Name: name,
	})
	if err != nil {
		if isUniqueViolation(err) {
			httputil.Error(w, http.StatusConflict, "a customer with this name already exists")
			return
		}
		// DBError handles ErrNoRows → 404 (incl. soft-deleted rows since the
		// query has WHERE deleted_at IS NULL) and any other error → 500.
		httputil.DBError(w, err, "customer")
		return
	}
	httputil.Data(w, c)
}

// HandleDelete soft-deletes a customer (sets deleted_at).
//
// Projects referencing this customer keep their customer_id (no cascade)
// because the FK uses ON DELETE SET NULL — but here we soft-delete, so the
// FK isn't even triggered. Project queries that JOIN customers and filter
// `deleted_at IS NULL` will see a NULL customer for those projects.
//
// Returns 204 even if the customer was already deleted (idempotent).
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "customerID")
	if err != nil {
		httputil.BadRequest(w, "invalid customer ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if err := h.queries.SoftDeleteCustomer(r.Context(), db.SoftDeleteCustomerParams{
		ID: id, TenantID: tenantID,
	}); err != nil {
		httputil.ServerError(w, "delete", "customer")
		return
	}
	httputil.NoContent(w)
}

// HandleProjectCount returns the number of projects referencing a customer.
// Used by the frontend to surface a confirmation before delete:
// "12 projects reference this customer — they'll be detached." Tenant-scoped.
func (h *Handler) HandleProjectCount(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "customerID")
	if err != nil {
		httputil.BadRequest(w, "invalid customer ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	count, err := h.queries.CountProjectsForCustomer(r.Context(), db.CountProjectsForCustomerParams{
		CustomerID: &id, TenantID: tenantID,
	})
	if err != nil {
		httputil.ServerError(w, "count", "projects")
		return
	}
	httputil.Data(w, map[string]int64{"project_count": count})
}

// isUniqueViolation returns true if err is a Postgres unique_violation,
// regardless of which constraint. Customers' only unique index is on
// (tenant_id, lower(name)), so any 23505 here is a duplicate name.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolationCode
}
