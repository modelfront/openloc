package qa

import (
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/qa/numeric"
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
	// ProfileAssigned is false when the job's project has no QA profile, so a
	// QA run cannot start.
	ProfileAssigned bool `json:"profile_assigned"`
}

// Outcomes of a scope QA run on one job.
const (
	// ScopeRunDone: the job's rows were checked.
	ScopeRunDone = "done"
	// ScopeRunNoProfile: the project has no QA profile, so nothing ran.
	ScopeRunNoProfile = "no_profile"
	// ScopeRunFailed: the run failed; the other jobs still ran.
	ScopeRunFailed = "failed"
)

// ScopeRunJob is what a scope QA run did on one job.
type ScopeRunJob struct {
	JobID   int64  `json:"job_id"`
	Outcome string `json:"outcome"`
	// Issues is the number of findings written, ignored ones included.
	Issues int `json:"issues"`
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

// RegexRuleConfig is the stored config of a regex rule. Pattern is matched
// against the target, or against the source in source_must_not_match mode;
// SourcePattern is used only by source_match_target_not. Text is matched as the
// editor shows it: each inline tag is its label (<1>, </1>, {1}).
type RegexRuleConfig struct {
	Name            string   `json:"name"`
	Mode            string   `json:"mode"`
	Pattern         string   `json:"pattern"`
	SourcePattern   string   `json:"source_pattern,omitempty"`
	Message         string   `json:"message"`
	TargetLanguages []string `json:"target_languages"`
	IgnoreCase      bool     `json:"ignore_case"`
}

// NumberFormatConfig is the config of the number_format rule. Mode "allow"
// (the default) accepts the source number's separators or the target
// language's; "require" accepts only the target language's. While the rule is
// on, Languages replace a language's CLDR separators wherever the Numbers group
// reads a number; while it is off they are kept but not used.
type NumberFormatConfig struct {
	Mode      string                 `json:"mode"`
	Languages []NumberFormatLanguage `json:"languages,omitempty"`

	separators map[string]numeric.Separators // normalized code → separators
}

// NumberFormatLanguage is how one language writes numbers. A bare language
// ("es") covers all its locales, a locale ("es-mx") only itself.
type NumberFormatLanguage struct {
	Language string `json:"language"`
	Decimal  string `json:"decimal"` // "." or ","
	Group    string `json:"group"`   // ",", ".", " ", "'" or "" for none
}

// PlaceablesConfig picks which placeables the placeables rule compares. A
// field left out of a stored config is on.
type PlaceablesConfig struct {
	URLsEmails bool `json:"urls_emails"`
	Codes      bool `json:"codes"`
	Variables  bool `json:"variables"`
}
