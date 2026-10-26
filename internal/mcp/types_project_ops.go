package mcp

import (
	"context"

	"openloc/internal/parsers/xlsx"
)

// projectOpsTools is the Backend slice for the projects, files, project settings, labels tools.
type projectOpsTools interface {
	// Projects
	GetProjectSettings(ctx context.Context, in ProjectRef) (ProjectSettingsOut, error)
	ListProjectEvents(ctx context.Context, in ListProjectEventsIn) (ProjectEventsOut, error)
	UpdateProject(ctx context.Context, in UpdateProjectIn) (ProjectDetail, error)
	SetProjectLabels(ctx context.Context, in SetProjectLabelsIn) (ProjectLabelsOut, error)
	UpdateProjectSettings(ctx context.Context, in UpdateProjectSettingsIn) (ProjectSettingsOut, error)
	SetProjectTMs(ctx context.Context, in SetProjectTMsIn) (ProjectTMsOut, error)
	SetProjectTermBases(ctx context.Context, in SetProjectTermBasesIn) (ProjectTermBasesOut, error)
	PreviewSetProjectWorkflowSteps(ctx context.Context, in SetProjectWorkflowStepsIn) (Preview, error)
	SetProjectWorkflowSteps(ctx context.Context, in SetProjectWorkflowStepsIn) (WorkflowStepsOut, error)
	PreviewApplyWorkflow(ctx context.Context, in ApplyWorkflowIn) (Preview, error)
	ApplyWorkflow(ctx context.Context, in ApplyWorkflowIn) (WorkflowStepsOut, error)
	PublishProject(ctx context.Context, in PublishProjectIn) (ProjectDetail, error)
	PretranslateProject(ctx context.Context, in ProjectRef) (PretranslateOut, error)
	PreviewClearProjectTranslations(ctx context.Context, in ClearProjectTranslationsIn) (Preview, error)
	ClearProjectTranslations(ctx context.Context, in ClearProjectTranslationsIn) (ClearTranslationsOut, error)

	// Files
	GetFile(ctx context.Context, in FileRef) (FileDetail, error)
	RenameFile(ctx context.Context, in RenameFileIn) (FileDetail, error)
	AttachUploadedFile(ctx context.Context, in AttachUploadedFileIn) (AttachedFileOut, error)
	UploadTextFile(ctx context.Context, in UploadTextFileIn) (AttachedFileOut, error)
	PreviewReimportXLIFF(ctx context.Context, in ReimportXLIFFIn) (Preview, error)
	ReimportXLIFF(ctx context.Context, in ReimportXLIFFIn) (ReimportOut, error)
	ExportFile(ctx context.Context, in ExportFileIn) (ExportFileOut, error)
	DownloadSource(ctx context.Context, in FileRef) (DownloadOut, error)

	// Labels
	ListLabels(ctx context.Context, in ListLabelsIn) (LabelsOut, error)
	CreateLabel(ctx context.Context, in CreateLabelIn) (LabelOut, error)
	AddLabelValue(ctx context.Context, in AddLabelValueIn) (LabelValueOut, error)
	PreviewDeleteLabel(ctx context.Context, in DeleteLabelIn) (Preview, error)
	DeleteLabel(ctx context.Context, in DeleteLabelIn) (DeletedOut, error)
	PreviewDeleteLabelValue(ctx context.Context, in DeleteLabelValueIn) (Preview, error)
	DeleteLabelValue(ctx context.Context, in DeleteLabelValueIn) (DeletedOut, error)
}

// --- Projects ---------------------------------------------------------------

type ProjectRef struct {
	ProjectID int64 `json:"project_id" jsonschema:"the project's numeric id, from openloc_list_projects"`
}

type TMProfileOut struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// UsageCount is how many projects and templates follow the profile.
	UsageCount int32 `json:"usage_count"`
}

type TMAssignmentOut struct {
	TMID       int64  `json:"tm_id"`
	Mode       string `json:"mode"`
	Priority   int32  `json:"priority"`
	PenaltyPct int32  `json:"penalty_pct"`
}

type TermAssignmentOut struct {
	TermBaseID     int64  `json:"term_base_id"`
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
	Mode           string `json:"mode"`
	Priority       int32  `json:"priority"`
}

