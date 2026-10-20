package translation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// HistoryEvent is one row of the CAT editor's segment History section.
//
// Provenance fields are sparse by kind: `tm_name` + `tm_match_percent` on TM
// events, `mt_engine_name` on MT/APE events, the created_by trio on human and
// propagation events. `details` carries the per-kind payload documented on
// InsertTranslationHistory (predict_outcome, source, source_translation_id).
//
// `editing_time_ms` is deliberately absent: it exists for the per-step
// productivity report, and a per-segment timer on a colleague's work answers a
// management question this panel doesn't ask. `edit_distance` stays — how much
// of the machine output a human changed is provenance.
type HistoryEvent struct {
	ID             int64           `json:"id"`
	Kind           string          `json:"kind"`
	Text           *string         `json:"text"`
	RichText       *string         `json:"rich_text"`
	CreatedAt      string          `json:"created_at"`
	TMMatchPercent *int16          `json:"tm_match_percent"`
	EditDistance   *int32          `json:"edit_distance"`
	Details        json.RawMessage `json:"details"`
	CreatedBy      *int64          `json:"created_by"`
	CreatedByName  *string         `json:"created_by_name"`
	CreatedByEmail *string         `json:"created_by_email"`
	MTEngineName   *string         `json:"mt_engine_name"`
	TMName         *string         `json:"tm_name"`
	StepName       *string         `json:"step_name"`
	// The step assignment the event happened in: a person's saves and AI-step
	// results carry one, pre-translation and imports don't. The panel groups a
	// person's saves by it, since two adjacent steps can share a name.
	JobStepAssignmentID *int64 `json:"job_step_assignment_id"`
}

// HandleHistory serves GET /api/translations/{translationID}/history — every
// recorded change to one segment, newest first.
//
// Access: managers and owners/admins on any segment; linguists only on
// segments they're assigned, enforced by RequireLinguistTranslationAccess on
// the route. Same gate as per-segment comments and QA.
//
// Tenant isolation: translation_history carries no tenant_id, so the parent
// translation is resolved tenant-scoped first and a miss is a 404 — the route
// middleware is the second layer, not the only one.
func (h *Handler) HandleHistory(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "translationID")
	if err != nil {
		httputil.BadRequest(w, "invalid translation ID")
		return
	}
	events, err := h.History(r.Context(), auth.TenantIDFromContext(r.Context()), id)
	if httputil.DBError(w, err, "translation") {
		return
	}
	httputil.Data(w, events)
}

// History returns every recorded change to one segment, newest first.
// translation_history carries no tenant_id, so the parent translation is
// resolved tenant-scoped first; pgx.ErrNoRows means not in the tenant.
func (s *Service) History(ctx context.Context, tenantID, id int64) ([]HistoryEvent, error) {
	if _, err := s.queries.GetTranslationTenant(ctx, db.GetTranslationTenantParams{
		ID:       id,
		TenantID: tenantID,
	}); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListTranslationHistory(ctx, id)
	if err != nil {
		logging.Error(ctx, err, "segment history read failed", "segment-history-list-failed",
			logging.TranslationID(id))
		return nil, fmt.Errorf("list history: %w", err)
	}
	events := make([]HistoryEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, HistoryEvent{
			ID:                  row.ID,
			Kind:                string(row.Kind),
			Text:                row.Text,
			RichText:            row.RichText,
			CreatedAt:           row.CreatedAt.Time.UTC().Format(time.RFC3339),
			TMMatchPercent:      row.TmMatchPercent,
			EditDistance:        row.EditDistance,
			Details:             json.RawMessage(row.Details),
			CreatedBy:           row.CreatedBy,
			CreatedByName:       row.CreatedByName,
			CreatedByEmail:      row.CreatedByEmail,
			MTEngineName:        row.MtEngineName,
			TMName:              row.TmName,
			StepName:            row.StepName,
			JobStepAssignmentID: row.JobStepAssignmentID,
		})
	}
	return events, nil
}
