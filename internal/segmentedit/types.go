package segmentedit

import "time"

// The wire shapes of /api/jobs/{jobID}/segments/split and /join, generated
// into web/src/types/segmentedit.ts.

// SplitRequest splits one row of the job at Offset, counted in UTF-16 code
// units of the row's plain source text. ExpectedUpdatedAt is the version the
// caller read; the editor always sends it, and a zero one skips the check.
type SplitRequest struct {
	TranslationID     int64     `json:"translation_id"`
	Offset            int       `json:"offset"`
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

// JoinRequest joins rows that follow each other in the job, in document
// order. Each row carries the version the editor read.
type JoinRequest struct {
	Rows []JoinRow `json:"rows"`
}

// JoinRow is one row of a join and the version the editor read.
type JoinRow struct {
	TranslationID     int64     `json:"translation_id"`
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

// Result lists the translations that now stand where the edited rows were,
// in document order.
type Result struct {
	TranslationIDs []int64 `json:"translation_ids"`
}
