package mcp

import (
	"context"

	"openloc/internal/analytics"
	"openloc/internal/job"
	"openloc/internal/notification"
)

// insightTools is the Backend slice for the analysis, analytics, dashboards, team, notifications tools.
type insightTools interface {
	// Analysis and analytics.
	GetProjectAnalysis(ctx context.Context, in GetProjectAnalysisIn) (job.ProjectAnalysis, error)
	ListSavedAnalyses(ctx context.Context, in ListSavedAnalysesIn) (SavedAnalysisList, error)
	SaveAnalysis(ctx context.Context, in SaveAnalysisIn) (SavedAnalysis, error)
	PreviewDeleteSavedAnalysis(ctx context.Context, in DeleteSavedAnalysisIn) (Preview, error)
	DeleteSavedAnalysis(ctx context.Context, in DeleteSavedAnalysisIn) (SavedAnalysisDeleted, error)
	GetProductivity(ctx context.Context, in GetProductivityIn) (ProductivityReport, error)

	// Dashboards.
	ListDashboards(ctx context.Context, in ListDashboardsIn) (DashboardList, error)
	GetDashboard(ctx context.Context, in GetDashboardIn) (DashboardDetail, error)
	PreviewUpdateChart(ctx context.Context, in UpdateChartIn) (Preview, error)
	UpdateChart(ctx context.Context, in UpdateChartIn) (DashboardChart, error)
	PreviewDeleteChart(ctx context.Context, in DeleteChartIn) (Preview, error)
	DeleteChart(ctx context.Context, in DeleteChartIn) (ChartDeleted, error)

	// Team.
	GetMe(ctx context.Context, in GetMeIn) (MeProfile, error)
	ListInvites(ctx context.Context, in ListInvitesIn) (InviteList, error)
	PreviewInviteMember(ctx context.Context, in InviteMemberIn) (Preview, error)
	InviteMember(ctx context.Context, in InviteMemberIn) (InviteView, error)
	SetMemberLanguagePairs(ctx context.Context, in SetMemberLanguagePairsIn) (MemberLanguagePairs, error)
	PreviewChangeMemberRole(ctx context.Context, in ChangeMemberRoleIn) (Preview, error)
	ChangeMemberRole(ctx context.Context, in ChangeMemberRoleIn) (MemberRoleChanged, error)
	PreviewRemoveMember(ctx context.Context, in RemoveMemberIn) (Preview, error)
	RemoveMember(ctx context.Context, in RemoveMemberIn) (MemberRemoved, error)
	PreviewRevokeInvite(ctx context.Context, in RevokeInviteIn) (Preview, error)
	RevokeInvite(ctx context.Context, in RevokeInviteIn) (InviteRevoked, error)

	// Notifications (the caller's own; every role).
	ListNotifications(ctx context.Context, in ListNotificationsIn) (NotificationList, error)
	GetNotificationPreferences(ctx context.Context, in GetNotificationPreferencesIn) (notification.Preferences, error)
	MarkNotificationsRead(ctx context.Context, in MarkNotificationsReadIn) (NotificationsMarked, error)
	UpdateNotificationPreferences(ctx context.Context, in UpdateNotificationPreferencesIn) (notification.Preferences, error)

	// Project-manager overviews.
	GetAttentionSummary(ctx context.Context, in GetAttentionSummaryIn) (AttentionSummary, error)
	GetProgress(ctx context.Context, in GetProgressIn) (ProgressReport, error)
}

// --- Analysis and analytics -------------------------------------------------

type GetProjectAnalysisIn struct {
	ProjectID int64 `json:"project_id" jsonschema:"the project to analyse, from openloc_list_projects"`
}

type ListSavedAnalysesIn struct {
	ProjectID  int64 `json:"project_id" jsonschema:"the project whose saved analyses to list, from openloc_list_projects"`
	AnalysisID int64 `json:"analysis_id,omitempty" jsonschema:"optional: one saved analysis id from this list. When set, the result holds only that analysis, with its per-job band breakdown."`
}

// SavedAnalysis is one saved analysis run. Status is pending, running,
// completed or failed; a pending one fills in when the worker finishes.
type SavedAnalysis struct {
	ID           int64   `json:"id"`
	ProjectID    int64   `json:"project_id"`
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	JobIDs       []int64 `json:"job_ids"`
	IsAuto       bool    `json:"is_auto"`
	ErrorMessage string  `json:"error_message"`
	CreatedAt    string  `json:"created_at"`
	CompletedAt  string  `json:"completed_at"`
	// Breakdowns is set only when one analysis_id was asked for.
	Breakdowns []SavedAnalysisBreakdown `json:"breakdowns,omitempty"`
}

