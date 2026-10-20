import { useQuery } from '@tanstack/react-query'
import { getAppConfig, type AppConfig } from '@/api'
import { queryKeys } from '@/lib/query-keys'

// Each documentation site has one address. With its host configured (docs_url
// or developer_url in /api/config) the site sits at that host's root:
// /docs/guides/x there is /guides/x, /api-docs/endpoints/y is /endpoints/y.
// The app links to it directly (siteLink); the server redirects a prefixed
// page on any other host. Whether a page is on a site's host is the server's
// call (docs_host, developer_host): BASE_URL need not match the address the
// app is served on.

export type DocSite = 'docs' | 'developer'

/** The site's pages on the app's own address. */
export const SITE_PREFIX: Record<DocSite, string> = { docs: '/docs', developer: '/api-docs' }

const siteUrl = (c: AppConfig, site: DocSite) => (site === 'docs' ? c.docs_url : c.developer_url)

export const useAppConfig = () =>
  useQuery({ queryKey: queryKeys.docs.appConfig(), queryFn: getAppConfig, staleTime: Infinity })

const underPrefix = (path: string, prefix: string) => new RegExp(`^${prefix}(?=$|[/?#])`).test(path)

/** The path of a page under `prefix` at its site's root. */
export function sitePath(path: string, prefix: string): string {
  const rest = underPrefix(path, prefix) ? path.slice(prefix.length) : path
  return rest.startsWith('/') ? rest : `/${rest}`
}

/**
 * Rewrites a page link on the `here` site's host: its own pages lose the
 * prefix, the other site's pages go to that site's address, and links into
 * the app (/projects) go to the app's own address.
 */
export function siteHref(here: DocSite, config: AppConfig): (href: string) => string {
  const app = config.app_url.replace(/\/+$/, '')
  return href => {
    for (const site of ['docs', 'developer'] as const) {
      const prefix = SITE_PREFIX[site]
      if (!underPrefix(href, prefix)) continue
      if (site === here) return sitePath(href, prefix)
      const url = siteUrl(config, site)
      return url ? url + sitePath(href, prefix) : app + href
    }
    if (href.startsWith('/') && !href.startsWith('//') && !href.startsWith('/api/')) return app + href
    return href
  }
}

/**
 * The address of a docs page (/docs/…, /api-docs/…): on its site's host when
 * the site has one, else the path itself. For links from the app, which open
 * the site directly.
 */
export function siteLink(config: AppConfig | undefined, path: string): string {
  for (const site of ['docs', 'developer'] as const) {
    const prefix = SITE_PREFIX[site]
    const url = config && siteUrl(config, site)
    if (url && underPrefix(path, prefix)) return url + sitePath(path, prefix)
  }
  return path
}