type WorkflowStepOut struct {
	ID               int64  `json:"id"`
	Position         int32  `json:"position"`
	Name             string `json:"name"`
	StepType         string `json:"step_type"`
	RoleRequired     string `json:"role_required"`
	StepDefinitionID *int64 `json:"step_definition_id"`
	DefaultAssignee  *int64 `json:"default_assignee"`
	AIRouterID       *int64 `json:"ai_router_id"`
	Deadline         string `json:"deadline"`
}

type LabelPickOut struct {
	LabelID    int64  `json:"label_id"`
	LabelName  string `json:"label_name"`
	ValueID    int64  `json:"value_id"`
	LabelValue string `json:"value"`
}

type ProjectSettingsOut struct {
	ProjectID int64 `json:"project_id"`
	// Effective match scoring: the linked TM profile's when there is one.
	FuzzyThreshold        float32 `json:"fuzzy_threshold"`
	SublanguagePenaltyPct int32   `json:"sublanguage_penalty_pct"`
	NearExactPenaltyPct   int32   `json:"near_exact_penalty_pct"`
	TMOnUpload            bool    `json:"tm_on_upload"`
	MTOnUpload            bool    `json:"mt_on_upload"`
	AutoComplete          bool    `json:"auto_complete"`
	InlineHTML            bool    `json:"inline_html"`
	QAProfileID           *int64  `json:"qa_profile_id"`
	MTRouterID            *int64  `json:"mt_router_id"`
	MTRouterName          string  `json:"mt_router_name"`
	WorkflowID            *int64  `json:"workflow_id"`
	WorkflowName          string  `json:"workflow_name"`
	// TMProfile is set when the project follows a TM profile; its TMs and
	// scoring then come from the profile and cannot be edited on the project.
	TMProfile     *TMProfileOut       `json:"tm_profile"`
	TMs           []TMAssignmentOut   `json:"tms"`
	TermBases     []TermAssignmentOut `json:"term_bases"`
	WorkflowSteps []WorkflowStepOut   `json:"workflow_steps"`
	Labels        []LabelPickOut      `json:"labels"`
}

type ListProjectEventsIn struct {
	ProjectID int64  `json:"project_id" jsonschema:"the project whose history to list"`
	Type      string `json:"type,omitempty" jsonschema:"optional event type prefix: a category like file. or an exact type like file.uploaded"`
	ActorID   int64  `json:"actor_id,omitempty" jsonschema:"optional: only events by this user id, from openloc_list_members"`
	Cursor    int64  `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page; omit for the newest events"`
	Limit     int    `json:"limit,omitempty" jsonschema:"max events to return (default 50, max 200)"`
}

type ProjectEventOut struct {
	ID        int64  `json:"id"`
	EventType string `json:"event_type"`
	// ActorName is empty for system events.
	ActorName string         `json:"actor_name"`
	FileID    *int64         `json:"file_id"`
	JobID     *int64         `json:"job_id"`
	Details   map[string]any `json:"details"`
	CreatedAt string         `json:"created_at"`
}

type ProjectEventsOut struct {
	Events     []ProjectEventOut `json:"events"`
	HasMore    bool              `json:"has_more"`
	NextCursor int64             `json:"next_cursor"`
}

type UpdateProjectIn struct {
	ProjectID   int64   `json:"project_id" jsonschema:"the project to change"`
	Name        *string `json:"name,omitempty" jsonschema:"new name; omit to keep"`
	Description *string `json:"description,omitempty" jsonschema:"new description; empty string clears; omit to keep"`
	Status      *string `json:"status,omitempty" jsonschema:"new status: in_progress, on_hold or completed. A draft cannot change status here — publish it with openloc_publish_project."`
	Deadline    *string `json:"deadline,omitempty" jsonschema:"new ISO 8601 deadline, e.g. 2026-09-01T17:00:00Z; empty string clears; omit to keep. Does not change job deadlines."`
}

type SetProjectLabelsIn struct {
	ProjectID     int64   `json:"project_id" jsonschema:"the project to label"`
	LabelValueIDs []int64 `json:"label_value_ids" jsonschema:"the complete set of label value ids the project should carry, from openloc_list_labels — at most one value per label. Values not listed are removed; an empty list removes every label. Customer and Domain are labels too."`
}

