package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	toolListTMs          = "openloc_list_tms"
	toolGetTM            = "openloc_get_tm"
	toolListTMEntries    = "openloc_list_tm_entries"
	toolListTMImports    = "openloc_list_tm_imports"
	toolCreateTM         = "openloc_create_tm"
	toolRenameTM         = "openloc_rename_tm"
	toolUpdateTMEntry    = "openloc_update_tm_entry"
	toolImportTMX        = "openloc_import_tmx"
	toolExportTMX        = "openloc_export_tmx"
	toolDeleteTM         = "openloc_delete_tm"
	toolListTMProfiles   = "openloc_list_tm_profiles"
	toolCreateTMProfile  = "openloc_create_tm_profile"
	toolUpdateTMProfile  = "openloc_update_tm_profile"
	toolDeleteTMProfile  = "openloc_delete_tm_profile"
	toolListTermBases    = "openloc_list_term_bases"
	toolGetTermBase      = "openloc_get_term_base"
	toolListConcepts     = "openloc_list_concepts"
	toolGetConcept       = "openloc_get_concept"
	toolRenameTermBase   = "openloc_rename_term_base"
	toolImportTerms      = "openloc_import_terms"
	toolExportTerms      = "openloc_export_terms"
	toolDeleteTermBase   = "openloc_delete_term_base"
	toolDeleteConcept    = "openloc_delete_concept"
	toolDeleteTermInBase = "openloc_delete_term"
)

// registerResources registers the translation memories, TM profiles, term bases tools.
//
// Gates mirror /api/tms, /api/tm-profiles and /api/terminology in api.go:
// every tool is manager-tier; deleting a TM, a TM profile or a term base is
// owner/admin only. A linguist reaches TMs and terms only through
// the job-scoped openloc_search_tm and openloc_lookup_term.
func registerResources(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	registerTMTools(s, srv, managerTier, b)
	registerTMProfileTools(s, srv, managerTier, b)
	registerTermBaseTools(s, srv, managerTier, b)
}

func registerTMTools(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListTMs,
		title: "List translation memories",
		description: "List the organization's translation memories (TMs) with their languages, entry count and " +
			"customer. Use the tm_id with the other TM tools. To search the TMs a project uses, use " +
			"openloc_search_tm instead.",
		requireManager: true,
	}, b.ListTMs)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetTM,
		title: "Get translation memory",
		description: "One translation memory: its declared languages, the language pairs its entries hold, " +
			"its entry count, and how many project, template and TM profile settings use it.",
		requireManager: true,
	}, b.GetTM)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListTMEntries,
		title: "List TM entries",
		description: "Page through the entries of one translation memory, optionally filtered by language and " +
			"by a substring of the source or target text. Pass next_cursor back as cursor for the next page. " +
			"A search gives total, the number of matches, on its first page only. " +
			"Each entry carries the version openloc_update_tm_entry needs.",
		requireManager: true,
	}, b.ListTMEntries)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListTMImports,
		title: "List TMX imports",
		description: "The latest TMX imports into one translation memory, newest first, with status " +
			"(pending, processing, completed, failed) and imported/skipped counts. Use it to follow an " +
			"import started with openloc_import_tmx.",
		requireManager: true,
	}, b.ListTMImports)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateTM,
		title: "Create translation memory",
		description: "Create an empty translation memory with one source language and one or more target " +
			"languages, optionally for a customer. Check openloc_list_tms first so you don't create a " +
			"duplicate. Fill it with openloc_import_tmx.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateTM)

	addTool(s, srv, managerTier, toolSpec{
		name:           toolRenameTM,
		title:          "Rename translation memory",
		description:    "Change the name of a translation memory. Nothing else changes.",
		requireManager: true,
		requireWrite:   true,
	}, b.RenameTM)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateTMEntry,
		title: "Edit TM entry",
		description: "Replace the source and target text of one TM entry; the old text is kept in the entry's " +
			"history. Send both texts in full. Pass the version from openloc_list_tm_entries so a " +
			"concurrent edit is not overwritten. Entries with inline tags (has_tags) can only have their " +
			"untagged side changed.",
		requireManager: true,
		requireWrite:   true,
	}, b.UpdateTMEntry)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolImportTMX,
		title: "Import TMX file",
		description: "Import a TMX file the user attached in the assistant panel into a translation memory. " +
			"Every translation unit is added as an entry. The import runs in the background; follow it " +
			"with openloc_list_tm_imports. If the user has not attached a file, ask them to.",
		requireManager: true,
		requireWrite:   true,
	}, b.ImportTMX)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolExportTMX,
		title: "Export translation memory",
		description: "Get a download link for a translation memory as a TMX file. Give the user the " +
			"download_path as a link; their browser downloads the file.",
		requireManager: true,
	}, b.ExportTMX)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteTM,
		title: "Delete translation memory",
		description: "Delete a translation memory. Refused while any project, template or TM profile uses it. " +
			"Owner or admin only. Two calls: the first returns a preview, the second (with confirm_token, " +
			"after the user approves) deletes.",
		requireManager:    true,
		requireOwnerAdmin: true,
		destructive:       true,
	}, b.PreviewDeleteTM, b.DeleteTM)
}

