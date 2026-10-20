package mcp

import (
	"context"
	"strings"

	"openloc/internal/auth"
	"openloc/internal/productdocs"
)

// Documentation tools.
//
// The one read surface in this package with NO tenant scoping — the docs pages
// are global product content, not customer data. What is scoped is the ROLE:
// the same per-role filtering GET /api/docs applies (a linguist's token never
// sees the org-admin pages), taken from the token's claims, never from an
// argument. A page the role may not read is reported exactly like a page that
// does not exist.

// docsUnavailable is the model-visible message when the server was built
// without a documentation index (tests that don't wire one).
var docsUnavailable = invalidf("documentation is not available on this server")

func (b *directBackend) ListDocs(ctx context.Context, _ ListDocsIn) (DocTreeResult, error) {
	if b.docs == nil {
		return DocTreeResult{}, docsUnavailable
	}
	return DocTreeResult{Sections: b.docs.TreeFor(auth.RoleFromContext(ctx))}, nil
}

func (b *directBackend) SearchDocs(ctx context.Context, in SearchDocsIn) (DocSearchResult, error) {
	if b.docs == nil {
		return DocSearchResult{}, docsUnavailable
	}
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return DocSearchResult{}, invalidf("query is required")
	}
	if len(q) > 200 {
		return DocSearchResult{}, invalidf("query is too long (max 200 characters)")
	}
	return DocSearchResult{Hits: b.docs.Search(q, auth.RoleFromContext(ctx))}, nil
}

func (b *directBackend) GetDocPage(ctx context.Context, in GetDocPageIn) (productdocs.DocPage, error) {
	if b.docs == nil {
		return productdocs.DocPage{}, docsUnavailable
	}
	slug := strings.Trim(strings.TrimSpace(in.Slug), "/")
	page, ok := b.docs.PageFor(slug, auth.RoleFromContext(ctx))
	if !ok {
		return productdocs.DocPage{}, errNotFound
	}
	return page, nil
}
