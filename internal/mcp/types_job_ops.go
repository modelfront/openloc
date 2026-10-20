package mcp

import (
	"context"

	"openloc/internal/job"
)

// jobOpsTools is the Backend slice for the job actions, workflow steps, segments tools.
type jobOpsTools interface {
	// Jobs and workflow steps.
	ListJobSteps(ctx context.Context, in GetJobIn) (JobStepList, error)
	GetJobAnalysis(ctx context.Context, in GetJobIn) (JobAnalysis, error)
	PreviewMT(ctx context.Context, in SegmentSourceIn) (MTPreview, error)
	GetTMMatches(ctx context.Context, in SegmentSourceIn) (TMMatchList, error)
	ApplyTM(ctx context.Context, in GetJobIn) (PhaseRunResult, error)
	ApplyMT(ctx context.Context, in GetJobIn) (PhaseRunResult, error)
	AssignJob(ctx context.Context, in AssignJobIn) (AssignResult, error)
	AssignStep(ctx context.Context, in AssignStepIn) (AssignResult, error)
	SetStepDeadline(ctx context.Context, in SetStepDeadlineIn) (StepDeadlineResult, error)
	AdvanceStep(ctx context.Context, in AdvanceStepIn) (AdvanceStepResult, error)
	RejectStep(ctx context.Context, in RejectStepIn) (RejectStepResult, error)
	RerunAIStep(ctx context.Context, in GetJobIn) (AIStepRunResult, error)
	PreviewFindReplaceJob(ctx context.Context, in FindReplaceJobIn) (Preview, error)
	FindReplaceJob(ctx context.Context, in FindReplaceJobIn) (FindReplaceResult, error)
	ExportJob(ctx context.Context, in ExportJobIn) (ExportLink, error)
	PreviewClearJobTranslations(ctx context.Context, in ClearJobTranslationsIn) (Preview, error)
	ClearJobTranslations(ctx context.Context, in ClearJobTranslationsIn) (job.ClearResult, error)

	// Segments.
	GetSegmentHistory(ctx context.Context, in TranslationRefIn) (SegmentHistory, error)
	ListComments(ctx context.Context, in TranslationRefIn) (CommentList, error)
	SaveDraftsBatch(ctx context.Context, in SaveDraftsBatchIn) (DraftBatchResult, error)
	RunSegmentQA(ctx context.Context, in TranslationRefIn) (QAFindingList, error)
	PreviewClearSegment(ctx context.Context, in ClearSegmentIn) (Preview, error)
	ClearSegment(ctx context.Context, in ClearSegmentIn) (SegmentWriteResult, error)
	PreviewDeleteComment(ctx context.Context, in DeleteCommentIn) (Preview, error)
	DeleteComment(ctx context.Context, in DeleteCommentIn) (DeleteCommentResult, error)
	PreviewConfirmTranslation(ctx context.Context, in ConfirmTranslationIn) (Preview, error)
	ConfirmTranslation(ctx context.Context, in ConfirmTranslationIn) (SegmentWriteResult, error)
}

// --- Inputs -------------------------------------------------------------------

type TranslationRefIn struct {
	TranslationID int64 `json:"translation_id" jsonschema:"the segment's translation_id, from openloc_list_segments"`
}

// SegmentSourceIn names the source text to look up: a segment of the job, or
// free text.
type SegmentSourceIn struct {
	JobID         int64  `json:"job_id" jsonschema:"the job, from openloc_list_jobs or openloc_list_my_tasks"`
	TranslationID int64  `json:"translation_id,omitempty" jsonschema:"a segment of this job (translation_id from openloc_list_segments); its source text is used. Pass this OR source_text."`
	SourceText    string `json:"source_text,omitempty" jsonschema:"free source text to look up instead of a segment"`
}

type AssignJobIn struct {
	JobID  int64 `json:"job_id" jsonschema:"the job to assign, from openloc_list_jobs"`
	UserID int64 `json:"user_id" jsonschema:"the member to assign, from openloc_list_members"`
}

type AssignStepIn struct {
	JobID            int64 `json:"job_id" jsonschema:"the job, from openloc_list_jobs"`
	StepAssignmentID int64 `json:"step_assignment_id" jsonschema:"the step, from openloc_list_job_steps"`
	UserID           int64 `json:"user_id" jsonschema:"the member to assign, from openloc_list_members"`
}

type SetStepDeadlineIn struct {
	JobID            int64  `json:"job_id" jsonschema:"the job, from openloc_list_jobs"`
	StepAssignmentID int64  `json:"step_assignment_id" jsonschema:"the step, from openloc_list_job_steps"`
	Deadline         string `json:"deadline,omitempty" jsonschema:"the new deadline as an RFC3339 timestamp like 2026-10-01T17:00:00Z. Empty clears the deadline."`
}

