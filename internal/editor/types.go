package editor

import "github.com/jackc/pgx/v5/pgtype"

// Repetition roles in Index.Repetition.
const (
	RepetitionNone   = 0 // the source text occurs once in the file
	RepetitionSource = 1 // first occurrence of a source text that repeats
	RepetitionRepeat = 2 // a later occurrence of an earlier source text
)

// Index describes every segment of a job without its text, one array per
// field, all in document order. Positions keep the import numbering; a piece
// of a split has the position of the row it came from and a sub-position from
// 1 up (segment number "position+1.sub").
type Index struct {
	SegmentIDs     []int64  `json:"segment_ids"`
	TranslationIDs []int64  `json:"translation_ids"`
	Positions      []int32  `json:"positions"`
	SubPositions   []int32  `json:"sub_positions"`
	WordCounts     []int32  `json:"word_counts"`
	Statuses       []string `json:"statuses"`
	HasTarget      []bool   `json:"has_target"`
	StepEdited     []bool   `json:"step_edited"`
	// Predicted: the translation carries an AI verdict.
	Predicted    []bool   `json:"predicted"`
	Origins      []string `json:"origins"`
	TMMatch      []*int16 `json:"tm_match"`
	Locked       []bool   `json:"locked"`
	InFlight     []string `json:"in_flight"`
	ConfirmedVia []string `json:"confirmed_via"`
	// QENoteHeads holds the first six characters of each QE note.
	QENoteHeads []string `json:"qe_note_heads"`
	// JoinsNext: the next row is a piece of the same split segment or, by
	// its key, a sentence of the same block in a format whose sentences
	// join, so the editor offers a join with it. The join itself checks.
	JoinsNext       []bool          `json:"joins_next"`
	Repetition      []int8          `json:"repetition"`
	RepetitionStats RepetitionStats `json:"repetition_stats"`
}

// RepetitionStats totals the later occurrences of repeated source texts.
type RepetitionStats struct {
	Segments   int `json:"segments"`
	Words      int `json:"words"`
	Characters int `json:"characters"`
}

// SearchResult lists the segments whose text matches, in document order.
type SearchResult struct {
	SegmentIDs []int64 `json:"segment_ids"`
}

// Ref is the segment a cross-reference chip points at.
type Ref struct {
	Key       string `json:"key"`
	SegmentID int64  `json:"segment_id"`
	Source    string `json:"source"`
	Target    string `json:"target"`
}

// WordCandidate is a translation whose target contains a word.
type WordCandidate struct {
	ID             int64              `json:"id"`
	SegmentID      int64              `json:"segment_id"`
	Locked         bool               `json:"locked"`
	ActiveText     *string            `json:"active_text"`
	ActiveRichText *string            `json:"active_rich_text"`
	UpdatedAt      pgtype.Timestamptz `json:"updated_at"`
}
