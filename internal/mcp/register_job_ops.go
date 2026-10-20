package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	toolListJobSteps         = "openloc_list_job_steps"
	toolGetJobAnalysis       = "openloc_get_job_analysis"
	toolPreviewMT            = "openloc_preview_mt"
	toolGetTMMatches         = "openloc_get_tm_matches"
	toolApplyTM              = "openloc_apply_tm"
	toolApplyMT              = "openloc_apply_mt"
	toolAssignJob            = "openloc_assign_job"
	toolAssignStep           = "openloc_assign_step"
	toolSetStepDeadline      = "openloc_set_step_deadline"
	toolAdvanceStep          = "openloc_advance_step"
	toolRejectStep           = "openloc_reject_step"
	toolRerunAIStep          = "openloc_rerun_ai_step"
	toolFindReplaceJob       = "openloc_find_replace_job"
	toolExportJob            = "openloc_export_job"
	toolClearJobTranslations = "openloc_clear_job_translations"
	toolGetSegmentHistory    = "openloc_get_segment_history"
	toolListComments         = "openloc_list_comments"
	toolSaveDraftsBatch      = "openloc_save_drafts_batch"
	toolRunSegmentQA         = "openloc_run_segment_qa"
	toolClearSegment         = "openloc_clear_segment"
	toolDeleteComment        = "openloc_delete_comment"
	toolConfirmTranslation   = "openloc_confirm_translation"
)

