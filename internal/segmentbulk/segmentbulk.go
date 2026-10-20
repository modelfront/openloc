// Package segmentbulk holds what every bulk action on a CAT-editor segment
// selection shares: the request limit, how ids resolve to the job's rows, and
// the per-row result. The actions themselves live with the code that owns
// them (lock and clear in job, confirm in translation).
package segmentbulk

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// MaxIDs caps the translation ids one bulk request may carry. The editor sends
// larger selections in chunks of this size.
const MaxIDs = 500

// SkipReason says why a bulk action left a row alone.
type SkipReason string

const (
	// NotFound: the id is not a row of this job (wrong file, wrong target
	// language, another tenant, or deleted).
	NotFound SkipReason = "not_found"
	// Locked: the action never writes a locked row.
	Locked SkipReason = "locked"
	// Held: another user has the row open in the editor.
	Held SkipReason = "held"
	// Empty: there is no translation to act on.
	Empty SkipReason = "empty"
	// Unchanged: the row is already in the requested state.
	Unchanged SkipReason = "unchanged"
	// Conflict: the row changed between the read and the write.
	Conflict SkipReason = "conflict"
	// QAError: the confirm-time QA check found an error.
	QAError SkipReason = "qa_error"
	// TagNotInSource: the text holds a tag its source does not have, in a
	// format whose export writes text as is.
	TagNotInSource SkipReason = "tag_not_in_source"
	// AutoApproved: approved by quality prediction; a bulk confirm leaves it
	// for a person to confirm one by one.
	AutoApproved SkipReason = "auto_approved"
)

// Skip is one row a bulk action left alone, and why.
type Skip struct {
	ID     int64      `json:"id"`
	Reason SkipReason `json:"reason"`
}

// Result is the per-row outcome of a bulk action. Every requested id lands in
// exactly one of Applied or Skipped, in request order.
type Result struct {
	Applied          []int64 `json:"applied"`
	Skipped          []Skip  `json:"skipped"`
	WorkflowReopened bool    `json:"workflow_reopened,omitempty"`
	TaskReopened     bool    `json:"task_reopened,omitempty"`
}

// NewResult is an empty result whose lists encode as [] rather than null.
func NewResult() Result {
	return Result{Applied: []int64{}, Skipped: []Skip{}}
}

// Request is the body of every bulk segment route.
type Request struct {
	TranslationIDs []int64 `json:"translation_ids"`
}

// NormalizeIDs drops duplicates (keeping the first position) and rejects an
// empty, oversized or malformed id list.
func NormalizeIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, httputil.NewClientError("translation_ids must not be empty")
	}
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, httputil.NewClientError("translation_ids must be positive")
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) > MaxIDs {
		return nil, httputil.NewClientError(fmt.Sprintf("at most %d translation_ids per request", MaxIDs))
	}
	return out, nil
}

// Row is one of the job's rows as a bulk action sees it before writing.
type Row = db.ListJobTranslationsByIDsRow

// JobRows resolves ids to the job's rows — same file, same target language.
// The job must come from a tenant-scoped lookup; its file is then the tenant
// scope. An id missing from the map is not a row of this job. A row's
// HeldByOther is relative to actorID, the user the action runs as.
func JobRows(ctx context.Context, q db.Querier, job db.Job, actorID int64, ids []int64) (map[int64]Row, error) {
	rows, err := q.ListJobTranslationsByIDs(ctx, db.ListJobTranslationsByIDsParams{
		ActorID: &actorID, FileID: job.FileID, TargetLanguage: job.TargetLanguage, Ids: ids,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve translations: %w", err)
	}
	out := make(map[int64]Row, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// IsEmpty reports whether the row has no target text to act on.
func IsEmpty(r Row) bool {
	return r.ActiveText == nil || *r.ActiveText == ""
}

// Split sorts ids into applied (the written set) and skipped. A skipped id
// gets NotFound when it is not a row of the job, else reason(row).
func Split(ids, written []int64, rows map[int64]Row, reason func(Row) SkipReason) Result {
	done := make(map[int64]bool, len(written))
	for _, id := range written {
		done[id] = true
	}
	res := NewResult()
	for _, id := range ids {
		row, ok := rows[id]
		switch {
		case done[id]:
			res.Applied = append(res.Applied, id)
		case !ok:
			res.Skipped = append(res.Skipped, Skip{ID: id, Reason: NotFound})
		default:
			res.Skipped = append(res.Skipped, Skip{ID: id, Reason: reason(row)})
		}
	}
	return res
}
