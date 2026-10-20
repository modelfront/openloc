package analysis

import (
	"bytes"
	"net/http"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// HandleExport implements GET /api/analyses/{analysisID}/export?format=csv|xlsx.
// Tenant-scoped through Get; same audience as viewing the analysis (owner,
// admin, project manager). The file is built in memory first, so a failure is
// a clean 500 rather than half a download.
func (h *Handler) HandleExport(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "analysisID")
	if err != nil {
		httputil.BadRequest(w, "invalid analysis ID")
		return
	}
	format, ok := parseExportFormat(r.URL.Query().Get("format"))
	if !ok {
		httputil.BadRequest(w, "format must be csv or xlsx")
		return
	}
	a, breakdowns, err := Get(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), id)
	if httputil.DBError(w, err, "analysis") {
		return
	}
	if a.Status != db.AnalysisStatusCompleted {
		httputil.Error(w, http.StatusConflict, "the analysis is not finished, so there is nothing to export yet")
		return
	}

	now := time.Now()
	rows := exportRows(breakdowns)
	var buf bytes.Buffer
	if format == exportXLSX {
		err = writeXLSX(&buf, a, rows, now)
	} else {
		err = writeCSV(&buf, rows)
	}
	if err != nil {
		logging.Error(r.Context(), err, "building analysis export failed", "analysis.export_failed", logging.AnalysisID(id))
		httputil.ServerError(w, "export", "analysis")
		return
	}

	w.Header().Set("Content-Type", format.contentType())
	httputil.SetAttachmentFilename(w, exportFilename(a.Name, format, now))
	if _, err := buf.WriteTo(w); err != nil {
		logging.Warn(r.Context(), err, "writing analysis export failed", "analysis.export_write_failed", logging.AnalysisID(id))
	}
}
