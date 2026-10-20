import { request } from './request'
import type { DocPage, DocSearchHit, DocSection } from '../types/productdocs'
import type { OpenAPIDoc } from '@/lib/openapi'

// In-app documentation. The API base is a parameter so both sets — the user
// help (/docs) and the API guides (/api-docs) — share these fetchers.
export const USER_DOCS_API = '/docs'

export const getDocsTree = (base = USER_DOCS_API) =>
  request<{ data: DocSection[] }>(base).then(r => r.data)

export const getDocPage = (slug: string, base = USER_DOCS_API) =>
  request<{ data: DocPage }>(`${base}/${slug}`).then(r => r.data)

export const searchDocs = (q: string, base = USER_DOCS_API) =>
  request<{ data: DocSearchHit[] }>(`${base}/search?q=${encodeURIComponent(q)}`).then(r => r.data)

/** Absolute URL prefix the Markdown renderer resolves relative images against. */
export const docsAssetBase = (base = USER_DOCS_API) => `/api${base}/assets`

/** Startup settings: the app's address and each documentation site's (see pages/docs/DocsHost). */
export type AppConfig = {
  /** The running build, for issue reports. */
  version?: string
  app_url: string
  docs_url: string
  docs_host: boolean
  developer_url: string
  developer_host: boolean
  /** The Playground is on: the UI offers it and its saved tours. */
  playground: boolean
  /** Users may report issues: the deployment has platform admins to read them. */
  issues: boolean
}
export const getAppConfig = () => request<AppConfig>('/config')

/** The customer API's OpenAPI document, the public copy the API docs read (no access key). */
export const API_SPEC_PATH = '/api/api-docs/openapi.json'
export const getAPISpec = () => request<OpenAPIDoc>('/api-docs/openapi.json')
