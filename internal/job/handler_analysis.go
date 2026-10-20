package job

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"openloc/internal/analytics"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Job-level + project-level analysis (V1 + V2).

type MatchBand struct {
	Segments      int32 `json:"segments"`
	Words         int32 `json:"words"`
	Characters    int32 `json:"characters"`
	WeightedWords int32 `json:"weighted_words"`
	WeightPct     int32 `json:"weight_pct"`
}

type QPBreakdown struct {
	Approved        MatchBand `json:"approved"`
	ApeApproved     MatchBand `json:"ape_approved"`
	ApeNeedsReview  MatchBand `json:"ape_needs_review"`
	Rejected        MatchBand `json:"rejected"`
	NotRun          MatchBand `json:"not_run"`
	SavedFromReview MatchBand `json:"saved_from_review"`
}

type AnalysisBreakdown struct {
	TMExact   MatchBand   `json:"tm_exact"`
	TMFuzzy95 MatchBand   `json:"tm_fuzzy_95"`
	TMFuzzy85 MatchBand   `json:"tm_fuzzy_85"`
	TMFuzzy75 MatchBand   `json:"tm_fuzzy_75"`
	TMFuzzy50 MatchBand   `json:"tm_fuzzy_50"`
	QE        MatchBand   `json:"qe"`
	New       MatchBand   `json:"new"`
	Total     MatchBand   `json:"total"`
	QP        QPBreakdown `json:"qp"`
}

func aggregateJobAnalysis(rows []db.GetJobAnalysisRow) AnalysisBreakdown {
	var resp AnalysisBreakdown
	for _, r := range rows {
		// `origin` is `translation_origin_v2` (sticky automation
		// lineage — 'tm'|'mt'|'ape'|'file_import' or NULL), so a TM check is
		// a single equality.
		origin := asString(r.Origin)
		band := r.MatchBand
		outcome := asString(r.PredictOutcome)

		b := MatchBand{Segments: r.SegmentCount, Words: r.WordCount, Characters: r.CharCount}

		// TM/MT/QE breakdown. The origin is sticky: a row that came
		// from TM keeps origin='tm' through human edits. The SQL band CASE already gates by
		// origin='tm' so any band != 'no-match' implies a TM-sourced row.
		switch {
		case band == "100":
			resp.TMExact = addBands(resp.TMExact, b)
		case band == "95-99":
			resp.TMFuzzy95 = addBands(resp.TMFuzzy95, b)
		case band == "85-94":
			resp.TMFuzzy85 = addBands(resp.TMFuzzy85, b)
		case band == "75-84":
			resp.TMFuzzy75 = addBands(resp.TMFuzzy75, b)
		case band == "50-74" || band == "below-50":
			resp.TMFuzzy50 = addBands(resp.TMFuzzy50, b)
		case origin == "mt" && (outcome == "approved" || outcome == "ape_approved"):
			resp.QE = addBands(resp.QE, b)
		default:
			// No TM match — "New" in standard TMS analysis terms.
			// Includes MT-translated segments (MT is a pre-translation method, not a match category).
			resp.New = addBands(resp.New, b)
		}

		// QP breakdown
		switch outcome {
		case "approved":
			resp.QP.Approved = addBands(resp.QP.Approved, b)
		case "ape_approved":
			resp.QP.ApeApproved = addBands(resp.QP.ApeApproved, b)
		case "ape_needs_review":
			resp.QP.ApeNeedsReview = addBands(resp.QP.ApeNeedsReview, b)
		case "rejected":
			resp.QP.Rejected = addBands(resp.QP.Rejected, b)
		default:
			resp.QP.NotRun = addBands(resp.QP.NotRun, b)
		}

		resp.Total = addBands(resp.Total, b)
	}

	// Saved from review = approved + ape_approved
	resp.QP.SavedFromReview = addBands(resp.QP.Approved, resp.QP.ApeApproved)
	return resp
}

func addBands(a, b MatchBand) MatchBand {
	return MatchBand{
		Segments:   a.Segments + b.Segments,
		Words:      a.Words + b.Words,
		Characters: a.Characters + b.Characters,
	}
}

// applyWeights sets WeightedWords and WeightPct on each band of an AnalysisBreakdown.
func applyWeights(resp *AnalysisBreakdown, weights map[string]int32) {
	applyBandWeight(&resp.TMExact, weights["tm_exact"])
	applyBandWeight(&resp.TMFuzzy95, weights["tm_fuzzy_95"])
	applyBandWeight(&resp.TMFuzzy85, weights["tm_fuzzy_85"])
	applyBandWeight(&resp.TMFuzzy75, weights["tm_fuzzy_75"])
	applyBandWeight(&resp.TMFuzzy50, weights["tm_fuzzy_50"])
	applyBandWeight(&resp.New, weights["new"])
	applyBandWeight(&resp.QE, weights["qe"])

	// Recalculate total weighted words
	resp.Total.WeightedWords = resp.TMExact.WeightedWords +
		resp.TMFuzzy95.WeightedWords +
		resp.TMFuzzy85.WeightedWords +
		resp.TMFuzzy75.WeightedWords +
		resp.TMFuzzy50.WeightedWords +
		resp.New.WeightedWords +
		resp.QE.WeightedWords
	resp.Total.WeightPct = 0 // not meaningful for total
}

