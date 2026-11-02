package mcp

import "context"

// The support team's tools: the MCP side of the app's support queue
// (/api/platform/issues), for a caller who is a platform operator. They act
// on every organization's issues.

// supportIssueTools is the support team's side of the issues.
type supportIssueTools interface {
	// IsOperator reports whether the caller is on the support team.
	IsOperator(ctx context.Context) (bool, error)
	SupportListIssues(ctx context.Context, in SupportListIssuesIn) (SupportIssueList, error)
	SupportGetIssue(ctx context.Context, in SupportIssueIDIn) (SupportIssueThread, error)
	SupportSetIssueStatus(ctx context.Context, in SupportSetIssueStatusIn) (SupportIssueThread, error)
	SupportReplyToIssue(ctx context.Context, in SupportIssueTextIn) (SupportIssueThread, error)
	SupportAddIssueNote(ctx context.Context, in SupportIssueTextIn) (SupportIssueThread, error)
	SupportUpdateIssue(ctx context.Context, in SupportUpdateIssueIn) (SupportIssueThread, error)
	SupportListTeam(ctx context.Context, in SupportListTeamIn) (SupportTeam, error)
}

// SupportListIssuesIn filters the support queue: every organization's issues.
type SupportListIssuesIn struct {
	Statuses      []string `json:"statuses,omitempty" jsonschema:"only issues in one of these statuses: open, in_progress, needs_info, resolved, wont_fix, duplicate. Omit for every status."`
	Kinds         []string `json:"kinds,omitempty" jsonschema:"only these kinds: bug, enhancement, feature_request. Omit for every kind."`
	Areas         []string `json:"areas,omitempty" jsonschema:"only these areas, the part of OpenLoc an issue is about (the same values as openloc_report_issue takes). Omit for every area."`
	Severities    []string `json:"severities,omitempty" jsonschema:"only bugs of these severities: minor, major, critical, blocker. Omit for every severity."`
	OrgIDs        []int64  `json:"org_ids,omitempty" jsonschema:"only issues of these organizations, by org_id as the list gives it; with reporter_ids the two add up (an issue of a listed organization OR by a listed reporter). Omit for every organization."`
	ReporterIDs   []int64  `json:"reporter_ids,omitempty" jsonschema:"only issues reported by these people, by reporter_id as the list gives it; with org_ids the two add up (an issue of a listed organization OR by a listed reporter). Omit for everyone."`
	AssigneeIDs   []int64  `json:"assignee_ids,omitempty" jsonschema:"only issues assigned to these support team members, by assignee_id (openloc_support_list_team gives the ids). Omit for any assignee."`
	Unassigned    bool     `json:"unassigned,omitempty" jsonschema:"true adds the issues nobody is assigned to; with no assignee_ids it lists only those."`
	Priorities    []string `json:"priorities,omitempty" jsonschema:"only issues with one of these priorities: low, medium, high. Omit for any priority."`
	Tags          []string `json:"tags,omitempty" jsonschema:"only issues carrying any of these tags. Omit for any tag."`
	Verifications []string `json:"verifications,omitempty" jsonschema:"only reports the assistant checked with one of these results: verified, not_reproduced, unverified. Omit for every report."`
	Query         string   `json:"query,omitempty" jsonschema:"only issues whose title or description contains this text."`
	Sort          string   `json:"sort,omitempty" jsonschema:"the order: up to 3 comma-separated keys, each ascending or with a leading - descending. Keys: kind (bugs first, worst severity first), reporter (organization, then email), area, status (needs info, open, in progress, resolved, won't fix, duplicate), priority, assignee, updated (last activity). Omit for the newest report first."`
	Cursor        string   `json:"cursor,omitempty" jsonschema:"pass the next_cursor from a previous response, with the same filters and sort, to continue. Omit for the first page."`
	Limit         int      `json:"limit,omitempty" jsonschema:"max issues per page (default 50, max 100)."`
}

