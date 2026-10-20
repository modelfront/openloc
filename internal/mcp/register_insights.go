package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	toolGetProjectAnalysis            = "openloc_get_project_analysis"
	toolListSavedAnalyses             = "openloc_list_saved_analyses"
	toolSaveAnalysis                  = "openloc_save_analysis"
	toolDeleteSavedAnalysis           = "openloc_delete_saved_analysis"
	toolGetProductivity               = "openloc_get_productivity"
	toolListDashboards                = "openloc_list_dashboards"
	toolGetDashboard                  = "openloc_get_dashboard"
	toolUpdateChart                   = "openloc_update_chart"
	toolDeleteChart                   = "openloc_delete_chart"
	toolGetMe                         = "openloc_get_me"
	toolListInvites                   = "openloc_list_invites"
	toolInviteMember                  = "openloc_invite_member"
	toolSetMemberLanguagePairs        = "openloc_set_member_language_pairs"
	toolChangeMemberRole              = "openloc_change_member_role"
	toolRemoveMember                  = "openloc_remove_member"
	toolRevokeInvite                  = "openloc_revoke_invite"
	toolListNotifications             = "openloc_list_notifications"
	toolGetNotificationPreferences    = "openloc_get_notification_preferences"
	toolMarkNotificationsRead         = "openloc_mark_notifications_read"
	toolUpdateNotificationPreferences = "openloc_update_notification_preferences"
	toolGetAttentionSummary           = "openloc_get_attention_summary"
	toolGetProgress                   = "openloc_get_progress"
)

// registerInsights registers the analysis, analytics, dashboards, team, notifications tools.
//
// Gates mirror the REST routes in api.go: analysis, productivity, dashboards
// reads, invites and the member list are manager-tier; member roles, member removal and language pairs are owner/admin. Chart writes
// follow the dashboard owner rules (dashboard.MayWrite).
// Notifications and get_me are about the caller only, so every role has them.
func registerInsights(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	registerInsightAnalysis(s, srv, managerTier, b)
	registerInsightDashboards(s, srv, managerTier, b)
	registerInsightTeam(s, srv, managerTier, b)
	registerInsightNotifications(s, srv, managerTier, b)
}

func registerInsightAnalysis(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetProjectAnalysis,
		title: "Get project analysis",
		description: "The project's live word analysis, as on its Analysis tab: segments, words and " +
			"weighted words per TM match band (exact, fuzzy 95/85/75/50, QE approved, new), " +
			"per file × target language, per language and in total, plus repetitions, the quality " +
			"prediction outcome split. Computed from the translations as they are now.",
		requireManager: true,
	}, b.GetProjectAnalysis)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListSavedAnalyses,
		title: "List saved analyses",
		description: "List a project's saved analysis runs (snapshots taken at one moment), newest " +
			"first, with status and job ids. Pass analysis_id to get one run with its per-job words " +
			"per match band. openloc_save_analysis starts a new run.",
		requireManager: true,
	}, b.ListSavedAnalyses)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSaveAnalysis,
		title: "Save analysis",
		description: "Start a saved analysis run for chosen jobs of one project, as the Analyses tab " +
			"does. It is created as pending and fills in when the worker finishes — read it back " +
			"with openloc_list_saved_analyses. Pre-translation must have finished for every job.",
		requireManager: true,
		requireWrite:   true,
	}, b.SaveAnalysis)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteSavedAnalysis,
		title: "Delete saved analysis",
		description: "Delete one saved analysis run. The project's live analysis is not affected. " +
			"Two calls: the first returns a preview, the second (with confirm_token, after the " +
			"user approves) deletes.",
		requireManager: true,
		destructive:    true,
	}, b.PreviewDeleteSavedAnalysis, b.DeleteSavedAnalysis)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetProductivity,
		title: "Get translator productivity",
		description: "Translator productivity: segments and words edited, editing time, words per " +
			"hour and average edit distance. scope=org covers every translator for a date range; " +
			"scope=project splits one project by translator and language; scope=project_by_step " +
			"splits it by workflow step too. " +
			"For trends over time use openloc_query_metrics instead.",
		requireManager: true,
	}, b.GetProductivity)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetAttentionSummary,
		title: "Get attention summary",
		description: "What needs a project manager's attention now, in one call: overdue jobs, jobs " +
			"whose current step has no assignee, stuck jobs (no activity for stale_days, default 3), " +
			"failed jobs, and the jobs with the most unresolved QA errors. Every item carries job_id " +
			"and project_id for openloc_get_job, openloc_get_qa_report and the assignment tools. " +
			"Lists are capped; the result says how each list is defined.",
		requireManager: true,
	}, b.GetAttentionSummary)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetProgress,
		title: "Get translation progress",
		description: "Progress per project × target language: segments and words that are " +
			"untranslated, pre-translated (TM/MT, not yet edited), draft (edited, not confirmed), " +
			"auto-approved and confirmed, plus percent done by words. Omit project_id for every " +
			"project, newest first, paginated by project with next_cursor.",
		requireManager: true,
	}, b.GetProgress)
}