func applyBandWeight(b *MatchBand, pct int32) {
	b.WeightPct = pct
	b.WeightedWords = int32(int64(b.Words) * int64(pct) / 100)
}

// fetchWeightsMap loads tenant weights from DB and merges with defaults.
//
// Takes ctx + tenantID directly (rather than *http.Request) so the
// helper can be called from non-HTTP code paths — currently the
// shared analysis-orchestration helper used by both V2 (JSON) and
// the CSV export.
func fetchWeightsMap(ctx context.Context, q db.Querier, tenantID int64) map[string]int32 {
	rows, err := q.ListAnalysisWeights(ctx, tenantID)
	if err != nil {
		// On error, use defaults — non-fatal
		return analytics.DefaultWeights
	}
	return analytics.GetWeightsMap(rows)
}

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func (h *Handler) HandleAnalysis(w http.ResponseWriter, r *http.Request) {
	job, _, err := h.resolveJob(r)
	if err != nil {
		httputil.NotFound(w, "job")
		return
	}
	resp, err := h.Analysis(r.Context(), auth.TenantIDFromContext(r.Context()), job)
	if err != nil {
		logging.Error(r.Context(), err, "job analysis failed", "job-analysis-failed", logging.JobID(job.ID))
		httputil.ServerError(w, "run", "analysis")
		return
	}
	httputil.Data(w, resp)
}

type projectAnalysisResponse struct {
	Languages map[string]AnalysisBreakdown `json:"languages"`
	Total     AnalysisBreakdown            `json:"total"`
}

func (h *Handler) HandleProjectAnalysis(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}

	// Tenant-check first. The underlying GetProjectAnalysis SQL
	// filters only by project_id (not tenant_id), so without this
	// check a cross-tenant request would receive 200 + the other
	// tenant's analysis numbers. V2 does the same in
	// computeProjectAnalysis.
	tenantID := auth.TenantIDFromContext(r.Context())
	_, err = h.queries.GetProject(r.Context(), db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if httputil.DBError(w, err, "project") {
		return
	}

	rows, err := h.queries.GetProjectAnalysis(r.Context(), projectID)
	if err != nil {
		logging.Error(r.Context(), err, "project analysis failed", "project-analysis-failed", logging.ProjectID(projectID))
		httputil.ServerError(w, "run", "analysis")
		return
	}

	// Group by language, then aggregate
	byLang := map[string][]db.GetJobAnalysisRow{}
	for _, r := range rows {
		// Convert GetProjectAnalysisRow → GetJobAnalysisRow (same fields minus target_language)
		jr := db.GetJobAnalysisRow{
			Origin:         r.Origin,
			PredictOutcome: r.PredictOutcome,
			MatchBand:      r.MatchBand,
			SegmentCount:   r.SegmentCount,
			WordCount:      r.WordCount,
			CharCount:      r.CharCount,
		}
		byLang[r.TargetLanguage] = append(byLang[r.TargetLanguage], jr)
	}

	weights := fetchWeightsMap(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()))
	resp := projectAnalysisResponse{Languages: map[string]AnalysisBreakdown{}}
	var allRows []db.GetJobAnalysisRow
	for lang, langRows := range byLang {
		a := aggregateJobAnalysis(langRows)
		applyWeights(&a, weights)
		resp.Languages[lang] = a
		allRows = append(allRows, langRows...)
	}
	resp.Total = aggregateJobAnalysis(allRows)
	applyWeights(&resp.Total, weights)

	httputil.Data(w, resp)
}

// --- V2 Analysis (per-file breakdown) ---

type FileAnalysis struct {
	FileID      int64             `json:"file_id"`
	FileName    string            `json:"file_name"`
	TargetLang  string            `json:"target_language"`
	Analysis    AnalysisBreakdown `json:"analysis"`
	Repetitions MatchBand         `json:"repetitions"`
}

type ProjectAnalysis struct {
	Files            []FileAnalysis               `json:"files"`
	Languages        map[string]AnalysisBreakdown `json:"languages"`
	Total            AnalysisBreakdown            `json:"total"`
	TotalRepetitions MatchBand                    `json:"total_repetitions"`
}

// fileKey uniquely identifies a file+language combination for grouping.
type fileKey struct {
	FileID     int64
	TargetLang string
}

