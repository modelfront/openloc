// Package issues lets a user report a problem to the deployment's support
// team and follow it: a status, and a conversation in which either side
// comments and attaches files. The support team triages each issue (status,
// assignee, priority, tags, duplicates) and keeps internal notes the org never
// reads.
package issues

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/sliceutil"
)

// Statuses, in the order an issue usually moves through them.
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusNeedsInfo  = "needs_info"
	StatusResolved   = "resolved"
	StatusWontFix    = "wont_fix"
	// StatusDuplicate: the support team tracks the problem in another issue
	// (duplicate_of_id).
	StatusDuplicate = "duplicate"
)

// statusLabels are the names users see, in emails and the bell.
var statusLabels = map[string]string{
	StatusOpen:       "Open",
	StatusInProgress: "In progress",
	StatusNeedsInfo:  "Needs info",
	StatusResolved:   "Resolved",
	StatusWontFix:    "Won't fix",
	StatusDuplicate:  "Duplicate",
}

// reopenedByReporter are the statuses a reporter's comment moves back to open:
// the operators asked something, or closed the issue and the reporter disagrees.
var reopenedByReporter = map[string]bool{
	StatusNeedsInfo: true,
	StatusResolved:  true,
	StatusWontFix:   true,
	StatusDuplicate: true,
}

// priorityLabels: how soon the support team means to work on an issue. Their
// own field: the reporter's org never sees it.
var priorityLabels = map[string]string{
	"low":    "Low",
	"medium": "Medium",
	"high":   "High",
}

// KindBug is the only kind with a severity.
const KindBug = "bug"

var kindLabels = map[string]string{
	KindBug:           "Bug",
	"enhancement":     "Enhancement",
	"feature_request": "Feature request",
}

// severityLabels: how badly a bug gets in the way.
var severityLabels = map[string]string{
	"minor":    "Minor",
	"major":    "Major",
	"critical": "Critical",
	"blocker":  "Blocker",
}

// defaultSeverity is the report form's default, given to an issue that
// becomes a bug.
const defaultSeverity = "major"

// AreaOther is the area when the reporter does not pick one.
const AreaOther = "other"

// areaLabels: the part of the product an issue is about.
var areaLabels = map[string]string{
	"editor":           "Editor",
	"projects":         "Projects",
	"tasks":            "Tasks",
	"tm":               "Translation memory",
	"terminology":      "Terminology",
	"mt":               "Machine translation",
	"ai_engines":       "AI engines",
	"chat":             "AI assistant (chat)",
	"qa":               "Quality assurance",
	"workflow":         "Workflow",
	"project_settings": "Project settings",
	"general_settings": "General settings",
	"dashboards":       "Dashboards & reporting",
	"integrations":     "Integrations",
	"api":              "API",
	"mcp":              "MCP server",
	AreaOther:          "Other",
}

const (
	sideReporter = "reporter"
	sideOperator = "operator"
)

