package mcp

import (
	"context"

	"openloc/internal/llmdebug"
)

// llmLogTools read the captured AI provider calls (the /debug/llm feed).
// ListLLMCalls is the caller's own organization's calls; SupportListLLMCalls
// is every organization's, for the support team.
type llmLogTools interface {
	ListLLMCalls(ctx context.Context, in ListLLMCallsIn) (LLMCallList, error)
	SupportListLLMCalls(ctx context.Context, in ListLLMCallsIn) (LLMCallList, error)
}

// ListLLMCallsIn filters the LLM call log.
type ListLLMCallsIn struct {
	JobID     int64  `json:"job_id,omitempty" jsonschema:"only calls made for this job."`
	FileID    int64  `json:"file_id,omitempty" jsonschema:"only calls made for this file."`
	ProjectID int64  `json:"project_id,omitempty" jsonschema:"only calls made for the files of this project."`
	Kind      string `json:"kind,omitempty" jsonschema:"only calls of this kind: mt (AI translation), qe (quality estimation), ape (automatic post-editing) or extension (a call an extension made). Omit for every kind."`
	From      string `json:"from,omitempty" jsonschema:"only calls at or after this time, RFC 3339 (2026-10-30T09:00:00Z)."`
	To        string `json:"to,omitempty" jsonschema:"only calls before this time, RFC 3339."`
	GroupBy   string `json:"group_by,omitempty" jsonschema:"job or file: instead of the calls, return one row per job (or file) with its call, error and retry counts and its first and last call. Omit to list the calls."`
	Cursor    string `json:"cursor,omitempty" jsonschema:"pass the next_cursor from a previous response, with the same filters, to continue. Omit for the first page."`
	Limit     int    `json:"limit,omitempty" jsonschema:"max calls per page (default 10, max 20), or max groups (default and max 200)."`
}

// LLMCallList is one page of the LLM call log, or its groups.
type LLMCallList struct {
	Calls []LLMCall `json:"calls"`
	// Groups replaces Calls when group_by is set.
	Groups []llmdebug.Group `json:"groups"`
	Count  int              `json:"count"`
	// TotalGroups is every group the filters match; more than Count means the
	// list was cut at limit.
	TotalGroups int    `json:"total_groups,omitempty"`
	NextCursor  string `json:"next_cursor,omitempty"`
	HasMore     bool   `json:"has_more"`
	// Oldest is the time of the oldest call the log still holds, whatever the
	// filters: a job older than this has no calls left to read.
	Oldest string `json:"oldest,omitempty"`
	// Partial is true when the log could not be read in full in time: the
	// answer holds only the calls read so far, so a missing call may still exist.
	Partial bool `json:"partial"`
}

// LLMCall is one captured provider call.
type LLMCall struct {
	Time       string `json:"time"`
	Kind       string `json:"kind"`
	Provider   string `json:"provider"`
	Model      string `json:"model,omitempty"`
	Engine     string `json:"engine,omitempty"`
	Step       string `json:"step,omitempty"`
	JobID      int64  `json:"job_id,omitempty"`
	FileID     int64  `json:"file_id,omitempty"`
	Project    string `json:"project,omitempty"`
	Status     int    `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	Segments   int    `json:"segments,omitempty"`
	Retry      bool   `json:"retry,omitempty"`
	System     string `json:"system,omitempty"`
	User       string `json:"user,omitempty"`
	Response   string `json:"response"`
	// RequestBody is the raw body, only for calls without a user message (an
	// extension's call); for LLM calls it repeats system and user.
	RequestBody string `json:"request_body,omitempty"`
}