type ProjectLabelsOut struct {
	Labels []LabelPickOut `json:"labels"`
}

type UpdateProjectSettingsIn struct {
	ProjectID             int64    `json:"project_id" jsonschema:"the project to configure"`
	TMOnUpload            *bool    `json:"tm_on_upload,omitempty" jsonschema:"pre-translate new uploads from translation memory"`
	MTOnUpload            *bool    `json:"mt_on_upload,omitempty" jsonschema:"pre-translate new uploads with machine translation"`
	AutoComplete          *bool    `json:"auto_complete,omitempty" jsonschema:"complete the project automatically when its last job completes"`
	InlineHTML            *bool    `json:"inline_html,omitempty" jsonschema:"strings in JSON, PO, TXT and Excel files are HTML: their tags become protected tags. Applies to files uploaded afterwards"`
	FuzzyThreshold        *float32 `json:"fuzzy_threshold,omitempty" jsonschema:"lowest TM match score to use, as a fraction: 0.75 is 75%. Refused while the project follows a TM profile."`
	SublanguagePenaltyPct *int32   `json:"sublanguage_penalty_pct,omitempty" jsonschema:"TM penalty for a sublanguage match, 0-100. Refused while the project follows a TM profile."`
	NearExactPenaltyPct   *int32   `json:"near_exact_penalty_pct,omitempty" jsonschema:"TM penalty for a near-exact match, 0-100. Refused while the project follows a TM profile."`
	QAProfileID           *int64   `json:"qa_profile_id,omitempty" jsonschema:"QA profile to use; 0 removes it"`
	MTRouterID            *int64   `json:"mt_router_id,omitempty" jsonschema:"MT router to use, from openloc_list_mt_routers; 0 removes it"`
	TMProfileID           *int64   `json:"tm_profile_id,omitempty" jsonschema:"link the project to this TM profile: its TMs and match scoring then come from the profile"`
}

type TMAssignmentIn struct {
	TMID       int64  `json:"tm_id" jsonschema:"a translation memory id"`
	Mode       string `json:"mode,omitempty" jsonschema:"read or read_write (default read_write)"`
	Priority   int32  `json:"priority,omitempty" jsonschema:"lower is consulted first"`
	PenaltyPct int32  `json:"penalty_pct,omitempty" jsonschema:"score penalty for every match from this TM, 0-100"`
}

type SetProjectTMsIn struct {
	ProjectID int64            `json:"project_id" jsonschema:"the project to configure"`
	TMs       []TMAssignmentIn `json:"tms" jsonschema:"the complete TM list; TMs not listed are removed. Refused while the project follows a TM profile."`
}

type ProjectTMsOut struct {
	TMs []TMAssignmentOut `json:"tms"`
}

type TermAssignmentIn struct {
	TermBaseID     int64  `json:"term_base_id" jsonschema:"a term base id"`
	SourceLanguage string `json:"source_language" jsonschema:"source language code the term base serves on this project"`
	TargetLanguage string `json:"target_language" jsonschema:"target language code"`
	Mode           string `json:"mode,omitempty" jsonschema:"read or read_write (default read)"`
	Priority       int32  `json:"priority,omitempty" jsonschema:"lower is consulted first"`
}

type SetProjectTermBasesIn struct {
	ProjectID int64              `json:"project_id" jsonschema:"the project to configure"`
	TermBases []TermAssignmentIn `json:"term_bases" jsonschema:"the complete term base list; term bases not listed are removed"`
}

type ProjectTermBasesOut struct {
	TermBases []TermAssignmentOut `json:"term_bases"`
}

