package dashboard

// Chart share links.
//
// A share link is a CAPABILITY: holding the URL is the permission. That makes it
// a different mechanism from dashboard visibility, which asks "may this user
// browse here", and it is kept separate on purpose — the token read has its own
// query with no owner predicate, so nothing about it can loosen the browsing
// path.
//
// Two rules it does NOT relax:
//   - You must be signed in. The link is for colleagues, not the internet.
//   - The token is scoped to the tenant that issued it. A link pasted into the
//     wrong chat shows another organisation nothing.
//
// One it does: the ROLE gate. A linguist cannot browse dashboards, but may open
// a chart someone deliberately sent them — the gate governs the analytics
// surface, while the token is a person handing over one chart.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/metrics"
)

// shareTokenBytes is the entropy behind a link nobody can guess. 32 bytes is
// the same budget the API tokens use; at base64url that is a 43-character path
// segment.
const shareTokenBytes = 32

func newShareToken() (string, error) {
	buf := make([]byte, shareTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HandleShareChart issues a share link. Gated by mayWrite: handing a chart out
// is at least as consequential as editing it.
//
// Re-sharing mints a NEW token and discards the old one, so "share again" is
// also "revoke whatever I sent last time" — the alternative, handing back a
// token that may already have leaked, is the surprising one.
func (h *Handler) HandleShareChart(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := h.writableChart(w, r)
	if !ok {
		return
	}
	token, err := newShareToken()
	if err != nil {
		logging.Error(r.Context(), err, "generating a share token failed", "chart-share-token-failed", logging.ChartID(id))
		httputil.ServerError(w, "create", "share link")
		return
	}
	n, err := h.q.SetChartShareToken(r.Context(), db.SetChartShareTokenParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
		ShareToken: &token, ShareCreatedBy: &claims.UserID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "saving a share token failed", "chart-share-save-failed", logging.ChartID(id))
		httputil.ServerError(w, "create", "share link")
		return
	}
	if n == 0 {
		httputil.Error(w, http.StatusNotFound, "chart not found")
		return
	}
	httputil.Data(w, map[string]any{"token": token})
}

// HandleRevokeChartShare kills the link. Idempotent: revoking a chart that was
// never shared is a success, because the caller's intent — "this must not be
// reachable by link" — is satisfied either way.
func (h *Handler) HandleRevokeChartShare(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := h.writableChart(w, r)
	if !ok {
		return
	}
	if _, err := h.q.RevokeChartShareToken(r.Context(), db.RevokeChartShareTokenParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	}); err != nil {
		logging.Error(r.Context(), err, "revoking a share token failed", "chart-share-revoke-failed", logging.ChartID(id))
		httputil.ServerError(w, "revoke", "share link")
		return
	}
	httputil.NoContent(w)
}

// writableChart resolves the chart in the URL and checks the caller may write
// it — the same gate as renaming or deleting, held in one place.
func (h *Handler) writableChart(w http.ResponseWriter, r *http.Request) (int64, bool) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, err := httputil.ParseID(r, "chartID")
	if err != nil {
		httputil.BadRequest(w, "invalid chart id")
		return 0, false
	}
	chart, err := h.q.GetDashboardChartTenant(r.Context(), db.GetDashboardChartTenantParams{
		ID: id, TenantID: claims.TenantID, UserID: &claims.UserID,
	})
	if httputil.DBError(w, err, "chart") {
		return 0, false
	}
	d, err := h.dashboardFor(r, chart.DashboardID)
	if httputil.DBError(w, err, "chart") {
		return 0, false
	}
	if !mayWrite(r, d) {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return 0, false
	}
	return id, true
}

// sharedChart is what a link-holder gets: enough to draw the chart and say
// where it came from, and nothing about the dashboard they cannot browse.
type sharedChart struct {
	Title      string          `json:"title"`
	Definition ChartDefinition `json:"definition"`
	Viz        ChartViz        `json:"viz"`
	Dashboard  string          `json:"dashboard"`
	Filters    FilterConfig    `json:"filters"`
	// Controls that change this chart, so a download can say which of the
	// selection actually narrowed it.
	AvailableControls []string `json:"available_controls"`
}