type AdvanceStepIn struct {
	JobID int64 `json:"job_id" jsonschema:"the job whose current workflow step to complete"`
	// Mirrors the editor's completion dialog: warn, and allow an override.
	AllowUnconfirmed bool   `json:"allow_unconfirmed,omitempty" jsonschema:"set true only after the user agreed to complete the step although some segments are not confirmed. Without it the call is refused when unconfirmed segments remain, and the refusal says how many."`
	Comment          string `json:"comment,omitempty" jsonschema:"an optional note for whoever works the next step, shown to them in the editor and sent with their assignment notification; at most 4000 characters. Without it the next step gets no note."`
}

type RejectStepIn struct {
	JobID    int64  `json:"job_id" jsonschema:"the job whose current workflow step to reject"`
	Reason   string `json:"reason,omitempty" jsonschema:"why the step is sent back; shown to the reopened step's assignee"`
	ToStepID *int64 `json:"to_step_assignment_id,omitempty" jsonschema:"the step assignment to reopen, from openloc_list_job_steps: any earlier step except pre-translation. Omit to reopen the nearest earlier human or hybrid step"`
	RerunAI  bool   `json:"rerun_ai,omitempty" jsonschema:"run the reopened hybrid step's AI again on every translated, unlocked segment, people's text included. Reopening an AI step always runs it again. Needs a manager"`
}

type FindReplaceJobIn struct {
	Confirm
	JobID         int64  `json:"job_id" jsonschema:"the job to search, from openloc_list_jobs"`
	Find          string `json:"find" jsonschema:"the text to find in the job's translations (target side)"`
	Replace       string `json:"replace" jsonschema:"the replacement text; may be empty to delete the found text"`
	CaseSensitive bool   `json:"case_sensitive,omitempty" jsonschema:"match case exactly; default is case-insensitive"`
	IncludeLocked bool   `json:"include_locked,omitempty" jsonschema:"also search locked segments; they are replaced only when the user is an owner or admin, otherwise left as they are. Default false: locked segments are left out"`
}

type ExportJobIn struct {
	JobID  int64  `json:"job_id" jsonschema:"the job to export"`
	Format string `json:"format,omitempty" jsonschema:"source (default): the translated document in its original format; xliff: bilingual XLIFF 1.2 for another CAT tool"`
}

type ClearJobTranslationsIn struct {
	Confirm
	JobID int64  `json:"job_id" jsonschema:"the job to clear"`
	Scope string `json:"scope" jsonschema:"non_confirmed: only segments that are not confirmed; all: every segment. Locked segments are always kept."`
}

type DraftIn struct {
	TranslationID     int64  `json:"translation_id" jsonschema:"the segment's translation_id, from openloc_list_segments"`
	Text              string `json:"text" jsonschema:"the translated text to save"`
	ExpectedUpdatedAt string `json:"expected_updated_at,omitempty" jsonschema:"the segment's updated_at from openloc_list_segments, verbatim. A mismatch is reported as a conflict instead of overwriting a concurrent edit."`
}

type SaveDraftsBatchIn struct {
	JobID  int64     `json:"job_id" jsonschema:"the job the segments belong to"`
	Drafts []DraftIn `json:"drafts" jsonschema:"one entry per segment, at most 500"`
}

type ClearSegmentIn struct {
	Confirm
	TranslationID int64 `json:"translation_id" jsonschema:"the segment's translation_id, from openloc_list_segments"`
}

type DeleteCommentIn struct {
	Confirm
	TranslationID int64 `json:"translation_id" jsonschema:"the segment the comment is on"`
	CommentID     int64 `json:"comment_id" jsonschema:"the comment, from openloc_list_comments"`
}

type ConfirmTranslationIn struct {
	Confirm
	TranslationID     int64  `json:"translation_id" jsonschema:"the segment's translation_id, from openloc_list_segments"`
	Text              string `json:"text,omitempty" jsonschema:"the translation to confirm. Empty confirms the segment's current target text."`
	ExpectedUpdatedAt string `json:"expected_updated_at,omitempty" jsonschema:"the segment's updated_at from openloc_list_segments, verbatim. A mismatch is refused as a conflict."`
}

// --- Outputs ------------------------------------------------------------------

type JobStep struct {
	StepAssignmentID int64  `json:"step_assignment_id"`
	StepName         string `json:"step_name"`
	StepOrder        int32  `json:"step_order"`
	StepType         string `json:"step_type"`
	Status           string `json:"status"`
	AssignedTo       *int64 `json:"assigned_to"`
	AssigneeName     string `json:"assignee_name"`
	Deadline         string `json:"deadline"`
	RejectionReason  string `json:"rejection_reason,omitempty"`
	// HandoffNote is the comment the previous step left for this one.
	HandoffNote string `json:"handoff_note,omitempty"`
}

type JobStepList struct {
	JobID int64     `json:"job_id"`
	Steps []JobStep `json:"steps"`
}

