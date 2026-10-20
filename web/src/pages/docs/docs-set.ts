import { createContext, useContext } from 'react'
import { USER_DOCS_API } from '@/api'

/**
 * One documentation set rendered by DocsPage: where its pages come from, the
 * route it is mounted under, and the words that name it on screen.
 */
export type DocsSet = {
  /** Mount point of the docs API, relative to /api. */
  apiBase: string
  /** Route prefix the set's pages are mounted under; empty at the root (the docs host). */
  routeBase: string
  /** Maps a Markdown link in a page before it renders (the docs host rewrites /docs paths). */
  linkHref?: (href: string) => string
  /** The root breadcrumb. */
  label: string
  /** Title of the set's home page. */
  homeHeader: { title: string; subtitle: string }
  copy: {
    loadError: string
    pageNotFound: string
    pageLoadError: string
    sectionNotFound: string
    searchTitle: string
    searchDescription: string
    searchPlaceholder: string
    noResults: string
  }
}

export const USER_DOCS_SET: DocsSet = {
  apiBase: USER_DOCS_API,
  routeBase: '/docs',
  label: 'Docs',
  homeHeader: { title: 'Docs', subtitle: 'How OpenLoc works, one topic at a time.' },
  copy: {
    loadError: 'The docs could not be loaded. Try again in a moment.',
    pageNotFound: 'This docs page does not exist or is not available for your role.',
    pageLoadError: 'The docs page could not be loaded. Try again in a moment.',
    sectionNotFound: 'This docs section does not exist.',
    searchTitle: 'Search docs',
    searchDescription: 'Type to search the docs.',
    searchPlaceholder: 'Search docs…',
    noResults: 'No docs pages match.',
  },
}

export const API_DOCS_SET: DocsSet = {
  apiBase: '/api-docs',
  routeBase: '/api-docs',
  label: 'API',
  homeHeader: { title: 'API', subtitle: 'Connect your own systems to OpenLoc with an access key.' },
  copy: {
    loadError: 'The API docs could not be loaded. Try again in a moment.',
    pageNotFound: 'This guide does not exist.',
    pageLoadError: 'The guide could not be loaded. Try again in a moment.',
    sectionNotFound: 'This guide section does not exist.',
    searchTitle: 'Search API docs',
    searchDescription: 'Type to search the guides and endpoints.',
    searchPlaceholder: 'Search guides and endpoints…',
    noResults: 'Nothing matches.',
  },
}

export const DocsSetContext = createContext<DocsSet>(USER_DOCS_SET)

export const useDocsSet = () => useContext(DocsSetContext)