// IssueView is an issue as the reporter's org sees it. The support team gets
// PlatformIssueView, which adds the fields that are theirs alone. Lists and
// detail pages fill the reporter fields; the org name is for the support team
// only.
type IssueView struct {
	ID             int64            `json:"id"`
	Kind           string           `json:"kind"`
	Severity       *string          `json:"severity"`
	Area           string           `json:"area"`
	Title          string           `json:"title"`
	Description    string           `json:"description"`
	Status         string           `json:"status"`
	Context        json.RawMessage  `json:"context"`
	Attachments    []AttachmentView `json:"attachments"`
	LastActivityBy string           `json:"last_activity_by"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	ResolvedAt     *time.Time       `json:"resolved_at"`
	// ConfirmedAt: when the reporter's org confirmed the fix of a resolved issue.
	ConfirmedAt *time.Time `json:"confirmed_at"`
	// Assignee is absent until a support team member is assigned.
	Assignee *AssigneeView `json:"assignee,omitempty"`
	// DuplicateOf is the original of a duplicate: its number and status, and
	// its title only when the viewer may open it (CanOpen; for a member, not
	// another org's issue or a colleague's they do not see). Absent once the
	// original is gone.
	DuplicateOf   *IssueRef `json:"duplicate_of,omitempty"`
	OrgName       string    `json:"org_name,omitempty"`
	ReporterName  string    `json:"reporter_name,omitempty"`
	ReporterEmail string    `json:"reporter_email,omitempty"`
	// CanDelete: whether the viewer may delete it (see Member.mayDelete).
	CanDelete bool `json:"can_delete"`
}

// PlatformIssueView is an issue as the support team sees it: the org's view
// plus their own fields.
type PlatformIssueView struct {
	IssueView
	AssigneeID *int64   `json:"assignee_id"`
	Priority   *string  `json:"priority"`
	Tags       []string `json:"tags"`
	// ImportKey: where an imported issue came from (a sheet and row).
	ImportKey *string `json:"import_key"`
	// ChatVerification is the assistant's check of a report it wrote
	// (verified, not_reproduced, unverified), with one line of what it saw;
	// both nil on any other report.
	ChatVerification     *string `json:"chat_verification"`
	ChatVerificationNote *string `json:"chat_verification_note"`
	// Duplicates are the issues marked as duplicates of this one, in any org;
	// filled on the detail page only.
	Duplicates []IssueRef `json:"duplicates,omitempty"`
}

// AssigneeView is the support team member working on an issue. Name is empty
// when the account has none.
type AssigneeView struct {
	Name string `json:"name"`
}

// IssueRef points at another issue. Title is empty when the viewer may not
// open it.
type IssueRef struct {
	ID      int64  `json:"id"`
	Title   string `json:"title,omitempty"`
	Status  string `json:"status"`
	CanOpen bool   `json:"can_open"`
}

// AttachmentView is a file on an issue or a comment. IsImage files are shown
// in the page; every other file is offered as a download.
type AttachmentView struct {
	ID        int64  `json:"id"`
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	IsImage   bool   `json:"is_image"`
}

// CommentView is one entry of the conversation: a comment, a status change, a
// change of another field, or a mix. Internal entries (notes, and priority,
// severity, area and tag changes) reach the support team only.
type CommentView struct {
	ID int64 `json:"id"`
	// AuthorID is nil once the author's account is gone, and on an imported
	// note without an author.
	AuthorID    *int64           `json:"author_id"`
	AuthorSide  string           `json:"author_side"`
	AuthorName  string           `json:"author_name"`
	Body        *string          `json:"body"`
	Status      *string          `json:"status"`
	Internal    bool             `json:"internal,omitempty"`
	Change      *ChangeView      `json:"change,omitempty"`
	Attachments []AttachmentView `json:"attachments"`
	CreatedAt   time.Time        `json:"created_at"`
}

// ChangeView is the field an entry changed, besides the status. For the
// assignee, From and To are names (absent for nobody); for tags,
// comma-separated lists; for a duplicate, the original's id, given to the
// support team only; for confirmed, nothing.
type ChangeView struct {
	Field string  `json:"field"`
	From  *string `json:"from,omitempty"`
	To    *string `json:"to,omitempty"`
}

// IssueDetail is an issue with its conversation, oldest entry first.
type IssueDetail struct {
	Issue    IssueView     `json:"issue"`
	Comments []CommentView `json:"comments"`
}

// PlatformIssueDetail is IssueDetail for the support team, with the internal
// entries and what the browser captured with the report: the action trail and
// the recording of the steps.
type PlatformIssueDetail struct {
	Issue     PlatformIssueView `json:"issue"`
	Comments  []CommentView     `json:"comments"`
	Trail     json.RawMessage   `json:"trail,omitempty"`
	Recording json.RawMessage   `json:"recording,omitempty"`
}

func issueView(i db.Issue) IssueView {
	return IssueView{
		ID: i.ID, Kind: i.Kind, Severity: i.Severity, Area: i.Area, Title: i.Title,
		Description: i.Description, Status: i.Status, Context: rawContext(i.Context),
		Attachments: []AttachmentView{}, LastActivityBy: i.LastActivityBy,
		CreatedAt: i.CreatedAt.Time, UpdatedAt: i.UpdatedAt.Time, ResolvedAt: optionalTime(i.ResolvedAt),
	}
}

// issueRow is the shape every issue list and get query returns: the issue,
// its org's name, the reporter, the assignee's name and the original of a
// duplicate. The queries' generated row types all convert to it.
type issueRow db.ListIssuesForPlatformNewestRow

// view is what every viewer sees: no org name and none of the support team's
// own fields.
func (r issueRow) view() IssueView {
	v := IssueView{
		ID: r.ID, Kind: r.Kind, Severity: r.Severity, Area: r.Area, Title: r.Title,
		Description: r.Description, Status: r.Status, Context: rawContext(r.Context),
		Attachments: []AttachmentView{}, LastActivityBy: r.LastActivityBy,
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
		ResolvedAt: optionalTime(r.ResolvedAt), ConfirmedAt: optionalTime(r.ConfirmedAt),
		ReporterName: r.ReporterName, ReporterEmail: r.ReporterEmail,
	}
	if r.AssigneeID != nil {
		v.Assignee = &AssigneeView{Name: ptr.Deref(r.AssigneeName)}
	}
	if r.DuplicateID != nil {
		v.DuplicateOf = &IssueRef{ID: *r.DuplicateID, Status: ptr.Deref(r.DuplicateStatus), CanOpen: r.DuplicateVisible}
		if r.DuplicateVisible {
			v.DuplicateOf.Title = r.DuplicateTitle
		}
	}
	return v
}

// memberView is the view m, a member of the issue's org, gets.
func (r issueRow) memberView(m Member) IssueView {
	v := r.view()
	v.CanDelete = m.mayDelete(r.ReporterID, r.ReporterRole)
	return v
}

// platformView is the support team's view, who may delete any issue.
func (r issueRow) platformView() PlatformIssueView {
	v := PlatformIssueView{IssueView: r.view(), AssigneeID: r.AssigneeID, Priority: r.Priority,
		Tags: sliceutil.NonNil(r.Tags), ImportKey: r.ImportKey,
		ChatVerification: r.ChatVerification, ChatVerificationNote: r.ChatVerificationNote}
	v.OrgName = r.OrgName
	v.CanDelete = true
	return v
}

func attachmentView(f db.IssueAttachment) AttachmentView {
	return AttachmentView{ID: f.ID, Filename: f.Filename, SizeBytes: f.SizeBytes, IsImage: imageTypes[f.ContentType]}
}

func rawContext(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

func optionalTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
