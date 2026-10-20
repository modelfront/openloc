package analysis

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Enqueuer dispatches the saved-analysis task to the asynq queue. The
// worker (analysis.Service) picks it up and computes breakdowns. nil is
// allowed in tests that exercise the gate without driving the worker.
type Enqueuer interface {
	EnqueueAnalysis(analysisID, tenantID int64) error
}

type Handler struct {
	queries  db.Querier
	enqueuer Enqueuer
}

func NewHandler(q db.Querier, e Enqueuer) *Handler {
	return &Handler{queries: q, enqueuer: e}
}

// createAnalysisRequest is the POST body shape.
type createAnalysisRequest struct {
	Name   string  `json:"name"`    // optional; defaults to "Analysis YYYY-MM-DD HH:MM"
	JobIDs []int64 `json:"job_ids"` // required, non-empty
}

// HandleCreate implements POST /api/projects/{projectID}/analyses. Creates
// an analysis row with status='pending' and enqueues the worker (see Create).
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	var req createAnalysisRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body: "+err.Error())
		return
	}
	a, err := Create(ctx, h.queries, h.enqueuer, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx),
		CreateInput{ProjectID: projectID, Name: req.Name, JobIDs: req.JobIDs})
	var inputErr InputError
	switch {
	case err == nil:
		httputil.Created(w, a)
	case errors.As(err, &inputErr):
		httputil.BadRequest(w, inputErr.Msg)
	case errors.Is(err, pgx.ErrNoRows):
		httputil.NotFound(w, "project")
	case errors.Is(err, ErrEnqueue):
		httputil.ServerError(w, "enqueue", "analysis")
	default:
		logging.Error(ctx, err, "creating analysis failed", "analysis.create_failed")
		httputil.ServerError(w, "create", "analysis")
	}
}

// HandleList implements GET /api/projects/{projectID}/analyses. Returns
// the list of analyses for the project, most recent first. Detail view
// (per-job breakdowns) is fetched via HandleGet.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	rows, err := List(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), projectID)
	if err != nil {
		if httputil.DBError(w, err, "project") {
			return
		}
		httputil.ServerError(w, "list", "analyses")
		return
	}
	httputil.Data(w, rows)
}

// HandleGet implements GET /api/analyses/{analysisID}. Returns the
// analysis row plus its breakdown rows. Tenant-scoped.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "analysisID")
	if err != nil {
		httputil.BadRequest(w, "invalid analysis ID")
		return
	}
	a, breakdowns, err := Get(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), id)
	if err != nil {
		if httputil.DBError(w, err, "analysis") {
			return
		}
		httputil.ServerError(w, "load", "analysis")
		return
	}
	httputil.Data(w, map[string]any{
		"analysis":   a,
		"breakdowns": breakdowns,
	})
}

// leverageBand holds one band's totals for the live job leverage response.
type leverageBand struct {
	Segments               int32 `json:"segments"`
	Words                  int32 `json:"words"`
	Characters             int32 `json:"characters"`
	SegmentsAutoApproved   int32 `json:"segments_auto_approved"`
	WordsAutoApproved      int32 `json:"words_auto_approved"`
	CharactersAutoApproved int32 `json:"characters_auto_approved"`
	// Needs-review subset — segments in this band with status='needs_review'.
	// The CAT-editor status bar surfaces this as "(N needs review)" so the
	// translator sees at a glance how many segments still need human
	// attention. Always a subset of the band totals; mutually exclusive
	// with the auto_approved subset (status is a single enum, can only
	// be one of needs_review / auto_approved / approved at any moment).
	SegmentsNeedsReview   int32 `json:"segments_needs_review"`
	WordsNeedsReview      int32 `json:"words_needs_review"`
	CharactersNeedsReview int32 `json:"characters_needs_review"`
}

type leverageResponse struct {
	JobID          int64        `json:"job_id"`
	TargetLanguage string       `json:"target_language"`
	Tm101          leverageBand `json:"tm_101"`
	Tm100          leverageBand `json:"tm_100"`
	Tm8599         leverageBand `json:"tm_85_99"`
	Tm6584         leverageBand `json:"tm_65_84"`
	New            leverageBand `json:"new"`
	Repetitions    leverageBand `json:"repetitions"`
}

