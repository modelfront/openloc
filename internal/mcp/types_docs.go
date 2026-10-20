package mcp

import (
	"openloc/internal/productdocs"
)

// ListDocsIn takes nothing: the tree is the same for every caller of a role.
type ListDocsIn struct{}

// DocTreeResult is the navigation tree the caller's role may read.
type DocTreeResult struct {
	Sections []productdocs.DocSection `json:"sections"`
}

type SearchDocsIn struct {
	Query string `json:"query" jsonschema:"words to look for in the docs pages, e.g. \"confirm segment\" or \"TMX import\""`
}

// DocSearchResult carries ranked hits with a short excerpt each.
type DocSearchResult struct {
	Hits []productdocs.DocSearchHit `json:"hits"`
}

type GetDocPageIn struct {
	Slug string `json:"slug" jsonschema:"page slug as returned by openloc_list_docs or openloc_search_docs, e.g. guides/cat-editor"`
}
