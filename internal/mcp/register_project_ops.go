package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	toolGetProjectSettings       = "openloc_get_project_settings"
	toolListProjectEvents        = "openloc_list_project_events"
	toolUpdateProject            = "openloc_update_project"
	toolSetProjectLabels         = "openloc_set_project_labels"
	toolUpdateProjectSettings    = "openloc_update_project_settings"
	toolSetProjectTMs            = "openloc_set_project_tms"
	toolSetProjectTermBases      = "openloc_set_project_term_bases"
	toolSetProjectWorkflowSteps  = "openloc_set_project_workflow_steps"
	toolApplyWorkflow            = "openloc_apply_workflow"
	toolPublishProject           = "openloc_publish_project"
	toolPretranslateProject      = "openloc_pretranslate_project"
	toolClearProjectTranslations = "openloc_clear_project_translations"

	toolGetFile            = "openloc_get_file"
	toolRenameFile         = "openloc_rename_file"
	toolAttachUploadedFile = "openloc_attach_uploaded_file"
	toolUploadTextFile     = "openloc_upload_text_file"
	toolReimportXLIFF      = "openloc_reimport_xliff"
	toolExportFile         = "openloc_export_file"
	toolDownloadSource     = "openloc_download_source"

	toolListLabels       = "openloc_list_labels"
	toolCreateLabel      = "openloc_create_label"
	toolAddLabelValue    = "openloc_add_label_value"
	toolDeleteLabel      = "openloc_delete_label"
	toolDeleteLabelValue = "openloc_delete_label_value"
)

// registerProjectOps registers the projects, files, project settings, labels tools.
// Gates mirror the REST routes in internal/api/api.go: the /api/projects and
// /api/labels groups are manager; label deletes are owner/admin; the
// /api/files/{id} reads and XLIFF import are open to any member with access
// to the file (a linguist needs an assignment; for import, on that language).
func registerProjectOps(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	registerProjectOpsProjects(s, srv, managerTier, b)
	registerProjectOpsFiles(s, srv, managerTier, b)
	registerProjectOpsLabels(s, srv, managerTier, b)
}