// SupportIssueList is one page of the support queue.
type SupportIssueList struct {
	Issues []SupportIssueSummary `json:"issues"`
	Count  int                   `json:"count"`
	// Total is every issue the filters match, over all pages.
	Total      int64  `json:"total"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// SupportIssueSummary is an issue as the support team sees it: the
// organization's view plus the team's own fields.
type SupportIssueSummary struct {
	IssueSummary
	OrgID      int64  `json:"org_id"`
	OrgName    string `json:"org_name"`
	ReporterID int64  `json:"reporter_id"`
	// AssigneeID is the support team member working on it, once one is.
	AssigneeID *int64   `json:"assignee_id,omitempty"`
	Priority   *string  `json:"priority,omitempty"`
	Tags       []string `json:"tags"`
	// Verification is the assistant's check of a report it wrote (verified,
	// not_reproduced, unverified), with one line of what it saw; absent on
	// any other report.
	Verification     *string `json:"verification,omitempty"`
	VerificationNote *string `json:"verification_note,omitempty"`
	// ImportKey is where an imported issue came from (a sheet and row).
	ImportKey *string `json:"import_key,omitempty"`
}

// SupportIssueIDIn names one issue of any organization.
type SupportIssueIDIn struct {
	IssueID int64 `json:"issue_id" jsonschema:"the issue's id, from openloc_support_list_issues."`
}

// SupportIssueTextIn is a text the support team adds to an issue.
type SupportIssueTextIn struct {
	IssueID int64  `json:"issue_id" jsonschema:"the issue's id, from openloc_support_list_issues."`
	Text    string `json:"text" jsonschema:"the text, in the user's words; at most 10,000 characters."`
}

// SupportIssueThread is an issue with its description, its files and its
// whole conversation, the team's internal entries included, oldest first.
type SupportIssueThread struct {
	SupportIssueSummary
	Description string         `json:"description"`
	Context     map[string]any `json:"context,omitempty"`
	Files       []IssueFile    `json:"files"`
	Entries     []IssueEntry   `json:"entries"`
	// Duplicates are the issues marked as duplicates of this one, in any
	// organization.
	Duplicates []IssueLink `json:"duplicates"`
	// HasTrail and HasRecording say whether the report carries the reporter's
	// recent actions and the steps they recorded; both open in the app.
	HasTrail     bool `json:"has_trail"`
	HasRecording bool `json:"has_recording"`
}

// SupportSetIssueStatusIn is a status change by the support team.
type SupportSetIssueStatusIn struct {
	IssueID     int64  `json:"issue_id" jsonschema:"the issue's id, from openloc_support_list_issues."`
	Status      string `json:"status" jsonschema:"the new status: open, in_progress, needs_info (asks the reporter a question: the note is required), resolved (the reporter is asked to confirm the fix), wont_fix, duplicate (duplicate_of is required)."`
	Note        string `json:"note,omitempty" jsonschema:"a comment to the reporter that goes with the change, in the user's words; at most 10,000 characters. Setting the status the issue already has only records the note."`
	DuplicateOf *int64 `json:"duplicate_of,omitempty" jsonschema:"with the duplicate status only: the id of the original issue, in any organization."`
}

// SupportUpdateIssueIn changes an issue's other fields. A field left empty
// stays as it is.
type SupportUpdateIssueIn struct {
	IssueID       int64    `json:"issue_id" jsonschema:"the issue's id, from openloc_support_list_issues."`
	Kind          string   `json:"kind,omitempty" jsonschema:"a new kind: bug, enhancement or feature_request. A bug takes the severity given with it, Major when none."`
	Severity      string   `json:"severity,omitempty" jsonschema:"a new severity for a bug: minor, major, critical or blocker."`
	Area          string   `json:"area,omitempty" jsonschema:"a new area (the same values as openloc_report_issue takes)."`
	Priority      string   `json:"priority,omitempty" jsonschema:"a new priority: low, medium or high."`
	ClearPriority bool     `json:"clear_priority,omitempty" jsonschema:"true removes the priority."`
	Tags          []string `json:"tags,omitempty" jsonschema:"the issue's tags, replacing the current ones; an empty list removes them all. Omit to leave them."`
	AssigneeEmail string   `json:"assignee_email,omitempty" jsonschema:"the email of the support team member to assign the issue to (openloc_support_list_team lists them)."`
	Unassign      bool     `json:"unassign,omitempty" jsonschema:"true takes the issue away from its assignee."`
}

// SupportListTeamIn takes no arguments.
type SupportListTeamIn struct{}

// SupportTeam is the support team: who an issue may be assigned to.
type SupportTeam struct {
	Members []SupportTeamMember `json:"members"`
}

// SupportTeamMember is one platform operator with an account.
type SupportTeamMember struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}
