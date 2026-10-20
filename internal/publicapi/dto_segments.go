package publicapi

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/stepround"
)

// version formats an updated_at a client sends back as expected_updated_at.
// Microsecond precision is required: a value rounded to the second would
// never match the row and every write would report a conflict.
func version(t pgtype.Timestamptz) string {
	return t.Time.UTC().Format(time.RFC3339Nano)
}

// Segment is one source segment with its translation in the job's language.
// The id is the translation id, which every segment route takes.
type Segment struct {
	ID             int64   `json:"id"`
	Position       int32   `json:"position"`
	SourceText     string  `json:"source_text"`
	TargetText     *string `json:"target_text"`
	Status         string  `json:"status"`
	Origin         *string `json:"origin"`
	TMMatchPercent *int16  `json:"tm_match_percent"`
	UpdatedAt      string  `json:"updated_at"`
	// PreviousStep is how the last finished workflow step left the segment;
	// null before any step has finished.
	PreviousStep *StepOutcome `json:"previous_step"`
}

// StepOutcome is how one workflow step round left a segment.
type StepOutcome struct {
	StepName  string  `json:"step_name"`
	StepOrder int32   `json:"step_order"`
	StepType  string  `json:"step_type"`
	Round     int32   `json:"round"`
	EndReason string  `json:"end_reason"`
	Status    string  `json:"status"`
	AIOutcome *string `json:"ai_outcome"`
	Edited    bool    `json:"edited"`
	EndedAt   string  `json:"ended_at"`
}

func stepOutcomeDTO(e stepround.Entry) *StepOutcome {
	return &StepOutcome{
		StepName: e.StepName, StepOrder: e.StepOrder, StepType: e.StepType, Round: e.Round,
		EndReason: e.EndReason, Status: e.Status, AIOutcome: e.AIOutcome, Edited: e.Edited, EndedAt: e.EndedAt,
	}
}

func segmentDTO(r db.ListJobSegmentsPageRow) Segment {
	var origin *string
	if r.Origin.Valid {
		o := string(r.Origin.TranslationOrigin)
		origin = &o
	}
	return Segment{
		ID:             r.TranslationID,
		Position:       r.Position,
		SourceText:     r.SourceText,
		TargetText:     r.TargetText,
		Status:         string(r.Status),
		Origin:         origin,
		TMMatchPercent: r.TmMatchPercent,
		UpdatedAt:      version(r.UpdatedAt),
	}
}

// SegmentState is a segment's translation after a write.
type SegmentState struct {
	ID         int64   `json:"id"`
	TargetText *string `json:"target_text"`
	Status     string  `json:"status"`
	UpdatedAt  string  `json:"updated_at"`
}

func segmentStateDTO(t db.Translation) SegmentState {
	return SegmentState{ID: t.ID, TargetText: t.ActiveText, Status: string(t.Status), UpdatedAt: version(t.UpdatedAt)}
}

// Comment is a remark on a segment. A comment with no author_id was written
// by an AI step.
type Comment struct {
	ID         int64   `json:"id"`
	Content    string  `json:"content"`
	AuthorID   *int64  `json:"author_id"`
	AuthorName *string `json:"author_name"`
	CreatedAt  string  `json:"created_at"`
}

func commentDTO(r db.ListCommentsByTranslationRow) Comment {
	return Comment{ID: r.ID, Content: r.Content, AuthorID: r.UserID, AuthorName: r.AuthorName, CreatedAt: timestamp(r.CreatedAt)}
}
