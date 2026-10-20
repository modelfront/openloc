package project

// Project lifecycle status, customer assignment, partial PATCH for metadata,
// and the filtered + paginated list.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
)

// parseDeadline parses an ISO 8601 datetime string from the FE into a
// pgtype.Timestamptz suitable for both CreateProject (where empty = NULL)
// and UpdateProjectMetadata's `deadline` narg (nil = "keep current",
// non-nil set value = "set"). The clear-via-explicit-null path is
// handled separately by ClearDeadline in the PATCH; this helper only
// distinguishes "absent/empty → return zero (Valid=false)" from
// "present → parse + return Valid=true".
//
// Deadlines are ISO 8601 with a timezone offset; any RFC 3339 variant is
// accepted (YYYY-MM-DDThh:mm:ssZ, ±hh:mm, fractional seconds). The web
// deadline picker sends UTC (`Date.toISOString()`, a `Z` suffix).
func parseDeadline(s string) (pgtype.Timestamptz, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return pgtype.Timestamptz{}, nil
	}
	// Go parses fractional seconds with either layout; RFC3339Nano makes
	// sub-second support explicit here.
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return pgtype.Timestamptz{}, fmt.Errorf("invalid deadline: must be ISO 8601 (e.g. 2026-05-08T17:00:00Z)")
	}
	return pgtype.Timestamptz{Time: t, Valid: true}, nil
}

// projectStatuses is the source of truth for the status enum on the wire.
// Mirror of the PG enum; if the DB type changes, update this. The lookup map
// and the rejection message below are both derived from it, so a value can
// never be accepted but missing from the error text (or vice versa).
var projectStatuses = []db.ProjectStatus{
	db.ProjectStatusDraft,
	db.ProjectStatusInProgress,
	db.ProjectStatusCompleted,
	db.ProjectStatusOnHold,
}

var validProjectStatuses = func() map[string]bool {
	m := make(map[string]bool, len(projectStatuses))
	for _, s := range projectStatuses {
		m[string(s)] = true
	}
	return m
}()

// invalidStatusMessage is the 400 body for a rejected status.
var invalidStatusMessage = func() string {
	names := make([]string, len(projectStatuses))
	for i, s := range projectStatuses {
		names[i] = string(s)
	}
	return "invalid status (must be " + strings.Join(names, " / ") + ")"
}()

// projectListMaxLimit caps the project list page size. It is also
// the default when no ?limit is sent, because the web client reads only
// r.data and does not paginate. Bounded to prevent OOM from a runaway client.
const projectListMaxLimit int32 = 10000

// HandleList returns a paginated, filtered list of projects for the tenant.
// All filter query params are optional; cursor pagination via `cursor`
// (id of the last item from the previous page, 0 for the first page) and
// `limit` (max 10000). With no ?limit the list returns up to
// projectListMaxLimit rows.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	page := httputil.ParseCursor(r, projectListMaxLimit)
	if r.URL.Query().Get("limit") == "" {
		// Override the helper's 50 default — see projectListMaxLimit comment.
		page.Limit = projectListMaxLimit
	}

	params, err := ListFilterParams(r, tenantID, page)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	rows, err := h.queries.ListProjectsFiltered(r.Context(), params)
	if err != nil {
		httputil.ServerError(w, "list", "projects")
		return
	}

	hasMore := int32(len(rows)) > page.Limit
	if hasMore {
		rows = rows[:page.Limit]
	}
	var nextCursor int64
	if hasMore && len(rows) > 0 {
		nextCursor = rows[len(rows)-1].ID
	}
	httputil.Paginated(w, rows, nextCursor, hasMore, -1)
}

// ListFilters are the project list's optional filters. Zero values filter
// nothing.
type ListFilters struct {
	Status     string
	CustomerID int64 // a Customer label value
	Source     string
	Target     string
	CreatedBy  int64
	Q          string
}

