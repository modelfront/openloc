package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	toolSupportListIssues     = "openloc_support_list_issues"
	toolSupportGetIssue       = "openloc_support_get_issue"
	toolSupportSetIssueStatus = "openloc_support_set_issue_status"
	toolSupportReplyToIssue   = "openloc_support_reply_to_issue"
	toolSupportAddIssueNote   = "openloc_support_add_issue_note"
	toolSupportUpdateIssue    = "openloc_support_update_issue"
	toolSupportListTeam       = "openloc_support_list_team"
)

// registerSupportIssues registers the support team's issue tools, the MCP side
// of the app's support queue, on the operator tiers only (Server.build), and
// each is refused at call time for anyone off the team (requireOperator). They
// act on every organization's issues, so they are served over /mcp only, never
// to the built-in chat (ConnectInProcess). The reads work with read access;
// the changes need write access, and a status change or a reply emails the
// reporter. Deleting an issue stays in the app.
func registerSupportIssues(s *Server, srv *sdk.Server, b Backend) {
	if !s.deps.IssueReports {
		return
	}
	addTool(s, srv, true, toolSpec{
		name:  toolSupportListIssues,
		title: "List the support queue",
		description: "Support team only. List the issues every organization reported, as the app's support " +
			"queue shows them, with the team's own fields: organization, reporter, assignee, priority, " +
			"tags and the assistant's check. Filter by status, kind, area, severity, organization, " +
			"reporter, assignee, priority, tag, verification and text. Each page holds up to 100 issues; " +
			"total is how many match. While has_more is true the list is NOT complete: call again with " +
			"next_cursor. Use openloc_support_get_issue for the conversation of one issue.",
		requireOperator: true,
	}, b.SupportListIssues)

	addTool(s, srv, true, toolSpec{
		name:  toolSupportGetIssue,
		title: "Read an issue as the support team",
		description: "Support team only. Read one issue of any organization: its description, files (names " +
			"only; they open in the app), the issues marked as its duplicates, and the whole conversation, " +
			"oldest entry first, including status changes and the team's internal notes (internal: true). " +
			"The reporter's recent actions and recorded steps, when the report carries them, open in the app.",
		requireOperator: true,
	}, b.SupportGetIssue)

	addTool(s, srv, true, toolSpec{
		name:  toolSupportSetIssueStatus,
		title: "Change an issue's status",
		description: "Support team only. Change an issue's status, with an optional note to the reporter, " +
			"as the app's status menu does: open, in_progress, needs_info (a question for the reporter; " +
			"the note is required), resolved (asks the reporter to confirm the fix and rings the bell of " +
			"the organization's managers), wont_fix, or duplicate of another issue (duplicate_of). The " +
			"reporter gets a notification and an email. Setting the status the issue already has only " +
			"records the note. Write the note from the user's words and show it to them before sending.",
		requireOperator: true,
		requireWrite:    true,
		openWorld:       true,
	}, b.SupportSetIssueStatus)

	addTool(s, srv, true, toolSpec{
		name:  toolSupportReplyToIssue,
		title: "Reply to an issue as the support team",
		description: "Support team only. Add a reply the reporter's organization sees, as the app's Reply " +
			"does. The reporter gets a notification and an email. It never changes the status: to ask the " +
			"reporter something, use openloc_support_set_issue_status with needs_info. Text only; files " +
			"are added in the app. Write the reply from the user's words and show it to them before sending.",
		requireOperator: true,
		requireWrite:    true,
		openWorld:       true,
	}, b.SupportReplyToIssue)

	addTool(s, srv, true, toolSpec{
		name:  toolSupportAddIssueNote,
		title: "Add an internal note to an issue",
		description: "Support team only. Add an internal note that the reporter's organization never sees, " +
			"as the app's Internal note does. It changes nothing on the issue and tells nobody. Text only.",
		requireOperator: true,
		requireWrite:    true,
	}, b.SupportAddIssueNote)

	addTool(s, srv, true, toolSpec{
		name:  toolSupportUpdateIssue,
		title: "Update an issue's fields as the support team",
		description: "Support team only. Change any of an issue's kind, severity, area, priority, tags and " +
			"assignee, as the app's side panel does; a field left out stays as it is. Only a support team " +
			"member may be assigned (openloc_support_list_team). Each change shows in the conversation; " +
			"the reporter sees only the assignee change, and nobody is emailed.",
		requireOperator: true,
		requireWrite:    true,
	}, b.SupportUpdateIssue)

	addTool(s, srv, true, toolSpec{
		name:  toolSupportListTeam,
		title: "List the support team",
		description: "Support team only. List the support team members an issue may be assigned to, with " +
			"their ids and emails.",
		requireOperator: true,
	}, b.SupportListTeam)
}
