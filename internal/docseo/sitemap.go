package docseo

import (
	"encoding/xml"

	"openloc/internal/productdocs"
)

type urlset struct {
	XMLName xml.Name     `xml:"urlset"`
	NS      string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// Sitemap lists every page an anonymous reader may see on the site: the
// home page, each section, each written page with its updated_at, and the
// endpoint reference.
func (site *Site) Sitemap() ([]byte, error) {
	set := urlset{NS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	add := func(path, lastmod string) {
		set.URLs = append(set.URLs, sitemapURL{Loc: site.canonical(path), LastMod: lastmod})
	}
	add("/", "")
	for _, s := range site.Docs.TreeFor(productdocs.RoleAnonymous) {
		add("/"+s.Slug, "")
		for _, p := range s.Pages {
			if !p.Stub {
				add("/"+p.Slug, p.UpdatedAt)
			}
		}
	}
	if len(site.endpoints) > 0 {
		add("/"+endpointsSection, "")
		for _, g := range site.endpoints {
			for _, e := range g.endpoints {
				add("/"+endpointsSection+"/"+e.id, "")
			}
		}
	}
	out, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}