// ListParams builds the project list query from filters, validating the status
// and normalizing the language codes. It asks for page.Limit+1 rows so the
// caller can tell whether there is a next page. The error is a user-facing
// message.
func ListParams(tenantID int64, page httputil.CursorParams, f ListFilters) (db.ListProjectsFilteredParams, error) {
	params := db.ListProjectsFilteredParams{
		TenantID: tenantID,
		Cursor:   page.Cursor,
		Lim:      page.Limit + 1, // +1 to detect has_more
	}
	if s := strings.TrimSpace(f.Status); s != "" {
		if !validProjectStatuses[s] {
			return params, errors.New(invalidStatusMessage)
		}
		params.Status = db.NullProjectStatus{ProjectStatus: db.ProjectStatus(s), Valid: true}
	}
	if f.CustomerID != 0 {
		params.CustomerID = &f.CustomerID
	}
	if s := strings.TrimSpace(f.Source); s != "" {
		normalized := langcode.Normalize(s)
		params.SourceLang = &normalized
	}
	if t := strings.TrimSpace(f.Target); t != "" {
		normalized := langcode.Normalize(t)
		params.TargetLang = &normalized
	}
	if f.CreatedBy != 0 {
		params.CreatedBy = &f.CreatedBy
	}
	if qs := strings.TrimSpace(f.Q); qs != "" {
		params.Q = &qs
	}
	return params, nil
}

// ListFilterParams reads the project list's optional filters (status,
// customer_id, source, target, created_by, q) from the query string into
// ListParams. The error is a user-facing 400 message.
func ListFilterParams(r *http.Request, tenantID int64, page httputil.CursorParams) (db.ListProjectsFilteredParams, error) {
	q := r.URL.Query()
	f := ListFilters{Status: q.Get("status"), Source: q.Get("source"), Target: q.Get("target"), Q: q.Get("q")}
	var err error
	if f.CustomerID, err = positiveID(q.Get("customer_id"), "customer_id"); err != nil {
		return db.ListProjectsFilteredParams{}, err
	}
	if f.CreatedBy, err = positiveID(q.Get("created_by"), "created_by"); err != nil {
		return db.ListProjectsFilteredParams{}, err
	}
	return ListParams(tenantID, page, f)
}

// positiveID parses an optional id query parameter; empty is 0.
func positiveID(raw, name string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid " + name)
	}
	return id, nil
}

// HandleListCreators returns the distinct creators of this tenant's projects —
// the option set for the list's "Created by" filter.
//
// Separate from /api/members on purpose: that lists everyone in the org,
// including members who never created a project, and each of those is a filter
// option that can only produce an empty list.
func (h *Handler) HandleListCreators(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queries.ListProjectCreators(r.Context(), auth.TenantIDFromContext(r.Context()))
	if err != nil {
		httputil.ServerError(w, "list", "project creators")
		return
	}
	httputil.Data(w, rows)
}

// HandleUpdateMetadata is the partial PATCH for project metadata.
// Accepts any subset of {name, description, customer_id, status, deadline,
// client, domain}; absent fields keep their current value. customer_id and
// deadline are tri-state via json.RawMessage: absent keeps, null clears, a
// value sets. The rules live in UpdateMetadata, shared with MCP.
func (h *Handler) HandleUpdateMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}

	patch, err := DecodeMetadataPatch(r)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	project, err := UpdateMetadata(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), id, patch)
	if err != nil {
		WriteServiceError(w, err, "update", "project")
		return
	}
	httputil.Data(w, project)
}

// DecodeMetadataPatch reads a PATCH body into a MetadataPatch. customer_id and
// deadline are tri-state: absent keeps, null clears, a value sets. The error
// is a user-facing 400 message.
func DecodeMetadataPatch(r *http.Request) (MetadataPatch, error) {
	var req struct {
		Name        *string         `json:"name"`
		Description *string         `json:"description"`
		Status      *string         `json:"status"`
		CustomerID  json.RawMessage `json:"customer_id"`
		Deadline    json.RawMessage `json:"deadline"`
		// Optional taxonomy; present-empty clears.
		Client *string `json:"client"`
		Domain *string `json:"domain"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		return MetadataPatch{}, errors.New("invalid request body")
	}
	patch := MetadataPatch{
		Name: req.Name, Description: req.Description, Status: req.Status,
		Client: req.Client, Domain: req.Domain,
	}
	if len(req.CustomerID) > 0 {
		var n int64
		if strings.TrimSpace(string(req.CustomerID)) != "null" {
			if err := json.Unmarshal(req.CustomerID, &n); err != nil {
				return MetadataPatch{}, errors.New("invalid customer_id (must be null or integer)")
			}
		}
		patch.CustomerID = &n
	}
	if len(req.Deadline) > 0 {
		var s string
		if strings.TrimSpace(string(req.Deadline)) != "null" {
			if err := json.Unmarshal(req.Deadline, &s); err != nil {
				return MetadataPatch{}, errors.New("invalid deadline (must be null or ISO 8601 string)")
			}
		}
		patch.Deadline = &s
	}
	return patch, nil
}