// SavedAnalysisBreakdown is one job's words per match band in a saved run.
type SavedAnalysisBreakdown struct {
	JobID          *int64 `json:"job_id"`
	JobName        string `json:"job_name"`
	TargetLanguage string `json:"target_language"`
	Tm101Words     int32  `json:"tm_101_words"`
	Tm100Words     int32  `json:"tm_100_words"`
	Tm8599Words    int32  `json:"tm_85_99_words"`
	Tm6584Words    int32  `json:"tm_65_84_words"`
	NewWords       int32  `json:"new_words"`
	RepeatedWords  int32  `json:"repetitions_words"`
}

type SavedAnalysisList struct {
	Analyses []SavedAnalysis `json:"analyses"`
	Count    int             `json:"count"`
}

type SaveAnalysisIn struct {
	ProjectID int64   `json:"project_id" jsonschema:"the project to analyse, from openloc_list_projects"`
	JobIDs    []int64 `json:"job_ids" jsonschema:"the jobs to include, from openloc_list_jobs for this project. Every job must belong to the project, and pre-translation must have finished for each."`
	Name      string  `json:"name,omitempty" jsonschema:"optional name for the run. Defaults to 'Analysis' plus the time."`
}

type DeleteSavedAnalysisIn struct {
	Confirm
	AnalysisID int64 `json:"analysis_id" jsonschema:"the saved analysis to delete, from openloc_list_saved_analyses"`
}

type SavedAnalysisDeleted struct {
	AnalysisID int64 `json:"analysis_id"`
	Deleted    bool  `json:"deleted"`
}

type GetProductivityIn struct {
	Scope     string `json:"scope" jsonschema:"org (every translator in the organization, for a date range), project (per translator and target language in one project, crediting the last editor) or project_by_step (per workflow step, translator and language in one project, crediting each confirm to its step)"`
	ProjectID int64  `json:"project_id,omitempty" jsonschema:"required for scope project and project_by_step, from openloc_list_projects"`
	From      string `json:"from,omitempty" jsonschema:"scope org only: start date YYYY-MM-DD, inclusive. Default 30 days ago."`
	To        string `json:"to,omitempty" jsonschema:"scope org only: end date YYYY-MM-DD, exclusive. Default tomorrow."`
}

// ProductivityReport carries the rows for the scope asked; the other list is
// empty.
type ProductivityReport struct {
	Scope string                            `json:"scope"`
	From  string                            `json:"from,omitempty"`
	To    string                            `json:"to,omitempty"`
	Rows  []analytics.ProductivityEntry     `json:"rows"`
	Steps []analytics.StepProductivityEntry `json:"steps"`
}

// --- Dashboards -------------------------------------------------------------

type ListDashboardsIn struct{}

type DashboardSummary struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Personal   bool   `json:"personal"`
	ChartCount int64  `json:"chart_count"`
	CreatedAt  string `json:"created_at"`
}

type DashboardList struct {
	Dashboards []DashboardSummary `json:"dashboards"`
	Count      int                `json:"count"`
}

type GetDashboardIn struct {
	DashboardID int64 `json:"dashboard_id" jsonschema:"the dashboard, from openloc_list_dashboards"`
}

// DashboardChart is a saved chart: what it measures and how it is drawn. It
// holds no data — run openloc_query_metrics with the definition for numbers.
type DashboardChart struct {
	ID          int64             `json:"id"`
	DashboardID int64             `json:"dashboard_id"`
	Title       string            `json:"title"`
	Measure     string            `json:"measure"`
	Dimensions  []string          `json:"dimensions"`
	Filters     map[string]string `json:"filters"`
	ChartType   string            `json:"chart_type"`
	XKey        string            `json:"x_key"`
	Series      []string          `json:"series"`
	SeriesKey   string            `json:"series_key,omitempty"`
	Controls    []string          `json:"controls"`
	Position    int32             `json:"position"`
}

type DashboardDetail struct {
	ID        int64            `json:"id"`
	Name      string           `json:"name"`
	Personal  bool             `json:"personal"`
	CanEdit   bool             `json:"can_edit"`
	CreatedAt string           `json:"created_at"`
	Charts    []DashboardChart `json:"charts"`
}

// UpdateChartIn replaces a chart's definition and drawing; send every field,
// changed or not. Filter values set on the chart in the app stay.
type UpdateChartIn struct {
	Confirm
	ChartID    int64    `json:"chart_id" jsonschema:"the chart to change, from openloc_get_dashboard"`
	Title      string   `json:"title" jsonschema:"short chart title shown on the card"`
	Measure    string   `json:"measure" jsonschema:"same vocabulary as openloc_query_metrics"`
	Dimensions []string `json:"dimensions,omitempty" jsonschema:"group-by keys, max 3, same vocabulary as openloc_query_metrics. For a time series include exactly one of day, week, month."`
	ChartType  string   `json:"chart_type" jsonschema:"one of line, bar, area, pie"`
	XKey       string   `json:"x_key" jsonschema:"which dimension is the x axis (or pie label); must be one of dimensions"`
	Series     []string `json:"series" jsonschema:"which value columns to plot, 1-5. With series_key set, give exactly one."`
	SeriesKey  string   `json:"series_key,omitempty" jsonschema:"optional: a dimension whose values each become a band; must be one of dimensions and differ from x_key"`
	Controls   []string `json:"controls,omitempty" jsonschema:"the chart's own filter dropdowns, on top of the dashboard's. Leave out to keep the current ones."`
}

