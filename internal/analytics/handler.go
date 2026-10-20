package analytics

import (
	"net/http"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// DefaultWeights returns industry-standard match band weights.
// Applied when a tenant has no custom weights configured.
var DefaultWeights = map[string]int32{
	"tm_exact":    0,
	"tm_fuzzy_95": 25,
	"tm_fuzzy_85": 50,
	"tm_fuzzy_75": 75,
	"tm_fuzzy_50": 100,
	"new":         100,
	"repetitions": 0,
	"qe":          0,
}

// AllBands is the canonical ordering for weight bands.
var AllBands = []string{
	"tm_exact", "tm_fuzzy_95", "tm_fuzzy_85", "tm_fuzzy_75", "tm_fuzzy_50",
	"new", "repetitions", "qe",
}

// Handler provides HTTP endpoints for analytics configuration.
type Handler struct {
	queries db.Querier
}

func NewHandler(q db.Querier) *Handler {
	return &Handler{queries: q}
}

// WeightEntry is a single band weight for API response.
type WeightEntry struct {
	Band      string `json:"band"`
	WeightPct int32  `json:"weight_pct"`
}

// HandleGetWeights returns the tenant's analysis weights (defaults merged with custom).
func (h *Handler) HandleGetWeights(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	rows, err := h.queries.ListAnalysisWeights(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "weights")
		return
	}

	weights := mergeWeights(rows)
	httputil.Data(w, weights)
}

// HandleUpdateWeights bulk-upserts analysis weights for the tenant.
func (h *Handler) HandleUpdateWeights(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	var req struct {
		Weights []WeightEntry `json:"weights"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Validate
	validBands := make(map[string]bool, len(AllBands))
	for _, b := range AllBands {
		validBands[b] = true
	}

	for _, we := range req.Weights {
		if !validBands[we.Band] {
			httputil.BadRequest(w, "invalid band: "+we.Band)
			return
		}
		if we.WeightPct < 0 || we.WeightPct > 100 {
			httputil.BadRequest(w, "weight_pct must be 0-100")
			return
		}
	}

	// Upsert each weight
	for _, we := range req.Weights {
		if _, err := h.queries.UpsertAnalysisWeight(r.Context(), db.UpsertAnalysisWeightParams{
			TenantID:  tenantID,
			Band:      we.Band,
			WeightPct: we.WeightPct,
		}); err != nil {
			httputil.ServerError(w, "update", "weights")
			return
		}
	}

	// Return merged result
	rows, err := h.queries.ListAnalysisWeights(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "weights")
		return
	}

	weights := mergeWeights(rows)
	httputil.Data(w, weights)
}

// GetWeightsMap returns the tenant's weights as a map (for use by analysis handlers).
func GetWeightsMap(rows []db.AnalysisWeight) map[string]int32 {
	result := make(map[string]int32, len(AllBands))
	for k, v := range DefaultWeights {
		result[k] = v
	}
	for _, row := range rows {
		result[row.Band] = row.WeightPct
	}
	return result
}

// --- Productivity ---

func wordsPerHour(words int32, timeMs int64) float64 {
	if timeMs <= 0 {
		return 0
	}
	hours := float64(timeMs) / 3_600_000
	if hours < 0.001 {
		return 0
	}
	return float64(words) / hours
}

// HandleProjectProductivity returns per-translator productivity for a
// project.
func (h *Handler) HandleProjectProductivity(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	entries, err := ProjectProductivity(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), projectID)
	if err != nil {
		if httputil.DBError(w, err, "project") {
			return
		}
		httputil.ServerError(w, "get", "productivity")
		return
	}
	httputil.Data(w, entries)
}

// HandleProjectStepProductivity returns per-workflow-step, per-translator
// productivity for a project. Where HandleProjectProductivity
// credits only the last editor, this attributes every confirm to the step it
// happened in.
func (h *Handler) HandleProjectStepProductivity(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	entries, err := ProjectStepProductivity(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), projectID)
	if err != nil {
		if httputil.DBError(w, err, "project") {
			return
		}
		httputil.ServerError(w, "get", "step productivity")
		return
	}
	httputil.Data(w, entries)
}

// HandleOrgProductivity returns org-wide productivity for ?from=&to=
// (YYYY-MM-DD). Defaults to the last 30 days; an unparsable date falls back to
// its default.
func (h *Handler) HandleOrgProductivity(w http.ResponseWriter, r *http.Request) {
	from, to := DefaultProductivityRange(time.Now())
	if t, err := parseDate(r.URL.Query().Get("from")); err == nil {
		from = t
	}
	if t, err := parseDate(r.URL.Query().Get("to")); err == nil {
		to = t
	}
	entries, err := OrgProductivity(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), from, to)
	if err != nil {
		httputil.ServerError(w, "get", "productivity")
		return
	}
	httputil.Data(w, entries)
}

// DefaultProductivityRange is the org report's default window: the last 30
// days, through the end of today.
func DefaultProductivityRange(now time.Time) (from, to time.Time) {
	return now.AddDate(0, 0, -30), now.AddDate(0, 0, 1)
}

func parseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// mergeWeights combines defaults with stored overrides into a stable-ordered list.
func mergeWeights(rows []db.AnalysisWeight) []WeightEntry {
	stored := make(map[string]int32, len(rows))
	for _, row := range rows {
		stored[row.Band] = row.WeightPct
	}

	result := make([]WeightEntry, 0, len(AllBands))
	for _, band := range AllBands {
		pct, ok := stored[band]
		if !ok {
			pct = DefaultWeights[band]
		}
		result = append(result, WeightEntry{Band: band, WeightPct: pct})
	}
	return result
}
