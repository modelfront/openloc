package qa

import (
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
)

// ResultDTO is the wire shape of one stored QA finding. It is the single
// response type for the per-translation and per-job QA endpoints, so the
// frontend merges both through one code path. Exported to TypeScript via tygo
// (web/src/types/qa.ts).
type ResultDTO struct {
	ID            int64  `json:"id"`
	TranslationID int64  `json:"translation_id"`
	CheckType     string `json:"check_type"`
	Severity      string `json:"severity"`
	Message       string `json:"message"`
	CreatedAt     string `json:"created_at"`
	// Ignored findings stay in the list, greyed out in the UI, and do not count
	// toward the severity summary or block confirm.
	Ignored       bool    `json:"ignored"`
	IgnoredByName *string `json:"ignored_by_name,omitempty"`
	IgnoredAt     *string `json:"ignored_at,omitempty"`
	// Set only on job-wide reports; zero for per-translation responses.
	SegmentID       int64 `json:"segment_id,omitempty"`
	SegmentPosition int32 `json:"segment_position,omitempty"`
}

// ReportSummary counts findings per bucket. Ignored findings leave their
// severity bucket and are counted in Ignored instead.
type ReportSummary struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
	Ignored int `json:"ignored"`
}

// ReportDTO is the response of GET /api/jobs/{id}/qa-report.
type ReportDTO struct {
	Summary ReportSummary `json:"summary"`
	Results []ResultDTO   `json:"results"`
}

func tsPtr(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	s := ts.Time.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	return &s
}

func tsString(ts pgtype.Timestamptz) string {
	if p := tsPtr(ts); p != nil {
		return *p
	}
	return ""
}

func translationRowsToDTO(rows []db.ListQAResultsForTranslationRow) []ResultDTO {
	out := make([]ResultDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, ResultDTO{
			ID:            r.ID,
			TranslationID: r.TranslationID,
			CheckType:     r.CheckType,
			Severity:      r.Severity,
			Message:       r.Message,
			CreatedAt:     tsString(r.CreatedAt),
			Ignored:       r.Ignored,
			IgnoredByName: r.IgnoredByName,
			IgnoredAt:     tsPtr(r.IgnoredAt),
		})
	}
	return out
}

func jobRowsToDTO(rows []db.ListQAResultsForJobRow) []ResultDTO {
	out := make([]ResultDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, ResultDTO{
			ID:              r.ID,
			TranslationID:   r.TranslationID,
			CheckType:       r.CheckType,
			Severity:        r.Severity,
			Message:         r.Message,
			CreatedAt:       tsString(r.CreatedAt),
			Ignored:         r.Ignored,
			IgnoredByName:   r.IgnoredByName,
			IgnoredAt:       tsPtr(r.IgnoredAt),
			SegmentID:       r.SegmentID,
			SegmentPosition: r.SegmentPosition,
		})
	}
	return out
}

// summaryFromBuckets folds CountQAIssuesByJob rows into a ReportSummary.
func summaryFromBuckets(rows []db.CountQAIssuesByJobRow) ReportSummary {
	var s ReportSummary
	for _, r := range rows {
		switch r.Bucket {
		case "error":
			s.Error = int(r.Count)
		case "warning":
			s.Warning = int(r.Count)
		case "info":
			s.Info = int(r.Count)
		case "ignored":
			s.Ignored = int(r.Count)
		}
	}
	return s
}
