package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
)

// Productivity reads shared by the REST handlers and the MCP tools. Project lookups are tenant-scoped; a foreign project is pgx.ErrNoRows.

// ProductivityEntry is one (user, target_language) row of a productivity
// report.
type ProductivityEntry struct {
	UserID         *int64  `json:"user_id"`
	UserName       string  `json:"user_name"`
	TargetLanguage string  `json:"target_language,omitempty"`
	SegmentsEdited int32   `json:"segments_edited"`
	WordsEdited    int32   `json:"words_edited"`
	TotalTimeMs    int64   `json:"total_editing_time_ms"`
	WordsPerHour   float64 `json:"words_per_hour"`
	AvgEditDist    float64 `json:"avg_edit_distance"`
	AvgTER         float64 `json:"avg_ter,omitempty"`
}

// StepProductivityEntry is one (step, user, target_language) row of the
// per-step report: every confirm is credited to the step it
// happened in.
type StepProductivityEntry struct {
	StepAssignmentID int64   `json:"step_assignment_id"`
	StepName         string  `json:"step_name"`
	StepOrder        int32   `json:"step_order"`
	UserID           *int64  `json:"user_id"`
	UserName         string  `json:"user_name"`
	TargetLanguage   string  `json:"target_language,omitempty"`
	SegmentsEdited   int32   `json:"segments_edited"`
	WordsEdited      int32   `json:"words_edited"`
	TotalTimeMs      int64   `json:"total_editing_time_ms"`
	WordsPerHour     float64 `json:"words_per_hour"`
	AvgEditDist      float64 `json:"avg_edit_distance"`
}

// OrgProductivity is org-wide productivity per user for edits in [from, to).
func OrgProductivity(ctx context.Context, q db.Querier, tenantID int64, from, to time.Time) ([]ProductivityEntry, error) {
	rows, err := q.GetProductivityByTenant(ctx, db.GetProductivityByTenantParams{
		TenantID: tenantID,
		FromDate: pgtype.Timestamptz{Time: from, Valid: true},
		ToDate:   pgtype.Timestamptz{Time: to, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("org productivity: %w", err)
	}
	entries := make([]ProductivityEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, ProductivityEntry{
			UserID:         r.UserID,
			UserName:       r.UserName,
			SegmentsEdited: r.SegmentsEdited,
			WordsEdited:    r.WordsEdited,
			TotalTimeMs:    r.TotalEditingTimeMs,
			WordsPerHour:   wordsPerHour(r.WordsEdited, r.TotalEditingTimeMs),
			AvgEditDist:    r.AvgEditDistance,
		})
	}
	return entries, nil
}

// ProjectProductivity is per-translator productivity for one project,
// crediting the last editor of each segment.
func ProjectProductivity(ctx context.Context, q db.Querier, tenantID, projectID int64) ([]ProductivityEntry, error) {
	// GetProductivityByProject filters only by project_id; this lookup is the
	// tenant gate.
	if _, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID}); err != nil {
		return nil, err
	}
	rows, err := q.GetProductivityByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("project productivity: %w", err)
	}
	entries := make([]ProductivityEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, ProductivityEntry{
			UserID:         row.UserID,
			UserName:       row.UserName,
			TargetLanguage: row.TargetLanguage,
			SegmentsEdited: row.SegmentsEdited,
			WordsEdited:    row.WordsEdited,
			TotalTimeMs:    row.TotalEditingTimeMs,
			WordsPerHour:   wordsPerHour(row.WordsEdited, row.TotalEditingTimeMs),
			AvgEditDist:    row.AvgEditDistance,
			AvgTER:         row.AvgTer,
		})
	}
	return entries, nil
}

// ProjectStepProductivity is per-step, per-translator productivity for one
// project, read from translation_history.
func ProjectStepProductivity(ctx context.Context, q db.Querier, tenantID, projectID int64) ([]StepProductivityEntry, error) {
	// GetStepProductivityByProject filters only by project_id; this lookup is
	// the tenant gate.
	if _, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID}); err != nil {
		return nil, err
	}
	rows, err := q.GetStepProductivityByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("step productivity: %w", err)
	}
	entries := make([]StepProductivityEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, StepProductivityEntry{
			StepAssignmentID: row.StepAssignmentID,
			StepName:         row.StepName,
			StepOrder:        row.StepOrder,
			UserID:           row.UserID,
			UserName:         row.UserName,
			TargetLanguage:   row.TargetLanguage,
			SegmentsEdited:   row.SegmentsEdited,
			WordsEdited:      row.WordsEdited,
			TotalTimeMs:      row.TotalEditingTimeMs,
			WordsPerHour:     wordsPerHour(row.WordsEdited, row.TotalEditingTimeMs),
			AvgEditDist:      row.AvgEditDistance,
		})
	}
	return entries, nil
}