// HandleJobLeverage implements GET /api/jobs/{jobID}/leverage. Returns the
// 6-band breakdown for ONE job — the same band shape as a saved analysis,
// but computed live from current stored translations.tm_match_percent +
// status. Drives the CAT-editor status bar.
//
// Response shape mirrors a single job_analysis row (just the band totals,
// no auto_approved subset split into a separate object — flat for cheap
// status-bar rendering).
func (h *Handler) HandleJobLeverage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)

	jobID, err := strconv.ParseInt(chi.URLParam(r, "jobID"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}

	// Tenant scope via the job → file → project → tenant chain. We use
	// GetJobForAnalysis (already in our queries) since it returns tenant_id
	// alongside the job context — one round-trip for the scope check + the
	// metadata the response needs.
	jobCtx, err := h.queries.GetJobForAnalysis(ctx, jobID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.NotFound(w, "job")
			return
		}
		httputil.ServerError(w, "load", "job")
		return
	}
	if jobCtx.TenantID != tenantID {
		httputil.NotFound(w, "job") // don't leak existence across tenants
		return
	}

	bands, err := h.queries.GetSavedAnalysisBands(ctx, jobID)
	if err != nil {
		httputil.ServerError(w, "compute", "leverage")
		return
	}
	// Second small query for the needs-review subset (planning doc §"Live
	// job-level view"). Separate from the totals query because the totals
	// group by (band, is_auto_approved) and needs_review is a different
	// status that wouldn't compose cleanly into the same GROUP BY.
	needsReview, err := h.queries.GetJobLeverageNeedsReview(ctx, jobID)
	if err != nil {
		httputil.ServerError(w, "compute", "leverage needs-review")
		return
	}

	resp := leverageResponse{JobID: jobID, TargetLanguage: jobCtx.TargetLanguage}
	for _, b := range bands {
		target := pickBand(&resp, b.Band)
		if target == nil {
			continue
		}
		target.Segments += b.Segments
		target.Words += b.Words
		target.Characters += b.Characters
		if b.IsAutoApproved {
			target.SegmentsAutoApproved += b.Segments
			target.WordsAutoApproved += b.Words
			target.CharactersAutoApproved += b.Characters
		}
	}
	for _, b := range needsReview {
		target := pickBand(&resp, b.Band)
		if target == nil {
			continue
		}
		target.SegmentsNeedsReview += b.Segments
		target.WordsNeedsReview += b.Words
		target.CharactersNeedsReview += b.Characters
	}
	httputil.Data(w, resp)
}

func pickBand(r *leverageResponse, name string) *leverageBand {
	switch name {
	case "tm_101":
		return &r.Tm101
	case "tm_100":
		return &r.Tm100
	case "tm_85_99":
		return &r.Tm8599
	case "tm_65_84":
		return &r.Tm6584
	case "new":
		return &r.New
	case "repetitions":
		return &r.Repetitions
	}
	return nil
}

// HandleDelete implements DELETE /api/analyses/{analysisID}. Soft delete
// (see Delete); an id with no live analysis is a no-op 204.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "analysisID")
	if err != nil {
		httputil.BadRequest(w, "invalid analysis ID")
		return
	}
	if err := Delete(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), id); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		logging.Error(r.Context(), err, "deleting analysis failed", "analysis.delete_failed", logging.AnalysisID(id))
		httputil.ServerError(w, "delete", "analysis")
		return
	}
	httputil.NoContent(w)
}

// ── helpers ──

func uniqueIntCount(s []int64) int {
	seen := map[int64]bool{}
	for _, v := range s {
		seen[v] = true
	}
	return len(seen)
}

func findMissing(want []int64, got []db.ListJobsByIDsForProjectRow) []int64 {
	have := map[int64]bool{}
	for _, r := range got {
		have[r.ID] = true
	}
	var missing []int64
	for _, id := range want {
		if !have[id] {
			missing = append(missing, id)
		}
	}
	return missing
}
