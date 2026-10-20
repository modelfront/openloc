package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

// registerDocs registers the docs tools.
func registerDocs(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetDocPage,
		title: "Get docs page",
		description: "Read one page of the OpenLoc user documentation as Markdown — how a " +
			"feature works, step by step, in the words the app uses. Pass a slug from " +
			"openloc_list_docs or openloc_search_docs (e.g. guides/cat-editor). " +
			"Use this to answer \"how do I…\" and \"what does … mean\" questions about " +
			"the product itself, rather than guessing. Pages are limited to what the " +
			"current user's role may read.",
	}, b.GetDocPage)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolListDocs,
		title: "List docs pages",
		description: "List the OpenLoc user documentation: sections (getting started, guides, " +
			"reference, concepts) with each page's slug, title, one-line summary and last " +
			"update date. Start here when a question is about how the PRODUCT works — " +
			"then read a page with openloc_get_doc_page. Pages marked stub have no " +
			"body yet. Only pages the current user's role may read are listed.",
	}, b.ListDocs)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSearchDocs,
		title: "Search docs",
		description: "Search the OpenLoc user documentation by words (e.g. \"confirm segment\", " +
			"\"TMX import\", \"forbidden term\"). Returns the best-matching pages with a " +
			"short excerpt; read one with openloc_get_doc_page. For translated text " +
			"in the user's own memories use openloc_search_tm instead — this searches " +
			"the product manual, not their data.",
	}, b.SearchDocs)
}
