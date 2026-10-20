import { useMemo } from 'react'
import ApiDocsPage from './ApiDocsPage'
import { DocsLayout } from '@/pages/docs/DocsLayout'
import { siteHref, useAppConfig } from '@/pages/docs/DocsHost'
import { API_DOCS_SET, type DocsSet } from '@/pages/docs/docs-set'

/**
 * The API docs without a session: every guide (each lists all four roles) and
 * the endpoint reference. `atRoot`: on the developer host, where they sit at
 * the root, not under /api-docs.
 */
export default function PublicApiDocsPage({ atRoot = false }: { atRoot?: boolean }) {
  const { data: config } = useAppConfig()
  const set = useMemo<DocsSet>(
    () => (atRoot ? { ...API_DOCS_SET, routeBase: '', linkHref: config ? siteHref('developer', config) : undefined } : API_DOCS_SET),
    [atRoot, config],
  )
  return (
    <DocsLayout signedIn={false}>
      <ApiDocsPage set={set} />
    </DocsLayout>
  )
}
