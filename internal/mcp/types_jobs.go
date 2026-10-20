package mcp

// JobList is one page of jobs. While HasMore is true the list is NOT
// complete: NextCursor, passed back as cursor, continues it.
type JobList struct {
	Jobs       []JobSummary `json:"jobs"`
	Count      int          `json:"count"`
	NextCursor string       `json:"next_cursor,omitempty"`
	HasMore    bool         `json:"has_more"`
}

type TaskList struct {
	Tasks []TaskSummary `json:"tasks"`
	Count int           `json:"count"`
	// Upcoming are the caller's steps that have not started yet: later steps
	// of a job whose active step is someone else's (or not yet reached). Never
	// null.
	Upcoming []UpcomingTask `json:"upcoming"`
}

type UpcomingTask struct {
	JobID          int64  `json:"job_id"`
	ProjectName    string `json:"project_name"`
	FileName       string `json:"file_name"`
	TargetLanguage string `json:"target_language"`
	StepName       string `json:"step_name"`
	// CurrentStep is the job's active step this one waits for; empty when no
	// step is active.
	CurrentStep string `json:"current_step"`
	Deadline    string `json:"deadline"`
}

type ListJobsIn struct {
	ProjectID   int64  `json:"project_id,omitempty" jsonschema:"the project whose jobs to list. OMIT to search across ALL projects in the organization."`
	Status      string `json:"status,omitempty" jsonschema:"filter by job status; one of created, in_progress, completed, failed. Omit for all."`
	OverdueOnly bool   `json:"overdue_only,omitempty" jsonschema:"return only jobs whose own deadline has passed and that are not completed. Use this (server-side) rather than fetching every page and filtering yourself."`
	AssigneeID  int64  `json:"assignee_id,omitempty" jsonschema:"only jobs with a workflow step (any status) assigned to this member, a user_id from openloc_list_members. Omit for everyone's jobs."`
	Cursor      string `json:"cursor,omitempty" jsonschema:"pagination cursor: pass the next_cursor from a previous response verbatim, with the same filters, to continue. Omit for the first page."`
	Limit       int    `json:"limit,omitempty" jsonschema:"max jobs per page (default 50, max 200)"`
}

type GetJobIn struct {
	JobID int64 `json:"job_id" jsonschema:"the job's numeric id. A job is one file translated into one target language."`
}

type ListSegmentsIn struct {
	JobID int64 `json:"job_id" jsonschema:"the job whose segments to list"`
	// Only three statuses exist. "untranslated" and
	// "pre_translated" are NOT statuses — they're derived from target_text and
	// origin. Saying so here matters: a model that filters on a non-existent
	// value gets zero rows and would report "no segments" as a finding.
	Status string `json:"status,omitempty" jsonschema:"filter by translation status; one of needs_review, approved, auto_approved. Omit for all. Note: an untranslated segment is status=needs_review with empty target_text; a machine-pretranslated one is status=needs_review with a non-empty origin. Approvals belong to the current workflow step: each new step puts approved segments back to needs_review (auto_approved stays), so an earlier step's result is in previous_step."`
	Origin string `json:"origin,omitempty" jsonschema:"filter by how the translation was produced; one of tm (translation memory), mt (machine translation), ape (automatic post-edit), file_import. Omit for all. An empty origin means a human typed it."`
	Query  string `json:"query,omitempty" jsonschema:"case-insensitive substring filter over source and target text"`
	Cursor int64  `json:"cursor,omitempty" jsonschema:"pagination cursor: pass the next_cursor from a previous response to continue. Omit for the first page."`
	Limit  int    `json:"limit,omitempty" jsonschema:"max segments to return (default 50, max 200)"`
}

type ListMyTasksIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"max tasks to return (default 50, max 200)"`
}

type AddCommentIn struct {
	TranslationID int64  `json:"translation_id" jsonschema:"the segment's translation_id, from openloc_list_segments"`
	Content       string `json:"content" jsonschema:"the comment text"`
}

type LockSegmentIn struct {
	TranslationID int64 `json:"translation_id" jsonschema:"the segment's translation_id, from openloc_list_segments"`
	// Default is to lock. Locking is managers only and unlocking is open, so
	// the direction is an explicit argument rather than two tools.
	Unlock bool `json:"unlock,omitempty" jsonschema:"set true to unlock instead of lock. Locking requires a project manager, admin or owner."`
}

type SaveDraftIn struct {
	TranslationID int64  `json:"translation_id" jsonschema:"the segment's translation_id, from openloc_list_segments"`
	Text          string `json:"text" jsonschema:"the translated text to save"`
	// Optimistic locking. Passing this is strongly encouraged: without it, a
	// concurrent human edit is silently overwritten instead of reported.
	ExpectedUpdatedAt string `json:"expected_updated_at,omitempty" jsonschema:"pass back the segment's updated_at value from openloc_list_segments VERBATIM. If it no longer matches, the write is refused as a conflict instead of overwriting a concurrent edit."`
}

type JobSummary struct {
	ID     int64 `json:"id"`
	FileID int64 `json:"file_id"`
	// ProjectID/ProjectName label every row so an org-wide listing
	// (project_id omitted) is self-describing — without them the model
	// re-enters a per-project lookup loop just to say which project an
	// overdue job belongs to.
	ProjectID       int64  `json:"project_id"`
	ProjectName     string `json:"project_name"`
	FileName        string `json:"file_name"`
	TargetLanguage  string `json:"target_language"`
	Status          string `json:"status"`
	CurrentStep     string `json:"current_step"`
	WordCount       int32  `json:"word_count"`
	SegmentCount    int32  `json:"segment_count"`
	TranslatedCount int32  `json:"translated_count"`
	ErrorMessage    string `json:"error_message"`
	// MTSkipReason says why the job's last pre-translation asked for MT and got
	// none: no_route (the MT router has no engine for the target language) or
	// no_provider (the AI engine it points to cannot translate the pair). A
	// pre-translation-only job stays open while it is set.
	MTSkipReason string `json:"mt_skip_reason,omitempty"`
	// Deadline is the job's EFFECTIVE deadline: the deadline of its active
	// workflow step. Empty means no deadline is set — there is
	// deliberately no fallback to the project's deadline, so "the project is due
	// Friday" does NOT make its jobs due Friday.
	Deadline string `json:"deadline"`
	// Steps are all the job's workflow steps in order, with their assignees,
	// whoever they are assigned to. Never null.
	Steps []JobStepBrief `json:"steps"`
}

// JobStepBrief is one workflow step of a job, as the job list shows it.
// openloc_list_job_steps has the ids the step tools take.
type JobStepBrief struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	AssigneeID   *int64 `json:"assignee_id"`
	AssigneeName string `json:"assignee_name"`
	Deadline     string `json:"deadline"`
}

type JobDetail struct {
	JobSummary
	SourceLanguage string `json:"source_language"`
	FileFormat     string `json:"file_format"`
	// ProjectDeadline is the project's own deadline, not the job's.
	// It is kept apart from `deadline` so a job never looks as if it inherits
	// its project's due date.
	ProjectDeadline string `json:"project_deadline"`
}

// LeverageBand is one TM-match tier's contribution to a job.
type LeverageBand struct {
	Band     string `json:"band"`
	Segments int32  `json:"segments"`
	Words    int32  `json:"words"`
}

type Leverage struct {
	JobID            int64          `json:"job_id"`
	Bands            []LeverageBand `json:"bands"`
	NeedsReviewWords int32          `json:"needs_review_words"`
	// Totals let a model tell "this job has no segments" apart from "this job's
	// segments are all new work". Both produce an unhelpful-looking band list,
	// and conflating them turns an empty file into a false finding about leverage.
	//
	// Note these bands are derived live from the current translations, not read
	// from a stored analysis run — so they always reflect the job as it is now.
	TotalSegments int32 `json:"total_segments"`
	TotalWords    int32 `json:"total_words"`
}

type Segment struct {
	TranslationID int64  `json:"translation_id"`
	SegmentID     int64  `json:"segment_id"`
	Position      int32  `json:"position"`
	SourceText    string `json:"source_text"`
	TargetText    string `json:"target_text"`
	Status        string `json:"status"`
	Origin        string `json:"origin"`
	TMMatch       *int16 `json:"tm_match_percent"`
	// UpdatedAt is the optimistic-locking version. It MUST be returned here:
	// without it a caller has no way to populate expected_updated_at on a write,
	// which silently turns concurrency protection off and reintroduces lost
	// updates. Caught by a test that reused a stale version and saw it accepted.
	UpdatedAt string `json:"updated_at"`
	// PreviousStep is how the last finished workflow step left the segment;
	// absent before any step has finished. Approvals start over at every
	// step, so this is where an earlier step's result lives.
	PreviousStep *StepOutcome `json:"previous_step,omitempty"`
}

// StepOutcome is how one workflow step round left a segment.
type StepOutcome struct {
	StepName  string  `json:"step_name"`
	StepOrder int32   `json:"step_order"`
	Round     int32   `json:"round"`
	EndReason string  `json:"end_reason" jsonschema:"completed, or rejected when the step was sent back"`
	Status    string  `json:"status" jsonschema:"needs_review, auto_approved or approved when the step ended"`
	AIOutcome *string `json:"ai_outcome,omitempty" jsonschema:"QE/APE verdict produced in this step: approved, ape_approved, ape_needs_review or rejected"`
	Edited    bool    `json:"edited" jsonschema:"a person changed the text during the step"`
}

// SegmentPage is a paginated slice of segments.
//
// Truncated + Total exist so the model is told when it is looking at a partial
// view and can narrow instead of drawing conclusions from page one of 400.
type SegmentPage struct {
	Segments   []Segment `json:"segments"`
	NextCursor int64     `json:"next_cursor"`
	HasMore    bool      `json:"has_more"`
	Total      int64     `json:"total"`
	Hint       string    `json:"hint,omitempty"`
}

type QASummary struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
	// Ignored findings are counted here instead of under their severity.
	Ignored int `json:"ignored"`
}

type QAIssue struct {
	TranslationID   int64  `json:"translation_id"`
	SegmentPosition int32  `json:"segment_position"`
	CheckType       string `json:"check_type"`
	Severity        string `json:"severity"`
	Message         string `json:"message"`
	// Ignored findings stay listed so nothing is hidden from the caller; they
	// do not block confirm and are excluded from the severity counts.
	Ignored   bool   `json:"ignored"`
	IgnoredBy string `json:"ignored_by,omitempty"`
	IgnoredAt string `json:"ignored_at,omitempty"`
}

// RunQAResult is what openloc_run_qa reports after a synchronous run.
type RunQAResult struct {
	JobID int64 `json:"job_id"`
	// Findings is the number of results written, ignored ones included.
	Findings int `json:"findings"`
	// NoRules is true when the QA profile has no enabled rule; the run cleared
	// every finding and wrote nothing.
	NoRules bool      `json:"no_rules"`
	Summary QASummary `json:"summary"`
}

type IgnoreQAFindingIn struct {
	TranslationID int64  `json:"translation_id" jsonschema:"the segment's translation_id, from openloc_get_qa_report or openloc_list_segments"`
	CheckType     string `json:"check_type" jsonschema:"the finding's check_type exactly as openloc_get_qa_report returned it (e.g. tag_missing, number_consistency, terminology)"`
	// One tool for both directions, like openloc_lock_segment's unlock.
	Unignore bool `json:"unignore,omitempty" jsonschema:"set true to remove an existing ignore instead of adding one"`
}

// QAFindingList is one segment's findings after an ignore/un-ignore, so the
// caller sees the resulting state without a second call.
type QAFindingList struct {
	TranslationID int64     `json:"translation_id"`
	Findings      []QAIssue `json:"findings"`
}

type QAReport struct {
	JobID   int64     `json:"job_id"`
	Summary QASummary `json:"summary"`
	Issues  []QAIssue `json:"issues"`
	HasMore bool      `json:"has_more"`
	Hint    string    `json:"hint,omitempty"`
}

type CommentResult struct {
	CommentID     int64  `json:"comment_id"`
	TranslationID int64  `json:"translation_id"`
	CreatedAt     string `json:"created_at"`
}

type LockResult struct {
	TranslationID int64 `json:"translation_id"`
	Locked        bool  `json:"locked"`
}

// SegmentWriteResult reports what a draft/confirm actually did.
//
// UpdatedAt is echoed back so a model doing several edits in a row can thread it
// into the next call's expected_updated_at without re-listing the segments.
type SegmentWriteResult struct {
	TranslationID int64  `json:"translation_id"`
	Status        string `json:"status"`
	UpdatedAt     string `json:"updated_at"`
	// TMEntriesWritten is 0 for drafts by design — only a human-confirmed
	// translation is written back to translation memory. Surfacing it makes that
	// difference visible rather than implicit.
	TMEntriesWritten int        `json:"tm_entries_written"`
	QASummary        *QASummary `json:"qa_summary,omitempty"`
}

type TaskSummary struct {
	JobID           int64  `json:"job_id"`
	ProjectName     string `json:"project_name"`
	FileName        string `json:"file_name"`
	TargetLanguage  string `json:"target_language"`
	Status          string `json:"status"`
	StepName        string `json:"step_name"`
	WordCount       int32  `json:"word_count"`
	TranslatedCount int32  `json:"translated_count"`
	TotalCount      int32  `json:"total_count"`
	Deadline        string `json:"deadline"`
}
