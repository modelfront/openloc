package mcp

// ListIssuesIn filters the issues the caller may see: an owner, admin or
// project manager sees every issue of the organization, anyone else the ones
// they reported.
type ListIssuesIn struct {
	Statuses   []string `json:"statuses,omitempty" jsonschema:"only issues in one of these statuses: open, in_progress, needs_info (the support team asked a question), resolved, wont_fix, duplicate. Omit for every status."`
	Kinds      []string `json:"kinds,omitempty" jsonschema:"only these kinds: bug, enhancement, feature_request. Omit for every kind."`
	Areas      []string `json:"areas,omitempty" jsonschema:"only these areas, the part of OpenLoc an issue is about (the same values as openloc_report_issue takes). Omit for every area."`
	Severities []string `json:"severities,omitempty" jsonschema:"only bugs of these severities: minor, major, critical, blocker. Omit for every severity."`
	Query      string   `json:"query,omitempty" jsonschema:"only issues whose title or description contains this text."`
	Sort       string   `json:"sort,omitempty" jsonschema:"the order: up to 3 comma-separated keys, each ascending or with a leading - descending. Keys: kind (bugs first, worst severity first), reporter, area, status (needs info, open, in progress, resolved, won't fix, duplicate), updated (last activity). Omit for the newest report first."`
	Cursor     string   `json:"cursor,omitempty" jsonschema:"pass the next_cursor from a previous response, with the same filters and sort, to continue. Omit for the first page."`
	Limit      int      `json:"limit,omitempty" jsonschema:"max issues per page (default 50, max 100)."`
}

// IssueList is one page of issues.
type IssueList struct {
	Issues []IssueSummary `json:"issues"`
	Count  int            `json:"count"`
	// Total is every issue the filters match, over all pages.
	Total      int64  `json:"total"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// IssueSummary is an issue as the reporter's organization sees it.
type IssueSummary struct {
	ID       int64   `json:"id"`
	Kind     string  `json:"kind"`
	Severity *string `json:"severity,omitempty"`
	Area     string  `json:"area"`
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	// LastActivityBy is who acted last: reporter (the organization) or
	// operator (the support team).
	LastActivityBy string  `json:"last_activity_by"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	ResolvedAt     *string `json:"resolved_at,omitempty"`
	ConfirmedAt    *string `json:"confirmed_at,omitempty"`
	// Assignee is the support team member working on it, once one is.
	Assignee string `json:"assignee,omitempty"`
	// DuplicateOf is the original of a duplicate; its title only when the
	// caller may open it.
	DuplicateOf   *IssueLink `json:"duplicate_of,omitempty"`
	ReporterName  string     `json:"reporter_name,omitempty"`
	ReporterEmail string     `json:"reporter_email,omitempty"`
	// Path is where the user opens it in the app.
	Path string `json:"path"`
}

// IssueLink is another issue: its number and status, and its title when the
// caller may open it.
type IssueLink struct {
	ID      int64  `json:"id"`
	Title   string `json:"title,omitempty"`
	Status  string `json:"status"`
	CanOpen bool   `json:"can_open"`
}

// IssueIDIn names one issue the caller may see.
type IssueIDIn struct {
	IssueID int64 `json:"issue_id" jsonschema:"the issue's id, from openloc_list_issues."`
}

// IssueThread is an issue with its description, its files and its
// conversation as the reporter's organization sees it, oldest entry first.
type IssueThread struct {
	IssueSummary
	Description string         `json:"description"`
	Context     map[string]any `json:"context,omitempty"`
	Files       []IssueFile    `json:"files"`
	Entries     []IssueEntry   `json:"entries"`
}

// IssueFile is a file on an issue or a reply; it is opened in the app.
type IssueFile struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	IsImage   bool   `json:"is_image"`
}

// IssueEntry is one entry of the conversation: a reply, a status change, a
// change of another field, or a mix.
type IssueEntry struct {
	// AuthorSide is reporter (someone in the organization) or operator (the
	// support team).
	AuthorSide string  `json:"author_side"`
	AuthorName string  `json:"author_name"`
	Text       *string `json:"text,omitempty"`
	Status     *string `json:"status,omitempty"`
	// Internal marks a support team note or change the reporter's
	// organization never sees; only the support tools return such entries.
	Internal  bool         `json:"internal,omitempty"`
	Change    *IssueChange `json:"change,omitempty"`
	Files     []IssueFile  `json:"files,omitempty"`
	CreatedAt string       `json:"created_at"`
}

// IssueChange is the field an entry changed, besides the status.
type IssueChange struct {
	Field string  `json:"field"`
	From  *string `json:"from,omitempty"`
	To    *string `json:"to,omitempty"`
}

// ReplyToIssueIn is a reply from the caller's organization.
type ReplyToIssueIn struct {
	IssueID int64  `json:"issue_id" jsonschema:"the issue's id, from openloc_list_issues."`
	Text    string `json:"text" jsonschema:"the reply, in the user's words; at most 10,000 characters."`
}

// ChangeIssueKindIn is a new kind for an issue.
type ChangeIssueKindIn struct {
	IssueID  int64  `json:"issue_id" jsonschema:"the issue's id, from openloc_list_issues."`
	Kind     string `json:"kind" jsonschema:"one of: bug, enhancement, feature_request."`
	Severity string `json:"severity,omitempty" jsonschema:"bugs only, and required for a bug: minor, major, critical or blocker. Leave empty for any other kind."`
}