type WorkflowStepIn struct {
	StepDefinitionID *int64  `json:"step_definition_id,omitempty" jsonschema:"library step for this position, from openloc_list_step_definitions. Set this or existing_step_id."`
	ExistingStepID   *int64  `json:"existing_step_id,omitempty" jsonschema:"keep the project's current step with this id unchanged at this position"`
	DefaultAssignee  *int64  `json:"default_assignee,omitempty" jsonschema:"user id who gets this step on new jobs, from openloc_list_members"`
	Deadline         *string `json:"deadline,omitempty" jsonschema:"ISO 8601 deadline for the step"`
	AIRouterID       *int64  `json:"ai_router_id,omitempty" jsonschema:"AI router for a hybrid or AI step on this project only"`
	// A pointer so "left out" and "empty" differ: left out keeps the step's
	// current per-language dates, an empty list clears them.
	LanguageDeadlines *[]StepLanguageDeadlineIn `json:"language_deadlines,omitempty" jsonschema:"per-target-language deadlines for this step, replacing its current ones; leave out to keep them, [] to clear them"`
}

type StepLanguageDeadlineIn struct {
	TargetLanguage string `json:"target_language" jsonschema:"one of the project's target languages"`
	Deadline       string `json:"deadline" jsonschema:"ISO 8601 deadline for this language"`
}

type SetProjectWorkflowStepsIn struct {
	ProjectID int64            `json:"project_id" jsonschema:"the project whose workflow to replace"`
	Steps     []WorkflowStepIn `json:"steps" jsonschema:"the complete workflow, first step first. A position a running job has reached must keep its step."`
	Confirm
}

type ApplyWorkflowIn struct {
	ProjectID  int64 `json:"project_id" jsonschema:"the project whose workflow to replace"`
	WorkflowID int64 `json:"workflow_id" jsonschema:"the library workflow to copy, from openloc_list_workflows"`
	Confirm
}

type WorkflowStepsOut struct {
	WorkflowID   *int64            `json:"workflow_id"`
	WorkflowName string            `json:"workflow_name"`
	Steps        []WorkflowStepOut `json:"steps"`
}

type PublishProjectIn struct {
	ProjectID int64 `json:"project_id" jsonschema:"the draft project to publish"`
	Analyze   bool  `json:"analyze,omitempty" jsonschema:"run the automatic analysis on the project's files once their jobs exist"`
}

type PretranslateOut struct {
	Queued  int `json:"queued"`
	Skipped int `json:"skipped"`
	Total   int `json:"total"`
}

type ClearProjectTranslationsIn struct {
	ProjectID int64  `json:"project_id" jsonschema:"the project whose translations to clear"`
	Scope     string `json:"scope" jsonschema:"non_confirmed clears everything not yet confirmed; all clears confirmed translations too. Locked segments are never cleared."`
	Confirm
}

type ClearTranslationsOut struct {
	Cleared int64 `json:"cleared"`
	// Held: segments another user has open, kept.
	Held int64 `json:"held"`
}

// --- Files ------------------------------------------------------------------

type FileRef struct {
	FileID int64 `json:"file_id" jsonschema:"the file's numeric id, from openloc_list_files or a job's file_id"`
}