func registerProjectOpsProjects(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetProjectSettings,
		title: "Get project settings",
		description: "Get how a project is configured: pre-translation on upload (TM, MT), TM match " +
			"scoring, QA profile, MT router, the TM profile it follows (if any), its TMs, term bases, " +
			"workflow steps (with step and assignee ids) and its labels (Customer, Domain and the " +
			"organization's own). Read this before changing any of them.",
		requireManager: true,
	}, b.GetProjectSettings)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListProjectEvents,
		title: "List project history",
		description: "List a project's history, newest first: uploads, settings and workflow changes, " +
			"status changes, pre-translation runs, clears, exports — each with who did it and what " +
			"changed. Filter by type prefix (e.g. \"file.\") or actor_id; page with next_cursor.",
		requireManager: true,
	}, b.ListProjectEvents)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateProject,
		title: "Update project",
		description: "Change a project's name, description, status (in_progress, on_hold, completed) " +
			"or deadline. Only the fields you pass change. A draft is published with " +
			"openloc_publish_project, not by setting its status. Customer and domain are labels: " +
			"use openloc_set_project_labels.",
		requireManager: true,
		requireWrite:   true,
	}, b.UpdateProject)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSetProjectLabels,
		title: "Set project labels",
		description: "Replace a project's labels — Customer, Domain and the organization's own labels — " +
			"with the value ids you pass (from openloc_list_labels), at most one per label. Read the " +
			"current ones with openloc_get_project_settings and pass them along to keep them.",
		requireManager: true,
		requireWrite:   true,
	}, b.SetProjectLabels)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateProjectSettings,
		title: "Update project settings",
		description: "Change a project's own settings: pre-translate uploads from TM and/or MT, auto " +
			"complete, TM match threshold and penalties, QA profile, MT router, or link it to a TM " +
			"profile. Only the fields you pass change; the change applies to this project only. " +
			"Returns the full settings.",
		requireManager: true,
		requireWrite:   true,
	}, b.UpdateProjectSettings)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSetProjectTMs,
		title: "Set project translation memories",
		description: "Replace the list of translation memories a project reads from and writes to. " +
			"Pass the complete list (read the current one with openloc_get_project_settings). " +
			"Refused while the project follows a TM profile.",
		requireManager: true,
		requireWrite:   true,
	}, b.SetProjectTMs)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSetProjectTermBases,
		title: "Set project term bases",
		description: "Replace the list of term bases a project uses, each for a source → target " +
			"language pair. Pass the complete list (read the current one with " +
			"openloc_get_project_settings).",
		requireManager: true,
		requireWrite:   true,
	}, b.SetProjectTermBases)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolSetProjectWorkflowSteps,
		title: "Set project workflow steps",
		description: "Replace a project's workflow steps, first step first — each position a library " +
			"step (openloc_list_step_definitions) with an optional default assignee and deadline. " +
			"Jobs in progress are affected: changed assignees move onto them. Two calls: the first " +
			"returns a preview, the second (with confirm_token, after the user approves) applies.",
		requireManager: true,
		openWorld:      true,
	}, b.PreviewSetProjectWorkflowSteps, b.SetProjectWorkflowSteps)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolApplyWorkflow,
		title: "Apply library workflow to project",
		description: "Replace a project's workflow steps with a copy of a library workflow " +
			"(openloc_list_workflows). Jobs in progress are affected like any step edit. Two calls: " +
			"the first returns a preview, the second (with confirm_token, after the user approves) applies.",
		requireManager: true,
		openWorld:      true,
	}, b.PreviewApplyWorkflow, b.ApplyWorkflow)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolPublishProject,
		title: "Publish draft project",
		description: "Publish a draft project: it needs a name, a source and at least one target " +
			"language. Its files get their jobs and pre-translation starts per the project settings. " +
			"Only drafts can be published.",
		requireManager: true,
		requireWrite:   true,
		openWorld:      true,
	}, b.PublishProject)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolPretranslateProject,
		title: "Pre-translate project",
		description: "Start TM and MT pre-translation for every job of a project that is not running " +
			"or completed. Segments that already have a translation are kept. MT may cost money. " +
			"Returns how many jobs were queued and skipped.",
		requireManager: true,
		requireWrite:   true,
		openWorld:      true,
	}, b.PretranslateProject)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolClearProjectTranslations,
		title: "Clear project translations",
		description: "Empty the translations of every file and language of a project (scope " +
			"non_confirmed or all), with their QA findings and AI comments. Segments another user has " +
			"open are kept and counted in held. Refused while a job is " +
			"running or past its first workflow step. Two calls: the first returns a preview with the " +
			"exact count, the second (with confirm_token, after the user approves) clears.",
		requireManager: true,
		destructive:    true,
	}, b.PreviewClearProjectTranslations, b.ClearProjectTranslations)
}

