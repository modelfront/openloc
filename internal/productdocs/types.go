package productdocs

// Response shapes for /api/docs. Exported to TypeScript by tygo
// (web/src/types/productdocs.ts) — keep them flat, no embedded structs.
// Frontmatter that only matters to tooling (roles, sources, screenshots) is
// deliberately not part of the wire format.

// DocPageMeta is a page as listed in the navigation tree and in search.
type DocPageMeta struct {
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	Section   string `json:"section"`
	Order     int    `json:"order"`
	UpdatedAt string `json:"updated_at"`
	// Stub pages exist in the tree but have no body yet.
	Stub bool `json:"stub"`
}

// DocSection is one navigation group with the pages the caller may read.
type DocSection struct {
	Slug    string        `json:"slug"`
	Title   string        `json:"title"`
	Summary string        `json:"summary"`
	Order   int           `json:"order"`
	Body    string        `json:"body"`
	Pages   []DocPageMeta `json:"pages"`
}

// DocPage is a full page: metadata, Markdown body, related pages.
type DocPage struct {
	Slug         string        `json:"slug"`
	Title        string        `json:"title"`
	Summary      string        `json:"summary"`
	Section      string        `json:"section"`
	SectionTitle string        `json:"section_title"`
	Order        int           `json:"order"`
	UpdatedAt    string        `json:"updated_at"`
	Stub         bool          `json:"stub"`
	Body         string        `json:"body"`
	Related      []DocPageMeta `json:"related"`
}

// DocSearchHit is one search result with a short body excerpt.
type DocSearchHit struct {
	Slug         string `json:"slug"`
	Title        string `json:"title"`
	Section      string `json:"section"`
	SectionTitle string `json:"section_title"`
	Snippet      string `json:"snippet"`
}
