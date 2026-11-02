package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	toolReportIssue     = "openloc_report_issue"
	toolListIssues      = "openloc_list_issues"
	toolGetIssue        = "openloc_get_issue"
	toolReplyToIssue    = "openloc_reply_to_issue"
	toolConfirmIssueFix = "openloc_confirm_issue_fix"
	toolChangeIssueKind = "openloc_change_issue_kind"
)

// registerIssues registers the issue tools, on a deployment that takes
// reports (Deps.IssueReports). Every role may report and follow its issues, so
// none is manager-gated: the issue service shows each caller what the app's
// Reported issues page shows them. The report is allowed in read-only mode — a
// report changes none of the org's data — and guarded, so the user confirms it
// before it is sent; replies, fix confirmations and kind changes need write
// access. Deleting an issue stays in the app. The support team's side is
// registerSupportIssues.
func registerIssues(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	if !s.deps.IssueReports {
		return
	}
	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolReportIssue,
		title: "Report an issue to the support team",
		description: "Send a bug report, an enhancement or a feature request about OpenLoc itself to the " +
			"support team, who answer it in the app. Use it when the user wants to report a problem " +
			"with OpenLoc or ask for a change to it — not for questions about their own projects. " +
			"Ask the user for anything you cannot tell: the kind, for a bug the severity, and what " +
			"happened. Write the title and description from their words. Check a bug where you " +
			"can, only by reading (never change data to reproduce it), and give the support team your " +
			"verification with one line of evidence. Two calls: the first " +
			"returns a preview and changes nothing; the user confirms it, then the second (with " +
			"confirm_token) sends it. Works in read-only mode too.",
		allowReadOnly: true,
		openWorld:     true,
	}, b.PreviewReportIssue, b.ReportIssue)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListIssues,
		title: "List reported issues",
		description: "List the issues reported to the support team that the user can see in the app: an " +
			"owner, admin or project manager sees every issue of their organization, anyone else the ones " +
			"they reported. Filter by status, kind, area, severity and text. Each page holds up to 100 " +
			"issues; total is how many match. While has_more is true the list is NOT complete: call again " +
			"with next_cursor. Use openloc_get_issue for the conversation of one issue.",
	}, b.ListIssues)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetIssue,
		title: "Read a reported issue",
		description: "Read one issue the user can see: its description, files (names only; they open in " +
			"the app), and the conversation with the support team, oldest entry first, including status " +
			"changes. The support team's internal notes are never included.",
	}, b.GetIssue)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolReplyToIssue,
		title: "Reply to a reported issue",
		description: "Add a reply from the user's organization to an issue they can see (the reporter, or an " +
			"owner, admin or project manager). The support team sees it, and the team member the issue is assigned to gets an email. A reply to an " +
			"issue that is resolved, won't fix, a duplicate or waiting for information (needs_info) " +
			"reopens it as open — that is how the user says a fix does not work. Text only; files are " +
			"added in the app. Write the reply from the user's words and show it to them before sending.",
		requireWrite: true,
		openWorld:    true,
	}, b.ReplyToIssue)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolConfirmIssueFix,
		title: "Confirm the fix of a resolved issue",
		description: "Confirm that the fix of a resolved issue works, for the user's organization, as the " +
			"app's Confirmed fixed button does; the support team sees it. Only for a resolved issue not yet " +
			"confirmed. If the fix does not work, reply with openloc_reply_to_issue instead.",
		requireWrite: true,
		openWorld:    true,
	}, b.ConfirmIssueFix)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolChangeIssueKind,
		title: "Change the kind of a reported issue",
		description: "Change an issue from bug, enhancement or feature request to another kind, as the " +
			"reporter or an owner, admin or project manager can in the app; a bug needs a severity. The " +
			"change shows in the conversation; nobody is emailed.",
		requireWrite: true,
	}, b.ChangeIssueKind)
}