func registerInsightDashboards(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListDashboards,
		title: "List dashboards",
		description: "List the dashboards the caller can see: the organization's shared ones and " +
			"their own personal ones (personal=true), with ids and chart counts. " +
			"openloc_get_dashboard shows a dashboard's charts.",
		requireManager: true,
	}, b.ListDashboards)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetDashboard,
		title: "Get dashboard",
		description: "One dashboard's charts with their full definitions (measure, dimensions, " +
			"frozen filters, controls) and drawing options, and can_edit: whether the caller may change it. It holds no data: run openloc_query_metrics " +
			"with a chart's definition for its numbers.",
		requireManager: true,
	}, b.GetDashboard)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateChart,
		title: "Update dashboard chart",
		description: "Change a saved chart: title, definition and drawing options, all sent in full " +
			"(copy unchanged fields from openloc_get_dashboard). Same vocabulary as " +
			"openloc_create_chart; the definition is checked before anything is saved. A shared " +
			"dashboard is owner/admin only; a personal one only its owner's. Two calls: the first " +
			"previews the change, the second (with confirm_token, after the user approves) saves it.",
		requireManager: true,
	}, b.PreviewUpdateChart, b.UpdateChart)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteChart,
		title: "Delete dashboard chart",
		description: "Delete one chart from a dashboard. A shared dashboard is owner/admin only; a " +
			"personal one only its owner's. Two calls: the first returns a preview, the second (with " +
			"confirm_token, after the user approves) deletes.",
		requireManager: true,
		destructive:    true,
	}, b.PreviewDeleteChart, b.DeleteChart)
}

func registerInsightTeam(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetMe,
		title: "Get my profile",
		description: "The caller's own profile: user id, name, email, role in this organization, the " +
			"organization's name, and the language pairs they are qualified for.",
		// ChatGPT's profile tool: its id tells two connected accounts apart.
		meta: map[string]any{"openai/profile": true},
	}, b.GetMe)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListInvites,
		title: "List invites",
		description: "List the organization's invites the user may handle, with email, role, status " +
			"(pending, accepted, expired) and who sent them. Owners see every invite, admins all but " +
			"owner invites, project managers only linguist invites. Invite links are never returned.",
		requireManager: true,
	}, b.ListInvites)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolInviteMember,
		title: "Invite member",
		description: "Invite a person by email; they get a link valid for 7 days. Role is linguist " +
			"by default. An owner or admin may invite admins and project managers; a project " +
			"manager may only invite linguists. Ask for the email and role if the user did not say. Two calls: the first previews, the second (with confirm_token, after the user approves) sends the invite.",
		requireManager: true,
		openWorld:      true,
	}, b.PreviewInviteMember, b.InviteMember)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSetMemberLanguagePairs,
		title: "Set member language pairs",
		description: "Replace a member's language pairs — the qualifications used to suggest " +
			"assignees. Send the COMPLETE list (read the current one from openloc_list_members); " +
			"anything left out is removed. Owner or admin only.",
		requireManager:    true,
		requireOwnerAdmin: true,
		requireWrite:      true,
	}, b.SetMemberLanguagePairs)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolChangeMemberRole,
		title: "Change member role",
		description: "Change a member's role (owner, admin, project_manager, linguist). Owner or " +
			"admin only; only an owner can grant owner or change an owner, nobody can change their " +
			"own role, and the last admin cannot be demoted. Two calls: the first previews, the " +
			"second (with confirm_token, after the user approves) applies.",
		requireManager:    true,
		requireOwnerAdmin: true,
	}, b.PreviewChangeMemberRole, b.ChangeMemberRole)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolRemoveMember,
		title: "Remove member",
		description: "Remove a member from this organization. Their account and other organizations " +
			"stay. Owner or admin only; only an owner can remove an owner, nobody can remove " +
			"themselves, and the last admin stays. Two calls: the first previews, the second (with " +
			"confirm_token, after the user approves) removes.",
		requireManager:    true,
		requireOwnerAdmin: true,
		destructive:       true,
	}, b.PreviewRemoveMember, b.RemoveMember)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolRevokeInvite,
		title: "Revoke invite",
		description: "Revoke an invite so its link stops working; only the invites list_invites shows " +
			"the user. Two calls: the first previews, the second (with confirm_token, after the user " +
			"approves) revokes.",
		requireManager: true,
		destructive:    true,
	}, b.PreviewRevokeInvite, b.RevokeInvite)
}

// registerInsightNotifications registers the caller's own notification tools.
// Not manager-gated: every role has a notification bell.
func registerInsightNotifications(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListNotifications,
		title: "List my notifications",
		description: "The caller's own notifications, newest first, with the unread count. Each names " +
			"what it is about (reference_type and reference_id, e.g. a job id for openloc_get_job). " +
			"Paginated with next_cursor.",
	}, b.ListNotifications)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetNotificationPreferences,
		title: "Get my notification preferences",
		description: "Which notification emails the caller receives: assignment, rejection, " +
			"completion, deadline.",
	}, b.GetNotificationPreferences)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolMarkNotificationsRead,
		title: "Mark notifications read",
		description: "Mark the caller's notifications read: pass ids from openloc_list_notifications, " +
			"or all=true for every unread one. Returns the new unread count.",
		requireWrite: true,
	}, b.MarkNotificationsRead)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateNotificationPreferences,
		title: "Update my notification preferences",
		description: "Turn the caller's notification emails on or off. Send only the switches to " +
			"change; the others keep their value.",
		requireWrite: true,
	}, b.UpdateNotificationPreferences)
}
