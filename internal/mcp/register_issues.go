package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const toolReportIssue = "openloc_report_issue"

// registerIssues registers the issue report tool, on a deployment that takes
// reports (Deps.IssueReports). Every role may report, so it is not
// manager-gated. It is allowed in read-only mode — a report changes
// none of the org's data — and guarded, so the user confirms the report
// before it is sent.
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
}
