package chat

import "regexp"

// platformPages are the support team's pages (PLATFORM_ADMIN_EMAILS). The
// assistant is told where the user is, but it cannot open them or point at
// anything on them: App.tsx sends everyone else back to the projects list. So
// they have no name and no anchors, and PageNames and BuildPath never see them.
var platformPages = []pageDef{
	{
		pattern:  regexp.MustCompile(`^/platform/orgs$`),
		describe: "the support team's Organizations page (every organization on this server: approve or reject a sign-up, create one, open one to help it)",
	},
	{
		pattern:  regexp.MustCompile(`^/platform/issues$`),
		describe: "the support team's Reported issues queue (issues from every organization, with assignee, priority, tags and the assistant's check)",
	},
	{
		pattern:  regexp.MustCompile(`^/platform/issues/(\d+)$`),
		idParams: []string{"issue_id"},
		describe: "issue %s in the support team's queue (the report, its conversation and internal notes, and the triage fields)",
	},
}
