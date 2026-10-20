import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getDocsTree } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { PageContainer } from '@/components/layout/PageContainer'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { EmptyState } from '@/components/layout/EmptyState'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { DocsSidebar } from './DocsSidebar'
import { DocsArticle } from './DocsArticle'
import { DocsSearch, type ExtraSearchHit } from './DocsSearch'
import { DocsHome, DocsSectionIndex, docsSectionId } from './DocsSectionIndex'
import { TocNav, WithToc, type TocItem } from './DocsToc'
import { DocsSetContext, USER_DOCS_SET, type DocsSet } from './docs-set'

/**
 * Pages a set adds beside its Markdown pages — the API docs add the endpoint
 * reference, read from the OpenAPI document — sharing the sidebar, search and
 * layout.
 */
export type DocsExtension = {
  /** Sidebar groups after the set's own sections. */
  nav: ReactNode
  /** Content for a slug the extension owns, or undefined for the set's own pages. */
  route: (slug: string) => ReactNode | undefined
  /** Shown on the home page after the sections. */
  home?: ReactNode
  /** "On this page" entries for what `home` adds. */
  homeToc?: TocItem[]
  /** Search hits the server does not know about. */
  search?: (query: string) => ExtraSearchHit[]
}

type Props = {
  /** The documentation set to show. */
  set?: DocsSet
  extension?: DocsExtension
}

// In-app documentation: the user help at /docs/* and the API docs at
// /api-docs/*. Access scope: every role — the server filters pages per role,
// so a linguist sees a smaller tree, not a redirect.
//
// Layout: the docs variant of PageContainer (full height, centered with room
// at the sides), with the sidebar and the content column scrolling
// independently.
export default function DocsPage({ set = USER_DOCS_SET, extension }: Props) {
  const { apiBase, routeBase, label, copy } = set
  const { '*': splat = '' } = useParams()
  const slug = splat.replace(/^\/+|\/+$/g, '')
  const [searchOpen, setSearchOpen] = useState(false)

  // A new page starts at the top, not where the previous one was scrolled to.
  const contentRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (contentRef.current) contentRef.current.scrollTop = 0
  }, [slug])

  const { data: tree, isLoading, error } = useQuery({
    queryKey: queryKeys.docs.tree(apiBase),
    queryFn: () => getDocsTree(apiBase),
    staleTime: 5 * 60_000,
  })

  // Cmd/Ctrl+K opens search. Registered only while this page is mounted, so
  // it never competes with the CAT editor's own shortcuts.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setSearchOpen(o => !o)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const extensionContent = slug ? extension?.route(slug) : undefined
  let content: React.ReactNode
  if (extensionContent !== undefined) {
    content = extensionContent
  } else if (isLoading) {
    content = <LoadingPlaceholder />
  } else if (error || !tree) {
    content = <EmptyState message={copy.loadError} />
  } else {
    const parts = slug ? slug.split('/') : []
    if (parts.length === 0) {
      const sections: TocItem[] = tree.map(s => ({ level: 2, text: s.title, id: docsSectionId(s.slug) }))
      content = (
        <WithToc toc={<TocNav headings={[...sections, ...(extension?.homeToc ?? [])]} />}>
          <div className="min-w-0 max-w-5xl flex-1">
            <DocsHome tree={tree} routeBase={routeBase} />
            {extension?.home}
          </div>
        </WithToc>
      )
    } else if (parts.length === 1) {
      const section = tree.find(s => s.slug === parts[0])
      content = section ? (
        <DocsSectionIndex section={section} routeBase={routeBase} />
      ) : (
        <div>
          <Breadcrumbs items={[{ label, to: routeBase || '/' }, { label: 'Not found' }]} />
          <EmptyState message={copy.sectionNotFound} />
        </div>
      )
    } else {
      content = <DocsArticle slug={slug} apiBase={apiBase} routeBase={routeBase} />
    }
  }

  return (
    <DocsSetContext.Provider value={set}>
      <PageContainer width="docs">
        <div className="flex min-h-0 flex-1 gap-8">
          <aside className="hidden w-60 shrink-0 overflow-y-auto pb-6 pr-1 md:block" data-testid="docs-sidebar">
            {tree && <DocsSidebar tree={tree} routeBase={routeBase} onOpenSearch={() => setSearchOpen(true)} after={extension?.nav} />}
          </aside>
          <div ref={contentRef} className="min-w-0 flex-1 overflow-y-auto pb-24" data-testid="docs-content">
            {content}
          </div>
        </div>
        <DocsSearch
          open={searchOpen}
          onOpenChange={setSearchOpen}
          apiBase={apiBase}
          routeBase={routeBase}
          extraHits={extension?.search}
        />
      </PageContainer>
    </DocsSetContext.Provider>
  )
}
