package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

// registerTerminology registers the terminology tools.
func registerTerminology(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolLookupTerm,
		title: "Look up glossary terms",
		description: "Find approved glossary terms occurring in a piece of source text, using the " +
			"term bases assigned to a project. Use this to check terminology " +
			"compliance. For free-text search of past translations, use openloc_search_tm.",
	}, b.LookupTerm)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSearchTM,
		title: "Search translation memory",
		description: "Search a project's translation memories for previously translated text " +
			"(concordance search). Use this to find how a phrase was translated before. " +
			"For glossary terms specifically, use openloc_lookup_term.",
	}, b.SearchTM)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolLookupConcept,
		title: "Look up termbase concepts",
		description: "Find concepts inside ONE term base by EXACT term text (case-sensitive, whole " +
			"term — not a substring). Returns each matching concept with its concept_id, domain, " +
			"definition and all its terms, so you can pick the right meaning (homographs share text " +
			"but differ by domain) and then edit it with openloc_update_concept or extend it with " +
			"openloc_add_term_to_concept. Optionally narrow by language or domain. This is the " +
			"project-independent lookup; openloc_lookup_term is the project-scoped, QA-oriented one.",
		requireManager: true,
	}, b.LookupConcept)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateTermBase,
		title: "Create term base",
		description: "Create a term base (glossary). A term base has ONE source language; " +
			"its target languages are derived automatically from the terms you add later, " +
			"so it can be multilingual. Check openloc_list_term_bases first so you don't " +
			"create a duplicate.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateTermBase)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolAddTerm,
		title: "Add glossary term",
		description: "Quick add of a single source→target term pair to a term base. If a concept " +
			"with the SAME source term and domain already exists, the translation is added to that " +
			"concept (so calling this repeatedly for one source term across languages builds one " +
			"multilingual concept, not duplicates); otherwise a new concept is created. " +
			"status=forbidden means the translation must not be used and QA flags it. For a concept " +
			"spanning several languages in one call, use openloc_create_concept. Returns the " +
			"concept_id it wrote to, so you can chain into openloc_update_concept or " +
			"openloc_add_term_to_concept without a lookup.",
		requireManager: true,
		requireWrite:   true,
	}, b.AddTerm)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateConcept,
		title: "Create termbase concept",
		description: "Add a full concept to a term base. A concept is ONE meaning; its terms " +
			"are that meaning expressed across languages (and synonyms within a language), each " +
			"with its own status (approved, pending, or forbidden — required per term). Use this " +
			"over openloc_add_term when the entry spans more than two languages or carries a " +
			"shared definition/domain.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateConcept)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolAddTermToConcept,
		title: "Add term to concept",
		description: "Add one term (in one language) to an EXISTING concept — e.g. a synonym, " +
			"or a target-language term for a meaning that already exists. Get the concept_id from " +
			"openloc_lookup_term (hits sharing a concept_id) or openloc_create_concept. " +
			"Idempotent: re-adding the same language+text returns the existing term. Status " +
			"(approved, pending, or forbidden) is required.",
		requireManager: true,
		requireWrite:   true,
	}, b.AddTermToConcept)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateConcept,
		title: "Update termbase concept",
		description: "Edit an existing concept's definition and/or domain (e.g. set a gloss on a " +
			"concept that lacks one). Provide only the fields you want to change — omitting one " +
			"leaves it unchanged. Get the concept_id from openloc_lookup_term or " +
			"openloc_create_concept. To change a term's text or status instead, that is a term " +
			"edit, not a concept edit.",
		requireManager: true,
		requireWrite:   true,
	}, b.UpdateConcept)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateTerm,
		title: "Update term",
		description: "Edit one existing term: its text, status (approved/pending/forbidden), part " +
			"of speech, and/or notes. Provide only the fields you want to change — omitting one " +
			"leaves it unchanged; send \"\" to clear part_of_speech or notes. Get the term_id from " +
			"openloc_lookup_concept, openloc_add_term, or openloc_add_term_to_concept. A term's " +
			"language cannot be changed here — remove it and add a new one instead.",
		requireManager: true,
		requireWrite:   true,
	}, b.UpdateTerm)
}
