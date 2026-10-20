package mcp

// ReportIssueIn is a report to the support team (the deployment's platform
// operators), as the app's Report an issue form takes it.
type ReportIssueIn struct {
	Kind        string `json:"kind" jsonschema:"one of: bug (something does not work as it should), enhancement (it works but could work better), feature_request (something OpenLoc does not do yet)."`
	Severity    string `json:"severity,omitempty" jsonschema:"bugs only, and required for a bug: one of minor (a small problem, work goes on), major (a real problem with a way around it; the usual choice), critical (an important part does not work and the way around is hard), blocker (the user cannot continue). Leave empty for any other kind."`
	Area        string `json:"area,omitempty" jsonschema:"the part of OpenLoc it is about, one of: editor, projects, tasks, tm (translation memory), terminology, mt (machine translation), ai_engines, chat (the AI assistant), qa (quality assurance), workflow, project_settings, general_settings, dashboards (dashboards and reporting), integrations, api, mcp (the MCP server), other. Defaults to other."`
	Title       string `json:"title" jsonschema:"a short title, at most 200 characters."`
	Description string `json:"description" jsonschema:"what happened, in the user's words: for a bug, what they did, what they expected and what happened instead. At most 10,000 characters."`
	Page        string `json:"page,omitempty" jsonschema:"the app page the user was on when the problem happened, as a path such as /projects/12/jobs/34/edit, when known."`
	// Verification and VerificationNote reach the support team only; the
	// reporter's org never reads them. They count only from the built-in
	// assistant (fromBuiltInAssistant).
	Verification        string   `json:"verification,omitempty" jsonschema:"your check of the problem, resting only on the user's words, the data you read with tools and a screenshot the user sent: verified (you saw the problem yourself), not_reproduced (you looked and everything was as it should be), unverified (you could not check it). Only the support team sees it, and only OpenLoc's built-in assistant sets it: from other clients it is ignored. Leave empty when you did not look."`
	VerificationNote    string   `json:"verification_note,omitempty" jsonschema:"required with verification: one line, at most 300 characters, of what you saw, such as the data or the part of the screenshot that shows it."`
	AttachmentUploadIDs []string `json:"attachment_upload_ids,omitempty" jsonschema:"the upload_id of each screenshot the user sent for this problem (from the note in their message), to attach to the report. At most 3."`
	Confirm
}

// ReportIssueResult is the filed issue.
type ReportIssueResult struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Path is where the user follows it in the app.
	Path string `json:"path"`
}