// HandleGetSharedChart resolves a share token. Mounted OUTSIDE the manager-tier
// group: the token is the authorisation, so a linguist who was sent one may
// read that chart.
func (h *Handler) HandleGetSharedChart(w http.ResponseWriter, r *http.Request) {
	row, def, viz, ok := h.sharedChartFor(w, r)
	if !ok {
		return
	}
	var cfg FilterConfig
	if err := json.Unmarshal(row.DashboardFilters, &cfg); err != nil {
		logging.Warn(r.Context(), err, "dashboard filter config is corrupt", "dashboard-filters-corrupt", logging.ChartID(row.ID))
	}
	if cfg.Controls == nil {
		cfg.Controls = []string{}
	}
	httputil.Data(w, sharedChart{
		Title: row.Title, Definition: def, Viz: viz,
		Dashboard: row.DashboardName, Filters: cfg, AvailableControls: AvailableControls(def),
	})
}

// HandleSharedChartData runs a shared chart's definition. Same overrides as the
// dashboard path, so the recipient can move the controls and the link can pin a
// selection.
func (h *Handler) HandleSharedChartData(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	row, def, viz, ok := h.sharedChartFor(w, r)
	if !ok {
		return
	}
	def, viz = parseOverrides(r).apply(def, viz)

	from, to, err := metrics.RangeFromLastDays(def.LastDays, time.Now())
	if err != nil {
		httputil.Error(w, http.StatusUnprocessableEntity, trimValidation(err))
		return
	}
	// Tenant comes from the SESSION, never from the shared row: the token
	// already had to match this tenant to resolve, and reading the id off the
	// caller keeps the metrics pin identical to every other path.
	res, err := metrics.Run(r.Context(), h.pool, claims.TenantID, metrics.Request{
		Measure: def.Measure, Dimensions: def.Dimensions, Filters: def.Filters, From: from, To: to,
		RowBudget: metrics.ChartRowCap,
	})
	if err != nil {
		if errors.Is(err, metrics.ErrValidation) {
			httputil.Error(w, http.StatusUnprocessableEntity, trimValidation(err))
			return
		}
		logging.Error(r.Context(), err, "running a shared chart query failed", "shared-chart-data-failed", logging.ChartID(row.ID))
		httputil.ServerError(w, "run", "chart query")
		return
	}
	httputil.Data(w, chartData{Result: res, XKey: viz.XKey})
}

// sharedChartFor resolves + decodes the token'd chart. A token that doesn't
// match — revoked, mistyped, or from another tenant — is a plain 404.
func (h *Handler) sharedChartFor(w http.ResponseWriter, r *http.Request) (db.GetChartByShareTokenRow, ChartDefinition, ChartViz, bool) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	token := chi.URLParam(r, "token")
	if token == "" {
		httputil.Error(w, http.StatusNotFound, "shared chart not found")
		return db.GetChartByShareTokenRow{}, ChartDefinition{}, ChartViz{}, false
	}
	row, err := h.q.GetChartByShareToken(r.Context(), db.GetChartByShareTokenParams{
		ShareToken: &token, TenantID: claims.TenantID,
	})
	if httputil.DBError(w, err, "shared chart") {
		return db.GetChartByShareTokenRow{}, ChartDefinition{}, ChartViz{}, false
	}
	var def ChartDefinition
	var viz ChartViz
	if err := json.Unmarshal(row.Definition, &def); err != nil {
		logging.Error(r.Context(), err, "dashboard chart row is corrupt", "dashboard-chart-corrupt", logging.ChartID(row.ID))
		httputil.ServerError(w, "read", "chart definition")
		return db.GetChartByShareTokenRow{}, ChartDefinition{}, ChartViz{}, false
	}
	if err := json.Unmarshal(row.Viz, &viz); err != nil {
		logging.Error(r.Context(), err, "dashboard chart row is corrupt", "dashboard-chart-corrupt", logging.ChartID(row.ID))
		httputil.ServerError(w, "read", "chart viz")
		return db.GetChartByShareTokenRow{}, ChartDefinition{}, ChartViz{}, false
	}
	return row, def, viz, true
}