func registerProjectOpsFiles(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:           toolGetFile,
		title:          "Get file details",
		description:    "Get one project file: name, format, segment count, dates, and whether the original upload can be downloaded.",
		requireManager: true,
	}, b.GetFile)

	addTool(s, srv, managerTier, toolSpec{
		name:           toolRenameFile,
		title:          "Rename file",
		description:    "Change a project file's display name. The document itself does not change.",
		requireManager: true,
		requireWrite:   true,
	}, b.RenameFile)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolAttachUploadedFile,
		title: "Add attached document to project",
		description: "Add a document the user attached in the assistant to a project, like an upload " +
			"on the project page: the format is detected from the file, its segments are created, and " +
			"on a live project one job per target language starts (with pre-translation per the " +
			"project settings). On a draft the jobs are created when it is published. Only files " +
			"attached in OpenLoc's own assistant have an upload_id; for a text file you can read, use " +
			"openloc_upload_text_file. If the user has not attached a file, ask them to.",
		requireManager: true,
		requireWrite:   true,
		openWorld:      true,
	}, b.AttachUploadedFile)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolUploadTextFile,
		title: "Add text document to project",
		description: "Add a text document to a project by sending its content, like an upload on the " +
			"project page: segments are created, and on a live project one job per target language " +
			"starts (with pre-translation per the project settings); on a draft the jobs are created " +
			"when it is published. Use it when the user's file is not attached in OpenLoc's own " +
			"assistant. Text formats only — .txt, .html, .md, .json, .xliff/.xlf, .po/.pot, .strings, " +
			".xml — up to 1 MB; send the file's full text unchanged and its name with the extension. " +
			"Word, PDF and Excel files are uploaded on the project page instead. For a format OpenLoc " +
			"does not read (slides, a CMS export, an app's own format), send a .json object of " +
			"key → text: one stable key per piece of text that says where it lives (e.g. " +
			"\"slide3.title\"), the text unchanged. To finish, read the translated JSON with " +
			"openloc_export_file (include_content: true) and put the translations back into the " +
			"original file.",
		requireManager: true,
		requireWrite:   true,
		openWorld:      true,
	}, b.UploadTextFile)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolReimportXLIFF,
		title: "Re-import XLIFF",
		description: "Update a file's translations for one target language from an XLIFF the user " +
			"attached (exported from this file and edited elsewhere). Changed segments go back to " +
			"needs review; locked segments and segments another user has open are never touched; " +
			"approved ones only with force=true. " +
			"Two calls: the first returns a preview of what changes, the second (with " +
			"confirm_token, after the user approves) applies.",
	}, b.PreviewReimportXLIFF, b.ReimportXLIFF)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolExportFile,
		title: "Export translated file",
		description: "Get a download link for a file's translation in one target language: the " +
			"translated document in its own format (default) or a bilingual XLIFF. Give the user the " +
			"download_path as a link; it opens in their signed-in browser. When you need the file " +
			"yourself and cannot open the link, set include_content: the text comes back in content " +
			"for a text format (.txt, .html, .md, .json, .xliff, .po, .strings, .xml, or format " +
			"xliff) of at most 1 MB; otherwise content_error says why and the link still works.",
	}, b.ExportFile)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolDownloadSource,
		title: "Download original file",
		description: "Get a download link for a file's original upload, untouched. Give the user the " +
			"download_path as a link; it opens in their signed-in browser.",
	}, b.DownloadSource)
}

func registerProjectOpsLabels(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListLabels,
		title: "List labels",
		description: "List the organization's project labels with their allowed values and ids — " +
			"the built-in Customer and Domain, and labels the organization defined. Use the value " +
			"ids with openloc_set_project_labels.",
		requireManager: true,
	}, b.ListLabels)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateLabel,
		title: "Create label",
		description: "Create a project label (e.g. Country) with optional first values. A label " +
			"name cannot be changed later.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateLabel)

	addTool(s, srv, managerTier, toolSpec{
		name:           toolAddLabelValue,
		title:          "Add label value",
		description:    "Add one allowed value to a label, e.g. a new customer to the Customer label.",
		requireManager: true,
		requireWrite:   true,
	}, b.AddLabelValue)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteLabel,
		title: "Delete label",
		description: "Delete a label and its values. Refused for the built-in Customer and Domain " +
			"and for a label while any project uses one of its values. Two calls: the first returns a preview, the second " +
			"(with confirm_token, after the user approves) deletes.",
		requireManager:    true,
		requireOwnerAdmin: true,
		destructive:       true,
	}, b.PreviewDeleteLabel, b.DeleteLabel)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteLabelValue,
		title: "Delete label value",
		description: "Delete one allowed value of a label. Projects and templates that use it lose " +
			"the pick. Two calls: the first returns a preview, the second (with confirm_token, after the user " +
			"approves) deletes.",
		requireManager:    true,
		requireOwnerAdmin: true,
		destructive:       true,
	}, b.PreviewDeleteLabelValue, b.DeleteLabelValue)
}
