package api

import (
	"net/http"

	"openloc/internal/docseo"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// The documentation hosts (open) are public sites: search engines and AI
// crawlers may index them. Every other host, the app's /docs included, asks
// all of them to stay away.

// blockCrawlers asks crawlers not to index any page outside the open hosts.
func blockCrawlers(open []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !onAnyHost(r, open) {
				w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// robotsTxt allows every crawler on the open hosts, pointing them at the
// host's sitemap, and turns every crawler away elsewhere, AI scrapers by name.
func robotsTxt(baseURL string, open []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		body := disallowAll
		for _, h := range open {
			if onHost(r, h) {
				body = allowAll
				if origin := HostOrigin(baseURL, h); origin != "" {
					body += "\nSitemap: " + origin + "/sitemap.xml\n"
				}
				break
			}
		}
		w.Write([]byte(body))
	}
}

// sitemapXML lists a documentation host's public pages; 404 on any other host.
func sitemapXML(pages *docseo.Sites) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		site := pages.ForHost(RequestHost(r))
		if site == nil {
			http.NotFound(w, r)
			return
		}
		body, err := site.Sitemap()
		if err != nil {
			logging.Error(r.Context(), err, "sitemap failed to render", "docs-sitemap-failed")
			httputil.ServerError(w, "render", "sitemap")
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Write(body)
	}
}

func onAnyHost(r *http.Request, hosts []string) bool {
	for _, h := range hosts {
		if onHost(r, h) {
			return true
		}
	}
	return false
}

const allowAll = "User-agent: *\nAllow: /\n"

const disallowAll = "User-agent: *\nDisallow: /\n\n" +
	"# LLM scrapers\n" +
	"User-agent: GPTBot\nDisallow: /\n\n" +
	"User-agent: ChatGPT-User\nDisallow: /\n\n" +
	"User-agent: Google-Extended\nDisallow: /\n\n" +
	"User-agent: CCBot\nDisallow: /\n\n" +
	"User-agent: anthropic-ai\nDisallow: /\n\n" +
	"User-agent: Claude-Web\nDisallow: /\n\n" +
	"User-agent: Bytespider\nDisallow: /\n\n" +
	"User-agent: cohere-ai\nDisallow: /\n\n" +
	"User-agent: PerplexityBot\nDisallow: /\n\n" +
	"User-agent: FacebookBot\nDisallow: /\n"
