import { describe, expect, it, vi } from 'vitest'
import type { AppConfig } from '@/api'

const api = vi.hoisted(() => ({ getAppConfig: vi.fn() }))

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, ...api }
})

import { siteHref, siteLink, sitePath } from './DocsHost'

const config = (over: Partial<AppConfig> = {}): AppConfig => ({
  app_url: 'https://app.example.com/',
  docs_url: 'https://docs.example.com',
  docs_host: false,
  developer_url: 'https://developer.example.com',
  developer_host: false,
  playground: false,
  issues: false,
  ...over,
})

describe('sitePath', () => {
  it('drops the prefix', () => {
    expect(sitePath('/docs', '/docs')).toBe('/')
    expect(sitePath('/docs/', '/docs')).toBe('/')
    expect(sitePath('/docs/guides/cat-editor', '/docs')).toBe('/guides/cat-editor')
    expect(sitePath('/docs#top', '/docs')).toBe('/#top')
    expect(sitePath('/api-docs/endpoints/getJob', '/api-docs')).toBe('/endpoints/getJob')
  })
})

describe('siteHref', () => {
  it('on the docs host: own pages at the root, API docs on the developer host, app links on the app', () => {
    const href = siteHref('docs', config())
    expect(href('/docs/reference/roles')).toBe('/reference/roles')
    expect(href('/docs')).toBe('/')
    expect(href('/api-docs/endpoints/getJob')).toBe('https://developer.example.com/endpoints/getJob')
    expect(href('/projects')).toBe('https://app.example.com/projects')
    expect(href('/docsearch')).toBe('https://app.example.com/docsearch')
  })
  it('on the developer host: own pages at the root, help on the docs host', () => {
    const href = siteHref('developer', config())
    expect(href('/api-docs/getting-started/authentication')).toBe('/getting-started/authentication')
    expect(href('/docs/guides/mcp')).toBe('https://docs.example.com/guides/mcp')
    expect(href('/ai-settings')).toBe('https://app.example.com/ai-settings')
  })
  it('sends the other site to the app when it has no host of its own', () => {
    const href = siteHref('docs', config({ developer_url: '' }))
    expect(href('/api-docs/endpoints/getJob')).toBe('https://app.example.com/api-docs/endpoints/getJob')
  })
  it('leaves anchors, external links and API paths alone', () => {
    const href = siteHref('docs', config())
    expect(href('#tags')).toBe('#tags')
    expect(href('https://example.com/x')).toBe('https://example.com/x')
    expect(href('/api/docs/assets/a.png')).toBe('/api/docs/assets/a.png')
  })
})

describe('siteLink', () => {
  it('opens a site on its own address when it has one', () => {
    expect(siteLink(config(), '/docs')).toBe('https://docs.example.com/')
    expect(siteLink(config(), '/docs/guides/mcp')).toBe('https://docs.example.com/guides/mcp')
    expect(siteLink(config(), '/api-docs')).toBe('https://developer.example.com/')
  })
  it('keeps the path when the site has no address, or the config is not loaded', () => {
    expect(siteLink(config({ developer_url: '' }), '/api-docs')).toBe('/api-docs')
    expect(siteLink(undefined, '/docs')).toBe('/docs')
    expect(siteLink(config(), '/projects')).toBe('/projects')
  })
})
