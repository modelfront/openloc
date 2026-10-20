import { useId, useMemo, useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { NavLink, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { API_SPEC_PATH, getAPISpec } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { endpointGroups, type Endpoint, type EndpointGroup } from '@/lib/openapi'
import { cn } from '@/lib/utils'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { EmptyState } from '@/components/layout/EmptyState'
import { Chip } from '@/components/ui/chip'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import DocsPage, { type DocsExtension } from '@/pages/docs/DocsPage'
import { API_DOCS_SET, useDocsSet, type DocsSet } from '@/pages/docs/docs-set'
import { useAppConfig } from '@/pages/docs/DocsHost'
import { DocsCards, docsSectionId } from '@/pages/docs/DocsSectionIndex'
import { docsNavLinkClassName, docsNavSectionClassName } from '@/pages/docs/docs-card'
import { EndpointDoc } from './EndpointDoc'
import { methodColor } from './method-color'
import { usePageTitle } from '../../hooks/usePageTitle'
import { TextLink } from '@/components/ui/text-link'

// The endpoint reference lives under the set's route base: /api-docs/endpoints
// in the app, /endpoints at the root of the developer host.
const endpointsPath = (routeBase: string) => `${routeBase}/endpoints`
const useEndpointsPath = () => endpointsPath(useDocsSet().routeBase)

// A fixed-width slot so the endpoint names line up whatever the method.
function MethodLabel({ method }: { method: string }) {
  return (
    <span className="flex w-16 shrink-0">
      <Chip size="small" color={methodColor(method)}>
        {method}
      </Chip>
    </span>
  )
}

const NAV_TITLE = 'API documentation'

// One endpoint group of the sidebar, folded unless the caller opens it.
function EndpointGroupNav({ group, open, onToggle }: { group: EndpointGroup; open: boolean; onToggle: () => void }) {
  const listId = useId()
  const endpoints = useEndpointsPath()
  return (
    <li>
      <DisclosureToggle open={open} onToggle={onToggle} controls={listId} className="w-full px-2 py-1 text-sm">
        {group.tag}
      </DisclosureToggle>
      {open && (
        <ul id={listId} className="ml-3 space-y-0.5 border-l border-gray-200 pl-2 dark:border-gray-800">
          {group.endpoints.map(e => (
            <li key={e.id}>
              <NavLink to={`${endpoints}/${e.id}`} className={({ isActive }) => cn(docsNavLinkClassName(isActive), 'flex items-center gap-2')}>
                <MethodLabel method={e.method} />
                <span className="truncate">{e.summary}</span>
              </NavLink>
            </li>
          ))}
        </ul>
      )}
    </li>
  )
}

// The endpoint reference in the sidebar, after the guide sections: one
// section whose groups fold. The group of the open endpoint is unfolded.
function EndpointNav({ groups }: { groups: EndpointGroup[] }) {
  const { pathname } = useLocation()
  const endpoints = useEndpointsPath()
  const activeTag = groups.find(g => g.endpoints.some(e => pathname === `${endpoints}/${e.id}`))?.tag
  const [open, setOpen] = useState<Set<string>>(() => new Set(activeTag ? [activeTag] : []))
  useOnChange([activeTag], () => {
    if (activeTag) setOpen(prev => (prev.has(activeTag) ? prev : new Set(prev).add(activeTag)))
  })
  const toggle = (tag: string) =>
    setOpen(prev => {
      const next = new Set(prev)
      if (!next.delete(tag)) next.add(tag)
      return next
    })

  if (groups.length === 0) return null
  return (
    <div data-testid="api-endpoint-nav">
      <NavLink to={endpoints} className={({ isActive }) => docsNavSectionClassName(isActive)}>
        {NAV_TITLE}
      </NavLink>
      <ul className="space-y-0.5">
        {groups.map(g => (
          <EndpointGroupNav key={g.tag} group={g} open={open.has(g.tag)} onToggle={() => toggle(g.tag)} />
        ))}
      </ul>
      <TextLink href={API_SPEC_PATH} download="openapi.json" className="mt-3 block px-2 text-xs">
        Download openapi.json
      </TextLink>
    </div>
  )
}

// One card per endpoint group, opening the group's first endpoint.
function GroupCards({ groups }: { groups: EndpointGroup[] }) {
  const endpoints = useEndpointsPath()
  return (
    <DocsCards
      cards={groups.map(g => ({
        to: `${endpoints}/${g.endpoints[0].id}`,
        title: g.tag,
        summary: `${g.endpoints.length} endpoint${g.endpoints.length === 1 ? '' : 's'}`,
      }))}
    />
  )
}

const OVERVIEW_SUMMARY = 'Every endpoint, by group. Each page lists who may call it, its fields and errors, and request samples.'

// The endpoint groups on the home page, after the guide sections.
const ENDPOINTS_SECTION = 'endpoints'
function EndpointCards({ groups }: { groups: EndpointGroup[] }) {
  const endpoints = useEndpointsPath()
  if (groups.length === 0) return null
  return (
    <section className="mt-10" aria-labelledby={docsSectionId(ENDPOINTS_SECTION)}>
      <div className="mb-3 flex items-baseline justify-between gap-4">
        <h2 id={docsSectionId(ENDPOINTS_SECTION)} className="text-lg font-medium text-gray-900 dark:text-gray-100">
          <NavLink to={endpoints} className="hover:underline">{NAV_TITLE}</NavLink>
        </h2>
      </div>
      <GroupCards groups={groups} />
    </section>
  )
}

// The landing page of the endpoint reference, shaped like a guide section's.
function EndpointOverview({ groups }: { groups: EndpointGroup[] }) {
  usePageTitle(NAV_TITLE)
  const { label, routeBase } = useDocsSet()
  return (
    <div className="max-w-3xl" data-testid="api-endpoint-overview">
      <Breadcrumbs items={[{ label, to: routeBase || '/' }, { label: NAV_TITLE }]} />
      <p className="text-base text-gray-500 dark:text-gray-400">{OVERVIEW_SUMMARY}</p>
      <div className="mt-6">
        <GroupCards groups={groups} />
      </div>
    </div>
  )
}

const matches = (e: Endpoint, q: string) => `${e.method} ${e.path} ${e.summary} ${e.tag}`.toLowerCase().includes(q.toLowerCase())

// The API docs: guides from the server's second documentation set and the
// endpoint reference read from the OpenAPI document, in one sidebar and one
// search. Endpoints live at <routeBase>/endpoints/<operationId>. Access scope:
// anyone, signed in or not; each endpoint lists the roles that may call it.
export default function ApiDocsPage({ set = API_DOCS_SET }: { set?: DocsSet }) {
  const { data: config } = useAppConfig()
  // Request samples call the app's own address, not the docs host's.
  const apiBase = config?.app_url ? new URL(config.app_url).origin : window.location.origin
  const endpoints = endpointsPath(set.routeBase)
  const { data: spec, isLoading, error } = useQuery({
    queryKey: queryKeys.docs.apiSpec(),
    queryFn: getAPISpec,
    staleTime: 5 * 60_000,
  })
  const groups = useMemo(() => (spec ? endpointGroups(spec) : []), [spec])

  const extension = useMemo<DocsExtension>(() => {
    const all = groups.flatMap(g => g.endpoints)
    return {
      nav: <EndpointNav groups={groups} />,
      home: <EndpointCards groups={groups} />,
      homeToc: groups.length > 0 ? [{ level: 2, text: NAV_TITLE, id: docsSectionId(ENDPOINTS_SECTION) }] : [],
      search: q =>
        all
          .filter(e => matches(e, q))
          .map(e => ({ key: `endpoint-${e.id}`, title: e.summary, subtitle: `${e.method} /api/v1${e.path}`, to: `${endpoints}/${e.id}` })),
      route: slug => {
        const [head, id, ...rest] = slug.split('/')
        if (head !== 'endpoints' || rest.length > 0) return undefined
        if (isLoading) return <LoadingPlaceholder />
        if (error || !spec) return <EmptyState message="The endpoint list could not be loaded. Try again in a moment." />
        if (!id) return groups.length > 0 ? <EndpointOverview groups={groups} /> : <EmptyState message="No endpoints yet." />
        const i = all.findIndex(e => e.id === id)
        if (i < 0) return <EmptyState message="This endpoint does not exist." />
        return <EndpointDoc endpoint={all[i]} prev={all[i - 1]} next={all[i + 1]} docsBase={set.routeBase} baseURL={apiBase} />
      },
    }
  }, [groups, spec, isLoading, error, endpoints, set.routeBase, apiBase])

  return <DocsPage set={set} extension={extension} />
}