func registerTMProfileTools(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListTMProfiles,
		title: "List TM profiles",
		description: "List the organization's TM profiles: named bundles of translation memories (each with " +
			"mode, priority and penalty) plus match scoring, which projects and templates follow. " +
			"usage_count is how many projects and templates follow each one.",
		requireManager: true,
	}, b.ListTMProfiles)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateTMProfile,
		title: "Create TM profile",
		description: "Create a TM profile from translation memories (tm_id from openloc_list_tms) and " +
			"optional match scoring. Names are unique in the organization.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateTMProfile)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateTMProfile,
		title: "Update TM profile",
		description: "Change a TM profile's name, description, match scoring and/or its TM list (tms replaces " +
			"the whole list). Omitted fields keep their values. Every project and template following the " +
			"profile picks up the change. Two calls: the first returns a preview naming who is affected, " +
			"the second (with confirm_token, after the user approves) applies it.",
		requireManager: true,
	}, b.PreviewUpdateTMProfile, b.UpdateTMProfile)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteTMProfile,
		title: "Delete TM profile",
		description: "Delete a TM profile. Its translation memories are kept. Refused while any project or " +
			"template follows it. Owner or admin only. Two calls: the first returns a preview, the second " +
			"(with confirm_token, after the user approves) deletes.",
		requireManager:    true,
		requireOwnerAdmin: true,
		destructive:       true,
	}, b.PreviewDeleteTMProfile, b.DeleteTMProfile)
}

func registerTermBaseTools(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListTermBases,
		title: "List term bases",
		description: "List the organization's term bases (glossaries) with source language, the languages " +
			"their terms use and concept count. Use the term_base_id with the other terminology tools.",
		requireManager: true,
	}, b.ListTermBases)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetTermBase,
		title: "Get term base",
		description: "One term base: languages, concept count, and how many projects and templates have " +
			"it assigned.",
		requireManager: true,
	}, b.GetTermBase)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListConcepts,
		title: "List termbase concepts",
		description: "Page through the concepts of one term base with all their terms, optionally filtered " +
			"by a substring (term text, definition or domain) and by languages. Pass next_cursor back as " +
			"cursor for the next page. For an exact term lookup use openloc_lookup_concept.",
		requireManager: true,
	}, b.ListConcepts)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetConcept,
		title: "Get termbase concept",
		description: "One concept with its definition, domain and every term (term_id, language, text, " +
			"status, part of speech, notes).",
		requireManager: true,
	}, b.GetConcept)

	addTool(s, srv, managerTier, toolSpec{
		name:           toolRenameTermBase,
		title:          "Rename term base",
		description:    "Change a term base's name and, optionally, its description. Its terms do not change.",
		requireManager: true,
		requireWrite:   true,
	}, b.RenameTermBase)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolImportTerms,
		title: "Import terms from file",
		description: "Import a CSV or TBX file the user attached in the assistant panel into a term base. " +
			"TBX keeps every language, synonym and status; CSV rows are bilingual (source_term, " +
			"target_term, status, domain, notes, with optional source_language/target_language columns). " +
			"Rows that cannot be read are skipped and counted. If the user has not attached a file, ask them to.",
		requireManager: true,
		requireWrite:   true,
	}, b.ImportTerms)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolExportTerms,
		title: "Export term base",
		description: "Get a download link for a term base as TBX (all languages; default) or CSV. Give the " +
			"user the download_path as a link; their browser downloads the file.",
		requireManager: true,
	}, b.ExportTerms)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteTermBase,
		title: "Delete term base",
		description: "Delete a term base with all its concepts and terms. Owner or admin only. Two calls: the " +
			"first returns a preview naming where it is assigned, the second (with confirm_token, after " +
			"the user approves) deletes.",
		requireManager:    true,
		requireOwnerAdmin: true,
		destructive:       true,
	}, b.PreviewDeleteTermBase, b.DeleteTermBase)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteConcept,
		title: "Delete termbase concept",
		description: "Delete one concept and with it all its terms. Two calls: the first returns a preview " +
			"listing the terms, the second (with confirm_token, after the user approves) deletes.",
		requireManager: true,
		destructive:    true,
	}, b.PreviewDeleteConcept, b.DeleteConcept)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteTermInBase,
		title: "Delete term",
		description: "Delete one term from a concept; the concept and its other terms stay. Two calls: the " +
			"first returns a preview, the second (with confirm_token, after the user approves) deletes.",
		requireManager: true,
		destructive:    true,
	}, b.PreviewDeleteTerm, b.DeleteTerm)
}