// registerJobOps registers the job actions, workflow steps, segments tools.
//
// Gates mirror the /api/jobs/{id} and /api/translations/{id} routes: editor
// actions (Apply TM/MT, clear, find and replace, drafts, confirm, segment QA,
// comments, history) sit on both tiers — a linguist reaches only assigned
// jobs through the job and translation helpers. Assigning, step
// deadlines and re-running the AI step are manager-only. Advance and reject
// are on both tiers; the workflow service lets only the step's assignee or a
// manager act.
func registerJobOps(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	// --- Reads ---
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListJobSteps,
		title: "List job workflow steps",
		description: "List a job's workflow steps in order: name, type, status (pending, in_progress, " +
			"completed, rejected), assignee and deadline. The step with status in_progress is the " +
			"current one. Use the step_assignment_id with openloc_assign_step, " +
			"openloc_set_step_deadline or openloc_reject_step.",
	}, b.ListJobSteps)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetJobAnalysis,
		title: "Get job analysis",
		description: "Get a job's word-count analysis: segments, words and characters per match band " +
			"(TM exact, TM fuzzy 95/85/75/50, QE approved, new, total), with weighted words from the " +
			"organization's analysis weights, plus the quality-prediction breakdown. The same numbers " +
			"as the job's Analysis view. For the live leverage split use openloc_get_job_leverage.",
	}, b.GetJobAnalysis)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolPreviewMT,
		title: "Preview machine translation",
		description: "Show what machine translation gives for one segment of a job (translation_id) or " +
			"for free source_text, using the project's MT engine for the job's language pair. " +
			"Nothing is saved. When the pipeline already machine-translated the segment, its stored " +
			"output is returned (stored=true) instead of a new MT call.",
		openWorld: true,
	}, b.PreviewMT)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetTMMatches,
		title: "Get TM matches for a segment",
		description: "Get the best fuzzy translation-memory matches for one segment of a job " +
			"(translation_id) or for free source_text, from the TMs assigned to the job's project, " +
			"scored like pre-translation (score 1.0 = exact). At most 10, best first. " +
			"For a plain text search across TMs use openloc_search_tm.",
	}, b.GetTMMatches)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolExportJob,
		title: "Export a job",
		description: "Get the download link for a job's translated file: format=source (default) for " +
			"the document in its original format, format=xliff for bilingual XLIFF. Returns " +
			"download_path — give it to the user as a link; opening it downloads the file with " +
			"their signed-in session. Nothing is changed.",
	}, b.ExportJob)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetSegmentHistory,
		title: "Get segment history",
		description: "List every recorded change to one segment, newest first: TM and MT fills, " +
			"human edits and confirms, clears, propagation — with who, when, the workflow step and " +
			"the text at that point.",
	}, b.GetSegmentHistory)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListComments,
		title: "List segment comments",
		description: "List the comments on one segment, oldest first. is_ai=true marks a comment " +
			"written by the AI step. Get comment ids here for openloc_delete_comment.",
	}, b.ListComments)

	// --- Editor writes (both tiers) ---
	addTool(s, srv, managerTier, toolSpec{
		name:  toolApplyTM,
		title: "Apply translation memory",
		description: "Fill a job's untranslated segments from translation memory, like the editor's " +
			"Apply TM. Segments without a TM match stay untranslated; an exact match (same text, " +
			"same locale) is confirmed, other fills are not. Segments another user has open in the " +
			"editor are skipped and counted in held. Runs synchronously and can take a while on a " +
			"large job; refused while another run holds the job.",
		requireWrite: true,
	}, b.ApplyTM)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolApplyMT,
		title: "Apply machine translation",
		description: "Machine-translate a job's untranslated segments with the project's MT engine, " +
			"like the editor's Apply MT. This makes paid MT calls. Filled segments are not " +
			"confirmed; segments another user has open in the editor are skipped and counted in " +
			"held. Runs synchronously and can take a while on a large job; refused while " +
			"another run holds the job.",
		requireWrite: true,
		openWorld:    true,
	}, b.ApplyMT)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolAdvanceStep,
		title: "Complete the current workflow step",
		description: "Complete the job's current workflow step and start the next one, like the " +
			"editor's Complete step button. Only the step's assignee or a manager may do this. " +
			"When segments are not confirmed the call is refused and says how many; ask the user, " +
			"then repeat with allow_unconfirmed=true. A comment is shown to whoever works the next " +
			"step. On the last step the job is also marked completed and its human-confirmed " +
			"segments are written to translation memory.",
		requireWrite: true,
		openWorld:    true,
	}, b.AdvanceStep)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolRejectStep,
		title: "Reject the current workflow step",
		description: "Send the job back: reject its current workflow step and reopen an earlier " +
			"one (the nearest human or hybrid step, or to_step_assignment_id), notifying that step's " +
			"assignee with the reason. Steps in between become not started. The reopened step gets " +
			"back the segment statuses it ended with; text changed since comes back unconfirmed. " +
			"Reopening an AI step, or a hybrid step with rerun_ai, runs its AI again and needs a " +
			"manager. Only the step's assignee or a manager may reject.",
		requireWrite: true,
		openWorld:    true,
	}, b.RejectStep)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSaveDraftsBatch,
		title: "Save several translation drafts",
		description: "Save proposed translations for up to 500 segments of one job in one call, " +
			"WITHOUT confirming them — each lands in needs_review and nothing is written to " +
			"translation memory. Locked segments, and segments someone has open in the editor, are " +
			"skipped. Each segment reports its own outcome (applied, conflicts, locked, held, " +
			"rejected); pass expected_updated_at to turn a " +
			"concurrent edit into a conflict instead of an overwrite.",
		requireWrite: true,
	}, b.SaveDraftsBatch)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolRunSegmentQA,
		title: "Run QA on a segment",
		description: "Re-run every enabled QA check on one segment's stored text and store the " +
			"findings, like the editor does after each save. Returns the segment's findings.",
		requireWrite: true,
	}, b.RunSegmentQA)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolConfirmTranslation,
		title: "Confirm a translation",
		description: "Confirm (approve) one segment's translation, like Ctrl+Enter in the editor — " +
			"only when the user asked for it. Pass text, or leave it empty to confirm the current " +
			"target text. The segment is marked as confirmed by the assistant and is NOT written " +
			"to translation memory; a person confirming it later writes it there. QA errors, a " +
			"lock or a running AI step refuse the confirm.",
	}, b.PreviewConfirmTranslation, b.ConfirmTranslation)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolFindReplaceJob,
		title: "Find and replace in a job",
		description: "Replace text in every translation of a job. The preview lists the matching " +
			"segments with their old and new text; after the user confirms, every matching segment " +
			"is saved as an unconfirmed draft, with its inline tags kept. Segments where the text " +
			"crosses an inline tag are left as they are. Locked segments are left out unless " +
			"include_locked is set; then an owner or admin replaces in them too, and for anyone " +
			"else they are left as they are. Segments someone has open in the editor are left " +
			"as they are and counted in held.",
	}, b.PreviewFindReplaceJob, b.FindReplaceJob)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolClearJobTranslations,
		title: "Clear a job's translations",
		description: "Remove the translations of a job so it can be translated again: scope " +
			"non_confirmed keeps confirmed segments, scope all clears them too. Locked segments " +
			"are always kept, and so are segments another user has open (counted in held). First workflow step only; refused while a run holds the job. The " +
			"preview gives the exact number of segments.",
		destructive: true,
	}, b.PreviewClearJobTranslations, b.ClearJobTranslations)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolClearSegment,
		title: "Clear a segment's translation",
		description: "Remove one segment's translation so it is untranslated again. First workflow " +
			"step only; refused for a locked segment.",
		destructive: true,
	}, b.PreviewClearSegment, b.ClearSegment)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteComment,
		title: "Delete a segment comment",
		description: "Delete one comment from a segment. You may delete your own comments and AI " +
			"comments, not other people's.",
		destructive: true,
	}, b.PreviewDeleteComment, b.DeleteComment)

	// --- Manager-only ---
	addTool(s, srv, managerTier, toolSpec{
		name:  toolAssignJob,
		title: "Assign a job",
		description: "Assign a member to the job's current workflow step, so the job appears on " +
			"their Tasks page. Get user_id from openloc_list_members. To assign a later step, " +
			"use openloc_assign_step.",
		requireManager: true,
		requireWrite:   true,
	}, b.AssignJob)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolAssignStep,
		title: "Assign a workflow step",
		description: "Assign a member to one workflow step of a job (any step, not only the " +
			"current one). Get step_assignment_id from openloc_list_job_steps and user_id from " +
			"openloc_list_members.",
		requireManager: true,
		requireWrite:   true,
	}, b.AssignStep)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSetStepDeadline,
		title: "Set a workflow step deadline",
		description: "Set or clear the deadline of one workflow step of a job. The job's deadline " +
			"is its current step's deadline. Get step_assignment_id from openloc_list_job_steps.",
		requireManager: true,
		requireWrite:   true,
	}, b.SetStepDeadline)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolRerunAIStep,
		title: "Re-run the AI step",
		description: "Run the AI step (quality estimation and automatic post-editing) again for " +
			"the job's current workflow step. It re-scores the step's pre-translated segments, " +
			"auto-approving some and leaving the rest for review. May make paid calls; runs " +
			"synchronously; refused while another run holds the job.",
		requireManager: true,
		requireWrite:   true,
		openWorld:      true,
	}, b.RerunAIStep)
}
