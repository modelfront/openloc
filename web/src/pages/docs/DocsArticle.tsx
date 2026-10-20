import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useLocation } from 'react-router-dom'
import { ApiError, docsAssetBase, getDocPage } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { Markdown } from '@/components/markdown/Markdown'
import { DocsToc, WithToc } from './DocsToc'
import { useAppConfig } from './DocsHost'
import { useDocsSet } from './docs-set'
import { usePageTitle } from '../../hooks/usePageTitle'
import { TextLink } from '@/components/ui/text-link'

type Props = { slug: string; apiBase: string; routeBase: string }

/** `2026-09-08` → "8 Sept 2026", built from the parts so no timezone shifts the day. */
export function formatDocDate(iso: string): string {
  const [y, m, d] = iso.split('-').map(Number)
  if (!y || !m || !d) return iso
  return new Date(y, m - 1, d).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

// One documentation page. The breadcrumb stops at the section (both crumbs are
// links); the page's own title is the article h1 — so nothing repeats and the
// "breadcrumb replaces the title" convention still holds.
export function DocsArticle({ slug, apiBase, routeBase }: Props) {
  const { label, copy, linkHref } = useDocsSet()
  // Examples in a page write {app_url} for the address the app runs on.
  const { data: config } = useAppConfig()
  const appUrl = (config?.app_url || window.location.origin).replace(/\/+$/, '')
  const { data: page, isLoading, error } = useQuery({
    queryKey: queryKeys.docs.page(apiBase, slug),
    queryFn: () => getDocPage(slug, apiBase),
    retry: (count, err) => !(err instanceof ApiError && err.status === 404) && count < 2,
  })
  usePageTitle(page?.title, label)

  // Deep links (`…/cat-editor#confirm-a-segment`): the browser's own hash jump
  // runs before the article has loaded, and a router navigation never scrolls
  // at all — so once the body is rendered, jump to the named heading.
  const { hash } = useLocation()
  useEffect(() => {
    if (!page || page.stub || !hash) return
    document.getElementById(decodeURIComponent(hash.slice(1)))?.scrollIntoView({ block: 'start' })
  }, [page, hash])

  if (isLoading) return <LoadingPlaceholder />
  if (error || !page) {
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <div>
        <Breadcrumbs items={[{ label, to: routeBase || '/' }, { label: 'Not found' }]} />
        <EmptyState message={notFound ? copy.pageNotFound : copy.pageLoadError} />
      </div>
    )
  }

  return (
    <WithToc toc={!page.stub && <DocsToc markdown={page.body} />}>
      <article className="min-w-0 max-w-5xl flex-1" data-testid="docs-article">
        <Breadcrumbs
          items={[
            { label, to: routeBase || '/' },
            { label: page.section_title, to: `${routeBase}/${page.section}` },
          ]}
        />
        <div>
          <h1 className="text-2xl font-normal text-gray-900 dark:text-gray-100">{page.title}</h1>
          <p className="mt-2 text-base text-gray-500 dark:text-gray-400">{page.summary}</p>
        </div>
        <p className="mt-3 text-xs text-gray-400 dark:text-gray-500" data-testid="docs-updated">
          Updated <time dateTime={page.updated_at}>{formatDocDate(page.updated_at)}</time>
        </p>
        <hr className="my-6 border-gray-200 dark:border-gray-800" />
        {page.stub ? (
          <EmptyState message="This page is being written. Check back soon." />
        ) : (
          <Markdown variant="docs" assetBase={docsAssetBase(apiBase)} linkHref={linkHref}>
            {page.body.replaceAll('{app_url}', appUrl)}
          </Markdown>
        )}
        {page.related.length > 0 && (
          <section className="mt-12 border-t border-gray-200 pt-6 dark:border-gray-800" aria-labelledby="docs-related">
            <h2 id="docs-related" className="mb-3 text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">Related</h2>
            <ul className="space-y-1.5">
              {page.related.map(r => (
                <li key={r.slug}>
                  <TextLink to={`${routeBase}/${r.slug}`} className="text-sm">
                    {r.title}
                  </TextLink>
                  <span className="ml-2 text-sm text-gray-500 dark:text-gray-400">{r.summary}</span>
                </li>
              ))}
            </ul>
          </section>
        )}
      </article>
    </WithToc>
  )
}
