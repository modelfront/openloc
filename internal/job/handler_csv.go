package job

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"openloc/internal/auth"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Project-level analysis CSV export: one row per (file, target_language)
// pair, words per match band, so the report drops into a quote
// spreadsheet. The layout is deliberately plain — word counts only (no
// segment/character or per-band weighted columns) and no totals rows — and
// the column order is a contract pinned by the integration test.

// HandleProjectAnalysisCSVExport handles GET /api/projects/{projectID}/analysis.csv.
//
// Tenant-scoped via the shared `computeProjectAnalysis` helper, which
// runs `GetProject(id, tenantID)` first; cross-tenant requests get
// 404 (same posture as V2).
func (h *Handler) HandleProjectAnalysisCSVExport(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())

	computed, err := computeProjectAnalysis(r.Context(), h.queries, tenantID, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		httputil.NotFound(w, "project")
		return
	}
	if err != nil {
		logging.Error(r.Context(), err, "project analysis CSV export failed", "analysis-csv-export-failed", logging.ProjectID(projectID))
		httputil.ServerError(w, "export", "analysis")
		return
	}

	filename := buildAnalysisCSVFilename(computed.ProjectName, projectID, time.Now())
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	httputil.SetAttachmentFilename(w, filename)

	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Header row. Column order is part of the contract — pinned by an
	// integration-test header check. Changing this MUST be a deliberate
	// decision (e.g., new ADR), because external consumers may import
	// by column position.
	_ = writer.Write(analysisCSVHeader)

	// Data rows: one per (file, target_language). The Files slice
	// returned by computeProjectAnalysis is in DB insertion order,
	// but to be deterministic across CSV downloads we re-sort by
	// (file_name, target_language). Customers eyeballing two
	// downloads side-by-side shouldn't see row reordering.
	files := append([]FileAnalysis(nil), computed.Response.Files...)
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].FileName != files[j].FileName {
			return files[i].FileName < files[j].FileName
		}
		return files[i].TargetLang < files[j].TargetLang
	})

	for _, f := range files {
		row := buildAnalysisCSVRow(f)
		_ = writer.Write(row)
	}
}

// analysisCSVHeader is the column-order contract. Kept at package
// level (not inlined) only so changes are a single edit; the
// integration test in package job_test deliberately DUPLICATES the
// expected header (anti-tautology by independence — a test that
// imported this slice would pass vacuously).
var analysisCSVHeader = []string{
	"File",
	"Target Language",
	"Total Segments",
	"Total Words",
	"Repetitions Words",
	"TM 100% Words",
	"TM 95-99% Words",
	"TM 85-94% Words",
	"TM 75-84% Words",
	"TM 50-74% Words",
	"QE Approved Words",
	"New Words",
	"Weighted Words",
}

// buildAnalysisCSVRow renders one (file, target_language) row.
func buildAnalysisCSVRow(f FileAnalysis) []string {
	a := f.Analysis
	return []string{
		f.FileName,
		f.TargetLang,
		strconv.Itoa(int(a.Total.Segments)),
		strconv.Itoa(int(a.Total.Words)),
		strconv.Itoa(int(f.Repetitions.Words)),
		strconv.Itoa(int(a.TMExact.Words)),
		strconv.Itoa(int(a.TMFuzzy95.Words)),
		strconv.Itoa(int(a.TMFuzzy85.Words)),
		strconv.Itoa(int(a.TMFuzzy75.Words)),
		strconv.Itoa(int(a.TMFuzzy50.Words)),
		strconv.Itoa(int(a.QE.Words)),
		strconv.Itoa(int(a.New.Words)),
		strconv.Itoa(int(a.Total.WeightedWords)),
	}
}

// buildAnalysisCSVFilename produces a deterministic filename for the
// download. Project name is sanitized at the response-write site so
// this helper returns the unsanitized candidate (testable in
// isolation).
//
// Format: <project>-analysis-<yyyy-mm-dd>.csv. Falls back to
// `analysis-<projectID>-<date>.csv` when project name is empty
// (shouldn't happen in practice — schema requires a name).
func buildAnalysisCSVFilename(projectName string, projectID int64, now time.Time) string {
	date := now.UTC().Format("2006-01-02")
	if projectName == "" {
		return fmt.Sprintf("analysis-%d-%s.csv", projectID, date)
	}
	return fmt.Sprintf("%s-analysis-%s.csv", projectName, date)
}
