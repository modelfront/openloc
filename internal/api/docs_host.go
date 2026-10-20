package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// docSite is one public documentation site: the pages the app serves under
// prefix, moved to the root of their own host.
type docSite struct {
	host   string   // e.g. docs.openloc.com; empty = the site stays at prefix
	prefix string   // the pages' path on the app, e.g. /docs
	api    []string // the API paths the site's pages read
}

// docSites lists the two documentation sites: the user help (DOCS_HOST) and
// the API docs (DEVELOPER_HOST).
func docSites(docsHost, developerHost string) []docSite {
	return []docSite{
		{host: strings.ToLower(docsHost), prefix: "/docs", api: []string{"/api/docs"}},
		{host: strings.ToLower(developerHost), prefix: "/api-docs", api: []string{"/api/api-docs"}},
	}
}

// docSitesOnly gives each documentation site one address, the root of its
// host:
//   - On any other host, a page under the site's prefix moves there for
//     good, without the prefix: /docs/guides/x → <docs origin>/guides/x.
//   - On the site's host, every page path is the SPA, which shows the site at
//     the root. The site's API, /api/config and the error-notice proxy are
//     served; any other API path, /mcp and /oauth are a 404.
//
// A site without a host stays at its prefix on every host.
func docSitesOnly(baseURL string, sites []docSite) func(http.Handler) http.Handler {
	type live struct {
		docSite
		origin string
	}
	var active []live
	for _, s := range sites {
		if origin := hostOrigin(baseURL, s.host); origin != "" {
			active = append(active, live{s, origin})
		}
	}
	return func(next http.Handler) http.Handler {
		if len(active) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := requestHost(r)
			for _, s := range active {
				page, ok := underPrefix(r.URL.Path, s.prefix)
				if !ok || host == s.host {
					continue
				}
				target := s.origin + page
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusMovedPermanently)
				return
			}
			for _, s := range active {
				if host == s.host && !servedOnSite(r.URL.Path, s.api) {
					http.NotFound(w, r)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// hostOrigin is BASE_URL's scheme and port on host. Empty when there is no
// host or BASE_URL does not parse.
func hostOrigin(baseURL, host string) string {
	if host == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	h := strings.ToLower(host)
	if port := u.Port(); port != "" {
		h = net.JoinHostPort(h, port)
	}
	return u.Scheme + "://" + h
}

// underPrefix maps a page path under prefix to its path at the site's root.
func underPrefix(p, prefix string) (string, bool) {
	if p == prefix {
		return "/", true
	}
	if rest, ok := strings.CutPrefix(p, prefix+"/"); ok {
		return "/" + rest, true
	}
	return "", false
}

// servedOnSite reports what a documentation host serves besides its pages:
// the site's API, the SPA's bootstrap config, the error-notice proxy and every
// non-API path (pages, static files, probes, robots.txt).
func servedOnSite(p string, api []string) bool {
	for _, a := range api {
		if _, ok := underPrefix(p, a); ok {
			return true
		}
	}
	switch {
	case p == "/api/config", p == errtrackNoticePath:
		return true
	case strings.HasPrefix(p, "/api/"), p == "/mcp", strings.HasPrefix(p, "/oauth/"):
		return false
	}
	return true
}

// requestHost is the request's host without the port, lowercased.
func requestHost(r *http.Request) string {
	h := r.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(h)
}

// onHost reports whether the request came in on host (empty never matches).
func onHost(r *http.Request, host string) bool {
	return host != "" && requestHost(r) == strings.ToLower(host)
}
