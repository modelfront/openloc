// Package docseo renders the public documentation sites' pages as plain HTML
// for readers that do not run JavaScript: search engines and AI crawlers.
// The server puts it inside the app's index.html, so the SPA still takes
// over in a browser. Only pages an anonymous reader may see are rendered.
package docseo

import (
	"html/template"
	"strings"

	"openloc/internal/productdocs"
	"openloc/internal/publicapi"
)

// Site is one public documentation site, at the root of its own host.
type Site struct {
	Host   string // e.g. docs.openloc.com; empty = no host, nothing rendered
	Origin string // scheme://host[:port]
	Prefix string // the site's pages on the app's address, e.g. /docs
	API    string // the site's API on its host, e.g. /api/docs
	Label  string // the site's name in titles, e.g. Docs
	Home   Home
	Docs   *productdocs.Index
	Spec   *publicapi.Spec // the endpoint reference; nil on the help site

	sites     *Sites
	endpoints []endpointGroup
}

// Home is the heading of a site's home page.
type Home struct {
	Title       string
	Description string
}

// Page is one rendered page.
type Page struct {
	Title       string // the full browser title, e.g. "Projects · Docs · OpenLoc"
	Description string
	Canonical   string
	Body        template.HTML
}

const appTitle = "OpenLoc"

// Sites is the set of documentation sites, which link to each other.
type Sites struct {
	app   string // the app's origin, for links into the app
	sites []*Site
}

// New links the sites together. app is the app's own origin.
func New(app string, sites ...*Site) *Sites {
	s := &Sites{app: strings.TrimRight(app, "/"), sites: sites}
	for _, site := range sites {
		site.Host = strings.ToLower(site.Host)
		site.sites = s
		if site.Spec != nil {
			site.endpoints = endpointGroups(site.Spec)
		}
	}
	return s
}

// ForHost is the site served on host, or nil.
func (s *Sites) ForHost(host string) *Site {
	if s == nil || host == "" {
		return nil
	}
	for _, site := range s.sites {
		if site.Host != "" && site.Origin != "" && site.Host == strings.ToLower(host) {
			return site
		}
	}
	return nil
}

// href maps a link in a page to its address as read on site: the site's own
// pages lose the prefix, the other site's pages go to that site's address,
// links into the app go to the app, and screenshots to the site's API.
func (site *Site) href(h string) string {
	for _, other := range site.sites.sites {
		rest, ok := underPrefix(h, other.Prefix)
		if !ok {
			continue
		}
		if other == site {
			return rest
		}
		if other.Host != "" && other.Origin != "" {
			return other.Origin + rest
		}
		return site.sites.app + h
	}
	switch {
	case strings.HasPrefix(h, "assets/"):
		return site.API + "/" + h
	case strings.HasPrefix(h, "/") && !strings.HasPrefix(h, "//") && !strings.HasPrefix(h, "/api/"):
		return site.sites.app + h
	}
	return h
}

// underPrefix maps a path under prefix (followed by /, ?, # or nothing) to
// its path at the site's root.
func underPrefix(p, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(p, prefix)
	if !ok || (rest != "" && !strings.ContainsAny(rest[:1], "/?#")) {
		return "", false
	}
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}
	return rest, true
}

func title(parts ...string) string {
	return strings.Join(append(parts, appTitle), " · ")
}
