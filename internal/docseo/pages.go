package docseo

import (
	"html/template"
	"strings"

	"openloc/internal/productdocs"
)

// endpointsSection is the developer site's endpoint reference, /endpoints.
const endpointsSection = "endpoints"

// endpointsTitle and endpointsSummary match the endpoint overview in the app.
const (
	endpointsTitle   = "API documentation"
	endpointsSummary = "Every endpoint, by group. Each page lists who may call it, its fields and errors, and request samples."
)

// Page renders the page at path on the site's host. False when no anonymous
// reader may see a page there.
func (site *Site) Page(path string) (Page, bool) {
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	switch {
	case path == "":
		return site.homePage()
	case parts[0] == endpointsSection && site.Spec != nil:
		switch len(parts) {
		case 1:
			return site.endpointsPage(), true
		case 2:
			return site.endpointPage(parts[1])
		}
		return Page{}, false
	case len(parts) == 1:
		return site.sectionPage(parts[0])
	case len(parts) == 2:
		return site.docPage(path)
	}
	return Page{}, false
}

func (site *Site) canonical(path string) string {
	return site.Origin + path
}

func (site *Site) homePage() (Page, bool) {
	var b htmlBuilder
	b.tag("h1", site.Home.Title)
	b.tag("p", site.Home.Description)
	for _, s := range site.Docs.TreeFor(productdocs.RoleAnonymous) {
		b.link("h2", "/"+s.Slug, s.Title)
		b.tag("p", s.Summary)
		b.pageList(s.Pages)
	}
	if len(site.endpoints) > 0 {
		b.link("h2", "/"+endpointsSection, endpointsTitle)
		b.endpointList(site.endpoints)
	}
	return Page{
		Title:       title(site.Label),
		Description: site.Home.Description,
		Canonical:   site.canonical("/"),
		Body:        b.html(),
	}, true
}

func (site *Site) sectionPage(slug string) (Page, bool) {
	for _, s := range site.Docs.TreeFor(productdocs.RoleAnonymous) {
		if s.Slug != slug {
			continue
		}
		var b htmlBuilder
		b.tag("h1", s.Title)
		b.tag("p", s.Summary)
		if err := b.markdown(s.Body, site.href); err != nil {
			return Page{}, false
		}
		b.pageList(s.Pages)
		return Page{
			Title:       title(s.Title, site.Label),
			Description: s.Summary,
			Canonical:   site.canonical("/" + s.Slug),
			Body:        b.html(),
		}, true
	}
	return Page{}, false
}

func (site *Site) docPage(slug string) (Page, bool) {
	p, ok := site.Docs.PageFor(slug, productdocs.RoleAnonymous)
	if !ok {
		return Page{}, false
	}
	var b htmlBuilder
	b.tag("h1", p.Title)
	b.tag("p", p.Summary)
	if err := b.markdown(p.Body, site.href); err != nil {
		return Page{}, false
	}
	if len(p.Related) > 0 {
		b.tag("h2", "Related")
		b.pageList(p.Related)
	}
	return Page{
		Title:       title(p.Title, site.Label),
		Description: p.Summary,
		Canonical:   site.canonical("/" + p.Slug),
		Body:        b.html(),
	}, true
}

// htmlBuilder writes escaped HTML.
type htmlBuilder struct{ sb strings.Builder }

func (b *htmlBuilder) html() template.HTML { return template.HTML(b.sb.String()) } //nolint:gosec // every piece is escaped or rendered from trusted Markdown with raw HTML off

func (b *htmlBuilder) raw(s string) { b.sb.WriteString(s) }

func (b *htmlBuilder) text(s string) { b.sb.WriteString(template.HTMLEscapeString(s)) }

func (b *htmlBuilder) tag(name, s string) {
	if s == "" {
		return
	}
	b.raw("<" + name + ">")
	b.text(s)
	b.raw("</" + name + ">\n")
}

func (b *htmlBuilder) link(tag, href, s string) {
	b.raw("<" + tag + `><a href="`)
	b.text(href)
	b.raw(`">`)
	b.text(s)
	b.raw("</a></" + tag + ">\n")
}

func (b *htmlBuilder) markdown(body string, href func(string) string) error {
	out, err := productdocs.RenderHTML(body, href)
	if err != nil {
		return err
	}
	b.raw(out)
	return nil
}

func (b *htmlBuilder) pageList(pages []productdocs.DocPageMeta) {
	if len(pages) == 0 {
		return
	}
	b.raw("<ul>\n")
	for _, p := range pages {
		b.raw(`<li><a href="`)
		b.text("/" + p.Slug)
		b.raw(`">`)
		b.text(p.Title)
		b.raw("</a>")
		if p.Summary != "" {
			b.raw(" — ")
			b.text(p.Summary)
		}
		b.raw("</li>\n")
	}
	b.raw("</ul>\n")
}