func (h *Handler) HandleProjectAnalysisV2(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	resp, err := ComputeProjectAnalysis(r.Context(), h.queries, tenantID, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		httputil.NotFound(w, "project")
		return
	}
	if err != nil {
		logging.Error(r.Context(), err, "project analysis v2 failed", "project-analysis-v2-failed", logging.ProjectID(projectID))
		httputil.ServerError(w, "run", "analysis")
		return
	}
	httputil.Data(w, resp)
}

// ComputeProjectAnalysis returns the project analysis the Analysis tab (V2)
// and the CSV export show: bands per file and language, and weighted words.
// A project outside the tenant is pgx.ErrNoRows.
func ComputeProjectAnalysis(ctx context.Context, q db.Querier, tenantID, projectID int64) (ProjectAnalysis, error) {
	c, err := computeProjectAnalysis(ctx, q, tenantID, projectID)
	if err != nil {
		return ProjectAnalysis{}, err
	}
	return c.Response, nil
}

// projectAnalysisCompute bundles the V2 JSON response with the project name
// the CSV export needs for its Content-Disposition filename.
type projectAnalysisCompute struct {
	Response    ProjectAnalysis
	ProjectName string
}

// computeProjectAnalysis is the shared orchestration behind V2 (JSON)
// and the CSV export. It tenant-checks the project FIRST (returning
// pgx.ErrNoRows on cross-tenant attempts so callers can translate to
// 404), then runs the analysis aggregation.
//
// Shared so the CSV renders the EXACT SAME numbers the V2 endpoint
// serves — the spreadsheet must match the on-screen tab.
//
// The tenant check must stay first: the analysis queries
// (`GetProjectAnalysisByFile`, `GetProjectRepetitions`) filter only by
// `project_id`, so GetProject(id, tenantID) is the tenant boundary.
func computeProjectAnalysis(ctx context.Context, q db.Querier, tenantID, projectID int64) (*projectAnalysisCompute, error) {
	project, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		// Includes pgx.ErrNoRows on cross-tenant. Caller translates to 404.
		return nil, err
	}

	rows, err := q.GetProjectAnalysisByFile(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("project analysis query: %w", err)
	}

	repRows, err := q.GetProjectRepetitions(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("project repetitions query: %w", err)
	}

	// Build repetition lookup: fileKey -> MatchBand.
	repMap := map[fileKey]MatchBand{}
	for _, rr := range repRows {
		k := fileKey{FileID: rr.FileID, TargetLang: rr.TargetLanguage}
		repMap[k] = MatchBand{Segments: rr.SegmentCount, Words: rr.WordCount, Characters: rr.CharCount}
	}

	// Group rows by file+lang, then aggregate.
	type fileGroup struct {
		fileName string
		rows     []db.GetJobAnalysisRow
	}
	grouped := map[fileKey]*fileGroup{}
	var orderedKeys []fileKey
	for _, row := range rows {
		k := fileKey{FileID: row.FileID, TargetLang: row.TargetLanguage}
		if _, ok := grouped[k]; !ok {
			grouped[k] = &fileGroup{fileName: row.FileName}
			orderedKeys = append(orderedKeys, k)
		}
		grouped[k].rows = append(grouped[k].rows, db.GetJobAnalysisRow{
			Origin:         row.Origin,
			PredictOutcome: row.PredictOutcome,
			MatchBand:      row.MatchBand,
			SegmentCount:   row.SegmentCount,
			WordCount:      row.WordCount,
			CharCount:      row.CharCount,
		})
	}

	weights := fetchWeightsMap(ctx, q, tenantID)

	resp := ProjectAnalysis{
		Files:     make([]FileAnalysis, 0, len(orderedKeys)),
		Languages: map[string]AnalysisBreakdown{},
	}
	byLang := map[string][]db.GetJobAnalysisRow{}
	var allRows []db.GetJobAnalysisRow

	for _, k := range orderedKeys {
		g := grouped[k]
		a := aggregateJobAnalysis(g.rows)
		applyWeights(&a, weights)
		rep := repMap[k]
		applyBandWeight(&rep, weights["repetitions"])
		resp.Files = append(resp.Files, FileAnalysis{
			FileID:      k.FileID,
			FileName:    g.fileName,
			TargetLang:  k.TargetLang,
			Analysis:    a,
			Repetitions: rep,
		})
		byLang[k.TargetLang] = append(byLang[k.TargetLang], g.rows...)
		allRows = append(allRows, g.rows...)
	}

	for lang, langRows := range byLang {
		a := aggregateJobAnalysis(langRows)
		applyWeights(&a, weights)
		resp.Languages[lang] = a
	}
	resp.Total = aggregateJobAnalysis(allRows)
	applyWeights(&resp.Total, weights)

	// Total repetitions across all files.
	var totalRep MatchBand
	for _, rep := range repMap {
		totalRep = addBands(totalRep, rep)
	}
	applyBandWeight(&totalRep, weights["repetitions"])
	resp.TotalRepetitions = totalRep

	return &projectAnalysisCompute{
		Response:    resp,
		ProjectName: project.Name,
	}, nil
}
