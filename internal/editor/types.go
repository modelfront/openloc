package editor

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// MaxScopeJobs caps the jobs one editor scope opens together.
const MaxScopeJobs = 500

// Repetition roles in Index.Repetition.
const (
	RepetitionNone   = 0 // the source text occurs once in the scope
	RepetitionSource = 1 // first occurrence of a source text that repeats
	RepetitionRepeat = 2 // a later occurrence of an earlier source text
)

// Index describes every segment of a scope without its text, one array per
// field, file after file in scope order, each file in document order. Files
// says which rows belong to which file. Positions keep the import numbering; a piece
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
	Files           []ScopeFile     `json:"files"`
}

// ScopeFile is one file of a scope: its job, the rows FirstRow up to
// FirstRow+RowCount-1 of the index, its step in progress, and whether the
// caller may edit it.
type ScopeFile struct {
	JobID        int64  `json:"job_id"`
	FileID       int64  `json:"file_id"`
	FileName     string `json:"file_name"`
	FileFormat   string `json:"file_format"`
	InlineHTML   bool   `json:"inline_html"`
	FirstRow     int    `json:"first_row"`
	RowCount     int    `json:"row_count"`
	StepName     string `json:"step_name"`
	StepAssignee *int64 `json:"step_assignee"`
	// StepID is the step in progress (a job step assignment), nil when none:
	// what a bulk Complete step sends back so a repeat never advances twice.
	StepID *int64 `json:"step_id"`
	// EditBlock is why the caller may not edit the file's rows, nil when
	// they may.
	EditBlock *EditBlock `json:"edit_block"`
	// HasPreviousStep: a step round of the file has finished, so its rows
	// carry a track-changes baseline.
	HasPreviousStep bool `json:"has_previous_step"`
}

// EditBlock is a task edit refusal, with the code the editor branches on.
type EditBlock struct {
	Code    string `json:"code"`
	Message string `json:"message"`
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

// Ref is the segment a cross-reference chip in file FileID points at.
type Ref struct {
	FileID    int64  `json:"file_id"`
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

// FuzzyMatch is a confirmed segment of the scope whose source is like the
// looked-up segment's: an internal fuzzy match. Percent is scored like a TM
// match, so the two read the same.
type FuzzyMatch struct {
	SegmentID      int64     `json:"segment_id"`
	FileID         int64     `json:"file_id"`
	TranslationID  int64     `json:"translation_id"`
	Percent        int16     `json:"percent"`
	SourceText     string    `json:"source_text"`
	SourceRichText *string   `json:"source_rich_text"`
	TargetText     string    `json:"target_text"`
	TargetRichText *string   `json:"target_rich_text"`
	UpdatedAt      time.Time `json:"updated_at"`
}