type FileDetail struct {
	ID           int64  `json:"id"`
	ProjectID    int64  `json:"project_id"`
	Name         string `json:"name"`
	Format       string `json:"format"`
	SegmentCount int32  `json:"segment_count"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	// HasSource says the original upload is stored and can be downloaded.
	HasSource bool `json:"has_source"`
}

type RenameFileIn struct {
	FileID int64  `json:"file_id" jsonschema:"the file to rename"`
	Name   string `json:"name" jsonschema:"the new display name"`
}

type AttachUploadedFileIn struct {
	ProjectID int64 `json:"project_id" jsonschema:"the project to add the document to"`
	UploadRef
	Analyze bool `json:"analyze,omitempty" jsonschema:"run the automatic analysis once the jobs exist"`
	// XLSXLayout picks the sheets and columns of an .xlsx without language codes in row 1.
	XLSXLayout *xlsx.XLSXLayout `json:"xlsx_layout,omitempty" jsonschema:"for an .xlsx without language codes in row 1: the sheets to translate, each with whether row 1 is a header and the columns to translate (letters such as B; none for every text cell of the sheet); without it every text cell of every visible sheet is translated. Translations are written back into their own cells"`
}

type UploadTextFileIn struct {
	ProjectID int64  `json:"project_id" jsonschema:"the project to add the document to"`
	Filename  string `json:"filename" jsonschema:"the file's name with its extension, such as brochure.html; the extension decides the format"`
	Content   string `json:"content" jsonschema:"the document's full text, exactly as in the file; at most 1 MB"`
	Analyze   bool   `json:"analyze,omitempty" jsonschema:"run the automatic analysis once the jobs exist"`
}

type AttachedFileOut struct {
	File FileDetail `json:"file"`
	// Draft is true when the project is a draft: the file is stored and
	// segmented, and its jobs are created when the project is published.
	Draft    bool     `json:"draft"`
	Warnings []string `json:"warnings"`
}

type ReimportXLIFFIn struct {
	FileID         int64  `json:"file_id" jsonschema:"the project file the XLIFF was exported from"`
	TargetLanguage string `json:"target_language" jsonschema:"the target language to update; must be one of the project's target languages"`
	UploadRef
	Force bool `json:"force,omitempty" jsonschema:"also overwrite approved segments. Locked segments are never overwritten."`
	Confirm
}

type ReimportOut struct {
	Applied           bool     `json:"applied"`
	ForceRequired     bool     `json:"force_required"`
	Updated           int      `json:"updated"`
	SkippedLocked     int      `json:"skipped_locked"`
	SkippedHeld       int      `json:"skipped_held"`
	SkippedApproved   int      `json:"skipped_approved"`
	UntouchedNoChange int      `json:"untouched_no_change"`
	ExtraInUpload     int      `json:"extra_in_upload"`
	MissingFromUpload int      `json:"missing_from_upload"`
	SkippedLockedKeys []string `json:"skipped_locked_keys"`
	SkippedHeldKeys   []string `json:"skipped_held_keys"`
	// Lists are capped at 100 keys; the counts are the true totals.
	SkippedApprovedKeys   []string `json:"skipped_approved_keys"`
	ExtraInUploadKeys     []string `json:"extra_in_upload_keys"`
	MissingFromUploadKeys []string `json:"missing_from_upload_keys"`
}

type ExportFileIn struct {
	FileID         int64  `json:"file_id" jsonschema:"the file to export"`
	TargetLanguage string `json:"target_language" jsonschema:"which translation to export; one of the project's target languages"`
	Format         string `json:"format,omitempty" jsonschema:"source (default) for the translated document in its own format, or xliff for a bilingual XLIFF"`
	IncludeContent bool   `json:"include_content,omitempty" jsonschema:"also return the exported file's text in content; a text format only, at most 1 MB"`
}

// ExportFileOut is the download link, and the file's text when
// include_content asked for it; ContentError says why the text is missing.
type ExportFileOut struct {
	DownloadOut
	Content      string `json:"content,omitempty"`
	ContentError string `json:"content_error,omitempty"`
}

// DownloadOut is a link the user opens in a signed-in browser.
type DownloadOut struct {
	DownloadPath string `json:"download_path"`
	Filename     string `json:"filename"`
}

// --- Labels -----------------------------------------------------------------

type ListLabelsIn struct{}

type LabelValueOut struct {
	ID    int64  `json:"id"`
	Value string `json:"value"`
	// InUse: a project has chosen this value, so it cannot be deleted.
	InUse bool `json:"in_use"`
}

type LabelOut struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// System is true for the built-in Customer and Domain labels, which
	// cannot be deleted.
	System bool            `json:"system"`
	InUse  bool            `json:"in_use"`
	Values []LabelValueOut `json:"values"`
}

type LabelsOut struct {
	Labels []LabelOut `json:"labels"`
}

type CreateLabelIn struct {
	Name   string   `json:"name" jsonschema:"the label name, e.g. Country. It cannot be renamed later."`
	Values []string `json:"values,omitempty" jsonschema:"optional first allowed values (max 200)"`
}

type AddLabelValueIn struct {
	LabelID int64  `json:"label_id" jsonschema:"the label, from openloc_list_labels"`
	Value   string `json:"value" jsonschema:"the new allowed value"`
}

type DeleteLabelIn struct {
	LabelID int64 `json:"label_id" jsonschema:"the label to delete, from openloc_list_labels"`
	Confirm
}

type DeleteLabelValueIn struct {
	ValueID int64 `json:"value_id" jsonschema:"the label value to delete, from openloc_list_labels"`
	Confirm
}

type DeletedOut struct {
	Deleted bool `json:"deleted"`
}