type DeleteChartIn struct {
	Confirm
	ChartID int64 `json:"chart_id" jsonschema:"the chart to delete, from openloc_get_dashboard"`
}

type ChartDeleted struct {
	ChartID int64 `json:"chart_id"`
	Deleted bool  `json:"deleted"`
}

// --- Team -------------------------------------------------------------------

type GetMeIn struct{}

// MemberLanguagePair is one source → target a person is qualified for.
type MemberLanguagePair struct {
	SourceLang string `json:"source_lang"`
	TargetLang string `json:"target_lang"`
}

type MeProfile struct {
	// ID is opaque and stable per person per organization: one connection
	// reaches one organization, so the same person in two is two accounts.
	ID            string               `json:"id"`
	Nickname      string               `json:"nickname"`
	UserID        int64                `json:"user_id"`
	Name          string               `json:"name"`
	Email         string               `json:"email"`
	Role          string               `json:"role"`
	TenantID      int64                `json:"tenant_id"`
	TenantName    string               `json:"tenant_name"`
	LanguagePairs []MemberLanguagePair `json:"language_pairs"`
}

type ListInvitesIn struct{}

// InviteView is an invite without its secret link token. Status is pending,
// accepted or expired.
type InviteView struct {
	ID            int64  `json:"id"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	Status        string `json:"status"`
	InvitedByName string `json:"invited_by_name"`
	CreatedAt     string `json:"created_at"`
	ExpiresAt     string `json:"expires_at"`
	AcceptedAt    string `json:"accepted_at"`
}

type InviteList struct {
	Invites []InviteView `json:"invites"`
	Count   int          `json:"count"`
}

type InviteMemberIn struct {
	Confirm
	Email string `json:"email" jsonschema:"the person's email address. They receive a link that is valid for 7 days."`
	Role  string `json:"role,omitempty" jsonschema:"linguist (default), project_manager or admin. A project manager may only invite linguists. Owners are never invited."`
}

type SetMemberLanguagePairsIn struct {
	UserID int64                `json:"user_id" jsonschema:"the member, from openloc_list_members"`
	Pairs  []MemberLanguagePair `json:"pairs" jsonschema:"the member's COMPLETE list of language pairs; it replaces what they have now. Send an empty list to clear. Language codes like en, de-DE."`
}

type MemberLanguagePairs struct {
	UserID int64                `json:"user_id"`
	Pairs  []MemberLanguagePair `json:"pairs"`
}

type ChangeMemberRoleIn struct {
	Confirm
	UserID int64  `json:"user_id" jsonschema:"the member, from openloc_list_members. Cannot be yourself."`
	Role   string `json:"role" jsonschema:"the new role: owner, admin, project_manager or linguist. Only an owner can grant owner or change an owner."`
}

type MemberRoleChanged struct {
	UserID       int64  `json:"user_id"`
	PreviousRole string `json:"previous_role"`
	Role         string `json:"role"`
}

type RemoveMemberIn struct {
	Confirm
	UserID int64 `json:"user_id" jsonschema:"the member to remove from this organization, from openloc_list_members. Cannot be yourself."`
}

type MemberRemoved struct {
	UserID  int64 `json:"user_id"`
	Removed bool  `json:"removed"`
}

type RevokeInviteIn struct {
	Confirm
	InviteID int64 `json:"invite_id" jsonschema:"the invite, from openloc_list_invites"`
}

type InviteRevoked struct {
	InviteID int64 `json:"invite_id"`
	Revoked  bool  `json:"revoked"`
}

// --- Notifications ----------------------------------------------------------

type ListNotificationsIn struct {
	UnreadOnly bool  `json:"unread_only,omitempty" jsonschema:"only unread notifications"`
	Cursor     int64 `json:"cursor,omitempty" jsonschema:"pass next_cursor from the previous page to continue. Omit for the first page."`
	Limit      int   `json:"limit,omitempty" jsonschema:"max notifications to return (default 20, max 100)"`
}

// NotificationView is one notification. ReferenceType/ReferenceID name what
// it is about (for example job and a job id for openloc_get_job).
type NotificationView struct {
	ID            int64  `json:"id"`
	EventType     string `json:"event_type"`
	Subject       string `json:"subject"`
	Preview       string `json:"preview"`
	ReferenceType string `json:"reference_type"`
	ReferenceID   int64  `json:"reference_id"`
	Read          bool   `json:"read"`
	CreatedAt     string `json:"created_at"`
}

type NotificationList struct {
	Notifications []NotificationView `json:"notifications"`
	Count         int                `json:"count"`
	UnreadCount   int64              `json:"unread_count"`
	NextCursor    int64              `json:"next_cursor"`
	HasMore       bool               `json:"has_more"`
}

type GetNotificationPreferencesIn struct{}

type MarkNotificationsReadIn struct {
	IDs []int64 `json:"ids,omitempty" jsonschema:"notification ids from openloc_list_notifications. Omit and set all=true to mark everything read."`
	All bool    `json:"all,omitempty" jsonschema:"mark every unread notification read"`
}

type NotificationsMarked struct {
	UnreadCount int64 `json:"unread_count"`
}

// UpdateNotificationPreferencesIn changes only the switches that are sent.
type UpdateNotificationPreferencesIn struct {
	EmailAssignment   *bool `json:"email_assignment,omitempty" jsonschema:"email me when work is assigned to me"`
	EmailRejection    *bool `json:"email_rejection,omitempty" jsonschema:"email me when my work is sent back"`
	EmailCompletion   *bool `json:"email_completion,omitempty" jsonschema:"email me when work I follow is completed"`
	EmailDeadline     *bool `json:"email_deadline,omitempty" jsonschema:"email me when a deadline is near"`
	EmailIssueUpdates *bool `json:"email_issue_updates,omitempty" jsonschema:"email me when the team replies to or updates an issue I reported"`
}

// --- Attention and progress -------------------------------------------------

type GetAttentionSummaryIn struct {
	StaleDays int `json:"stale_days,omitempty" jsonschema:"a job with no activity for this many days counts as stuck (default 3, max 90)"`
	Limit     int `json:"limit,omitempty" jsonschema:"max items per list (default 10, max 50)"`
}

// AttentionJob is one job that needs a look, with the ids other tools take.
type AttentionJob struct {
	JobID          int64  `json:"job_id"`
	ProjectID      int64  `json:"project_id"`
	ProjectName    string `json:"project_name"`
	FileName       string `json:"file_name"`
	TargetLanguage string `json:"target_language"`
	Status         string `json:"status"`
	CurrentStep    string `json:"current_step"`
	// Detail is the list-specific fact: the deadline, the step with no
	// assignee, the last activity, the error, or the QA error count.
	Detail string `json:"detail"`
}

type AttentionSummary struct {
	Overdue      []AttentionJob `json:"overdue"`
	Unassigned   []AttentionJob `json:"unassigned"`
	Stuck        []AttentionJob `json:"stuck"`
	Failed       []AttentionJob `json:"failed"`
	QAHotspots   []AttentionJob `json:"qa_hotspots"`
	StaleDays    int            `json:"stale_days"`
	LimitPerList int            `json:"limit_per_list"`
	GeneratedAt  string         `json:"generated_at"`
	Definition   string         `json:"definition"`
}

type GetProgressIn struct {
	ProjectID int64 `json:"project_id,omitempty" jsonschema:"one project, from openloc_list_projects. Omit for every project."`
	Cursor    int64 `json:"cursor,omitempty" jsonschema:"pass next_cursor from the previous page to continue. Omit for the first page."`
	Limit     int   `json:"limit,omitempty" jsonschema:"max projects per page (default 20, max 100)"`
}

// ProgressCounts is segments and words for one bucket.
type ProgressCounts struct {
	Segments int64 `json:"segments"`
	Words    int64 `json:"words"`
}

// ProgressRow is one project × target language. The buckets do not overlap
// and add up to Total.
type ProgressRow struct {
	ProjectID      int64          `json:"project_id"`
	ProjectName    string         `json:"project_name"`
	TargetLanguage string         `json:"target_language"`
	Untranslated   ProgressCounts `json:"untranslated"`
	PreTranslated  ProgressCounts `json:"pre_translated"`
	Draft          ProgressCounts `json:"draft"`
	AutoApproved   ProgressCounts `json:"auto_approved"`
	Confirmed      ProgressCounts `json:"confirmed"`
	Total          ProgressCounts `json:"total"`
	// PercentDoneWords is (confirmed + auto_approved) words over total words.
	PercentDoneWords float64 `json:"percent_done_words"`
}

type ProgressReport struct {
	Rows       []ProgressRow `json:"rows"`
	NextCursor int64         `json:"next_cursor"`
	HasMore    bool          `json:"has_more"`
	Definition string        `json:"definition"`
}
