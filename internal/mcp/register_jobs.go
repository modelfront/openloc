package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

// registerJobs registers the job, segment and QA tools.
//
// Write descriptions say plainly what changes, because the model is deciding
// on the user's behalf whether to mutate their data.
//
// The segment and QA writes are not manager-gated: they are editor actions a
// linguist performs on their own work, so they sit on both tiers. A linguist
// reaches only assigned jobs (the job and translation helpers in the direct
// backend apply the REST assignment gate).
func registerJobs(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetJob,
		title: "Get job details",
		description: "Get one translation job: status, progress, word count, file, language and " +
			"deadline. A job is one file translated into one target language. " +
			"`deadline` is the job's own due date (its active workflow step's); " +
			"`project_deadline` is the project's and does NOT apply to the job — a " +
			"project due Friday does not make its jobs due Friday. " +
			"Use openloc_list_jobs first if you don't already have a job_id.",
	}, b.GetJob)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetJobLeverage,
		title: "Get job TM leverage",
		description: "Get TM leverage for a job: how many segments and words fall into each " +
			"translation-memory match band (tm_101, tm_100, tm_85_99, tm_65_84, new), " +
			"plus words still needing review. Use this for cost and effort questions. " +
			"Bands are derived from the job's current translations. Check total_segments " +
			"before drawing conclusions — a job with no segments looks the same as one " +
			"with no leverage.",
	}, b.GetJobLeverage)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetQAReport,
		title: "Get QA report",
		description: "Get the quality-assurance report for a job: counts of error, warning and " +
			"info issues, plus the individual issues with segment positions. " +
			"Findings a human marked as ignored are still listed, flagged ignored=true, and are " +
			"counted under summary.ignored rather than their severity. " +
			"Reflects the last QA run — use openloc_run_qa to recompute first.",
	}, b.GetQAReport)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListJobs,
		title: "List translation jobs",
		description: "List translation jobs — in one project (pass project_id) or across the " +
			"WHOLE organization (omit project_id). One per file per target language, " +
			"each with its deadline and its project's id and name. To answer \"any " +
			"overdue jobs anywhere?\", query with overdue_only=true and no " +
			"project_id — do not iterate projects, and do not fetch everything and " +
			"filter yourself. Paginated: while has_more is true the list is NOT complete — " +
			"call again with cursor set to next_cursor verbatim (and the same filters) " +
			"before counting or concluding. " +
			"Soonest-due jobs come first; jobs with no deadline last. An empty " +
			"deadline means none is set, NOT that it is inherited from the project. " +
			"Every job carries all its workflow steps in order (steps: name, type, status, " +
			"assignee, deadline), whoever they are assigned to; current_step is the active " +
			"one. Pass assignee_id for the jobs one member has a step in.",
		requireManager: true,
	}, b.ListJobs)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListMyTasks,
		title: "List my tasks",
		description: "List your own work: tasks are the jobs whose ACTIVE workflow step is " +
			"assigned to you (work you can do now); upcoming are your steps that have not " +
			"started yet, each with the active step it waits for (current_step). Use this for " +
			"\"what should I work on\" and \"which steps am I assigned?\" — check both lists " +
			"before saying you have no other steps. For all jobs regardless of assignee, use " +
			"openloc_list_jobs.",
	}, b.ListMyTasks)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListSegments,
		title: "List job segments",
		description: "List a job's segments with source and target text. Paginated: returns at " +
			"most 200 and reports the total, so narrow with status, origin or query " +
			"rather than paging through a large file. " +
			"Use this to inspect actual translation content. Each segment carries " +
			"previous_step: how the last finished workflow step left it.",
	}, b.ListSegments)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolAddComment,
		title: "Add segment comment",
		description: "Add a comment to one segment. Comments are visible to everyone working " +
			"on the job and do not change the translation. Use this to flag a concern " +
			"for a reviewer rather than editing their text.",
		requireWrite: true,
	}, b.AddComment)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolLockSegment,
		title: "Lock or unlock segment",
		description: "Lock a segment, or unlock it. Locking marks the translation final: it " +
			"approves the segment (auto-approved text nobody has edited stays auto-approved) and makes it " +
			"read-only. Locking requires a project manager, admin or owner and refuses an " +
			"empty segment. Anyone who may edit the segment may unlock it; the status stays.",
		requireWrite: true,
	}, b.LockSegment)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSaveDraft,
		title: "Save translation draft",
		description: "Save a segment's translation WITHOUT approving it — it stays in " +
			"needs-review and is not written to translation memory. " +
			"Use this for proposed text a human should check. " +
			"To confirm a segment the user asked you to confirm, use " +
			"openloc_confirm_translation; only a person's confirm writes to translation memory.",
		requireWrite: true,
	}, b.SaveDraft)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolRunQA,
		title: "Run QA on a job",
		description: "Run every enabled QA check on every segment of a job and store the findings, " +
			"replacing the previous run. Synchronous. Findings a human ignored stay ignored " +
			"as long as the same problem is found again; a changed finding shows as new. " +
			"Follow with openloc_get_qa_report for the issue list.",
		requireWrite: true,
	}, b.RunQA)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolIgnoreQAFinding,
		title: "Ignore or un-ignore a QA finding",
		description: "Mark one QA finding on one segment as ignored, or " +
			"remove the ignore with unignore=true. An ignored finding stays visible as " +
			"ignored in the editor and the report, stops counting toward the totals, and no " +
			"longer blocks confirming the segment. The ignore lapses by itself if the segment " +
			"changes so that the finding is different. Use this for confirmed false positives " +
			"only — a real error should be fixed in the text.",
		requireWrite: true,
	}, b.IgnoreQAFinding)
}