type JobAnalysis struct {
	JobID    int64                 `json:"job_id"`
	Analysis job.AnalysisBreakdown `json:"analysis"`
}

type MTPreview struct {
	SourceText string `json:"source_text"`
	Text       string `json:"text"`
	Engine     string `json:"engine"`
	// Stored: the pipeline's own MT output on record, not a new MT call.
	Stored bool `json:"stored"`
}

type TMMatch struct {
	Score float64 `json:"score"`
	// Percent is the match % the editor shows (101 for an in-context match).
	Percent    int16  `json:"percent"`
	SourceText string `json:"source_text"`
	TargetText string `json:"target_text"`
	TMName     string `json:"tm_name"`
	TMID       int64  `json:"tm_id"`
}

type TMMatchList struct {
	SourceText string    `json:"source_text"`
	Matches    []TMMatch `json:"matches"`
	Count      int       `json:"count"`
}

type PhaseRunResult struct {
	JobID      int64  `json:"job_id"`
	Stopped    bool   `json:"stopped"`
	SkipReason string `json:"skip_reason,omitempty"`
	// Held: segments another user has open, skipped.
	Held int `json:"held"`
}

type AssignResult struct {
	JobID  int64 `json:"job_id"`
	UserID int64 `json:"user_id"`
}

type StepDeadlineResult struct {
	StepAssignmentID int64  `json:"step_assignment_id"`
	Deadline         string `json:"deadline"`
}

type AdvanceStepResult struct {
	JobID int64 `json:"job_id"`
	// Status is "advanced" (the next step is now active) or
	// "workflow_completed" (that was the last step).
	Status               string `json:"status"`
	NextStepAssignmentID int64  `json:"next_step_assignment_id,omitempty"`
	NextStepName         string `json:"next_step_name,omitempty"`
	UnconfirmedSegments  int    `json:"unconfirmed_segments"`
	// JobCompleted is true when the last step closed and the job was marked
	// completed (its human-confirmed segments written to translation memory).
	JobCompleted bool `json:"job_completed"`
	// Warning is set when the workflow finished but completing the job did not.
	Warning string `json:"warning,omitempty"`
}

type RejectStepResult struct {
	JobID                    int64 `json:"job_id"`
	ReopenedStepAssignmentID int64 `json:"reopened_step_assignment_id"`
	// RerunAI is true when the reopened step's AI was queued to run again.
	RerunAI bool `json:"rerun_ai"`
}

type AIStepRunResult struct {
	JobID        int64  `json:"job_id"`
	Stopped      bool   `json:"stopped"`
	Total        int    `json:"total"`
	AutoApproved int    `json:"auto_approved"`
	NeedsReview  int    `json:"needs_review"`
	SkipReason   string `json:"skip_reason,omitempty"`
}

type FindReplaceResult struct {
	JobID    int64 `json:"job_id"`
	Replaced int   `json:"replaced"`
	Segments int   `json:"segments"`
	// Matching segments left as they were.
	Locked int `json:"locked"`
	// Held: another user has the segment open in the editor.
	Held        int `json:"held"`
	SkippedTags int `json:"skipped_tags"`
	Conflicts   int `json:"conflicts"`
	// The write failed; the segment is unchanged.
	Failed int `json:"failed"`
}

type ExportLink struct {
	DownloadPath string `json:"download_path"`
	Filename     string `json:"filename"`
}

type HistoryEntry struct {
	Kind          string `json:"kind"`
	Text          string `json:"text,omitempty"`
	CreatedAt     string `json:"created_at"`
	CreatedByName string `json:"created_by_name,omitempty"`
	StepName      string `json:"step_name,omitempty"`
	TMName        string `json:"tm_name,omitempty"`
	TMMatch       *int16 `json:"tm_match_percent,omitempty"`
	MTEngineName  string `json:"mt_engine_name,omitempty"`
	EditDistance  *int32 `json:"edit_distance,omitempty"`
}

type SegmentHistory struct {
	TranslationID int64          `json:"translation_id"`
	Events        []HistoryEntry `json:"events"`
}

type Comment struct {
	CommentID int64  `json:"comment_id"`
	Content   string `json:"content"`
	// AuthorName is empty for an AI-authored comment.
	AuthorName string `json:"author_name"`
	IsAI       bool   `json:"is_ai"`
	CreatedAt  string `json:"created_at"`
}

type CommentList struct {
	TranslationID int64     `json:"translation_id"`
	Comments      []Comment `json:"comments"`
}

type DraftBatchResult struct {
	Applied   []SegmentWriteResult `json:"applied"`
	Conflicts []int64              `json:"conflicts"`
	Locked    []int64              `json:"locked"`
	// Held: another user has the segment open in the editor; try it later.
	Held     []int64 `json:"held"`
	Rejected []int64 `json:"rejected"`
}

type DeleteCommentResult struct {
	CommentID int64 `json:"comment_id"`
	Deleted   bool  `json:"deleted"`
}
