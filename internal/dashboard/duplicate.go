package dashboard

// Duplicating a dashboard.
//
// A dashboard is a saved VIEW: its charts store query definitions, never data.
// So a copy is one row plus N small JSON rows, and it answers the same
// questions as the original from the moment it exists.
//
// The gate is on the DESTINATION, not the source. You need to read the original
// — which the visibility predicate already decides — and the right to create on
// the side you are copying to. Copying the org's dashboard into your own space
// is therefore open to anyone who can see it, which is the point: it is how you
// experiment on a shared view without touching it.

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// copyPrefix matches the template duplicate wording, so "copy" reads the same
// everywhere in the app.
const copyPrefix = "Copy of "

// maxCopies bounds the free-name search. Someone with 50 copies of one
// dashboard has a different problem, and an unbounded loop is a query per
// attempt.
const maxCopies = 50

// HandleDuplicateDashboard copies a dashboard and its charts to the requested
// side. Charts are copied WITHOUT their share tokens: the copy is a new
// dashboard nobody has shared anything from, and inheriting a live link would
// hand its contents to whoever holds the old URL. The original's links are
// untouched.
//
// POST /api/dashboards/{dashboardID}/duplicate  {"personal": true|false}
func (h *Handler) HandleDuplicateDashboard(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "dashboardID")
	if err != nil {
		httputil.BadRequest(w, "invalid dashboard id")
		return
	}
	var body struct {
		Personal bool `json:"personal"`
	}
	if err := httputil.Decode(r, &body); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	src, err := h.dashboardFor(r, id)
	if httputil.DBError(w, err, "dashboard") {
		return
	}
	// Creating a SHARED dashboard is publishing to the org; creating one in your
	// own space is not. Note this is the destination's rule — the source only
	// had to be readable.
	if !body.Personal && !auth.IsAdmin(r) {
		httputil.Error(w, http.StatusForbidden, "Only an owner or admin can create a shared dashboard.")
		return
	}
	var owner *int64
	if body.Personal {
		owner = &claims.UserID
	}

	name, err := h.freeCopyName(r.Context(), claims.TenantID, owner, src.Name)
	if err != nil {
		logging.Error(r.Context(), err, "finding a free copy name failed", "dashboard-duplicate-failed", logging.DashboardID(id))
		httputil.ServerError(w, "copy", "dashboard")
		return
	}

	charts, err := h.q.ListDashboardCharts(r.Context(), db.ListDashboardChartsParams{
		DashboardID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "listing charts to copy failed", "dashboard-duplicate-failed", logging.DashboardID(id))
		httputil.ServerError(w, "copy", "dashboard")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "copy", "dashboard")
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)

	dup, err := q.CreateDashboard(r.Context(), db.CreateDashboardParams{
		TenantID: claims.TenantID, Name: name, CreatedBy: &claims.UserID, OwnerID: owner,
	})
	if err != nil {
		// Two copies racing read the same free name; the loser renames.
		if httputil.IsUniqueViolation(err) {
			httputil.Error(w, http.StatusConflict, "A dashboard with this name already exists. Try again.")
			return
		}
		logging.Error(r.Context(), err, "creating the copy failed", "dashboard-duplicate-failed", logging.DashboardID(id))
		httputil.ServerError(w, "copy", "dashboard")
		return
	}
	// The filter controls are part of the view, so a copy that dropped them
	// would answer a different question than the dashboard it came from.
	if _, err := q.UpdateDashboardFilters(r.Context(), db.UpdateDashboardFiltersParams{
		ID: dup.ID, TenantID: claims.TenantID, Filters: src.Filters,
	}); err != nil {
		logging.Error(r.Context(), err, "copying dashboard filters failed", "dashboard-duplicate-failed", logging.DashboardID(id))
		httputil.ServerError(w, "copy", "dashboard")
		return
	}
	// In order: CreateDashboardChart positions each at MAX+1, so the copy keeps
	// the original's arrangement. share_token defaults NULL — the copy is not
	// shared with anyone, and nothing is carried over from the original.
	for _, c := range charts {
		if _, err := q.CreateDashboardChart(r.Context(), db.CreateDashboardChartParams{
			TenantID: claims.TenantID, DashboardID: dup.ID, Title: c.Title,
			Definition: c.Definition, Viz: c.Viz, CreatedBy: &claims.UserID,
		}); err != nil {
			logging.Error(r.Context(), err, "copying a chart failed", "dashboard-duplicate-failed", logging.DashboardID(id))
			httputil.ServerError(w, "copy", "dashboard")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		logging.Error(r.Context(), err, "copying a dashboard failed", "dashboard-duplicate-failed", logging.DashboardID(id))
		httputil.ServerError(w, "copy", "dashboard")
		return
	}
	httputil.Created(w, map[string]any{"id": dup.ID, "name": dup.Name, "personal": owner != nil})
}

// freeCopyName returns the first unused "Copy of X" / "Copy of X 2" … on the
// destination side. Names are unique per side, so copying twice has to produce
// two names rather than one failure.
func (h *Handler) freeCopyName(ctx context.Context, tenantID int64, owner *int64, srcName string) (string, error) {
	base := copyPrefix + srcName
	taken, err := h.q.ListDashboardNamesOnSide(ctx, db.ListDashboardNamesOnSideParams{
		TenantID: tenantID, OwnerID: owner, Prefix: base + "%",
	})
	if err != nil {
		return "", err
	}
	// Case-insensitively, because the unique indexes are on the raw name but a
	// human reading "copy of ops" and "Copy of Ops" sees one thing.
	used := make(map[string]bool, len(taken))
	for _, n := range taken {
		used[strings.ToLower(n)] = true
	}
	if !used[strings.ToLower(base)] {
		return base, nil
	}
	for i := 2; i <= maxCopies; i++ {
		candidate := fmt.Sprintf("%s %d", base, i)
		if !used[strings.ToLower(candidate)] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("more than %d copies of %q already exist", maxCopies, srcName)
}
