import { useMemo } from 'react'
import DocsPage from './DocsPage'
import { DocsLayout } from './DocsLayout'
import { siteHref, useAppConfig } from './DocsHost'
import { USER_DOCS_SET, type DocsSet } from './docs-set'

// The user help without a session: the pages every role may read.
const PUBLIC_DOCS_SET: DocsSet = {
  ...USER_DOCS_SET,
  copy: { ...USER_DOCS_SET.copy, pageNotFound: 'This docs page does not exist.' },
}

/** `atRoot`: on the docs host, where the help sits at the root, not under /docs. */
export default function PublicDocsPage({ atRoot = false }: { atRoot?: boolean }) {
  const { data: config } = useAppConfig()
  const set = useMemo<DocsSet>(
    () => (atRoot ? { ...PUBLIC_DOCS_SET, routeBase: '', linkHref: config ? siteHref('docs', config) : undefined } : PUBLIC_DOCS_SET),
    [atRoot, config],
  )
  return (
    <DocsLayout signedIn={false}>
      <DocsPage set={set} />
    </DocsLayout>
  )
}
