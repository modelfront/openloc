import { act, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DocSection } from '@/types/productdocs'

const api = vi.hoisted(() => ({
  getDocsTree: vi.fn(),
  getDocPage: vi.fn(),
  searchDocs: vi.fn(),
  getAppConfig: vi.fn(),
}))

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, ...api }
})

import PublicDocsPage from './PublicDocsPage'
import DocsPage from './DocsPage'
import { DocsLayout } from './DocsLayout'

const tree: DocSection[] = [
  {
    slug: 'guides', title: 'Guides', summary: 'How-to.', order: 20, body: '',
    pages: [
      { slug: 'guides/cat-editor', title: 'The CAT editor', summary: 'Translate segments.', section: 'guides', order: 20, updated_at: '2026-09-08', stub: false },
    ],
  },
]

function renderAt(element: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/docs']}>
        <Routes>
          <Route path="/docs/*" element={element} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  api.getDocsTree.mockResolvedValue(tree)
  api.getAppConfig.mockResolvedValue({ app_url: 'https://app.example.com/', docs_host: false, docs_url: '' })
})

describe('PublicDocsPage', () => {
  it('shows the docs with an Open app link', async () => {
    renderAt(<PublicDocsPage />)
    await screen.findByTestId('docs-home')
    await waitFor(() => expect(screen.getByText('Open app').closest('a')).toHaveAttribute('href', 'https://app.example.com/'))
    expect(screen.getByRole('link', { name: 'OpenLoc' })).toHaveAttribute('href', 'https://openloc.com')
    expect(screen.getByTitle(/^Theme:/)).toBeInTheDocument()
    for (const link of [screen.getByText('Open app').closest('a'), screen.getByRole('link', { name: 'OpenLoc' })]) {
      expect(link).toHaveAttribute('target', '_blank')
      expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    }
  })
})

describe('DocsLayout', () => {
  it('signed in, leads back into the app instead of Open app', async () => {
    renderAt(<DocsLayout signedIn><DocsPage /></DocsLayout>)
    await screen.findByTestId('docs-home')
    expect(screen.getByText('Back to app').closest('a')).toHaveAttribute('href', '/')
    expect(screen.getByTitle(/^Theme:/)).toBeInTheDocument()
    expect(screen.queryByText('Open app')).toBeNull()
  })
})

describe('theme switch on the public docs', () => {
  it('recolors the page at once, without asking the server for the appearance', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({ matches: false, media: query, addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {} }))
    const { ThemeProvider } = await import('next-themes')
    const appearance = await import('@/api/appearance')
    const fetchAppearance = vi.spyOn(appearance, 'getAppearance')
    renderAt(
      <ThemeProvider attribute="class" defaultTheme="light" enableSystem={false} storageKey="test-theme">
        <PublicDocsPage />
      </ThemeProvider>,
    )
    await screen.findByTestId('docs-home')
    const background = () => document.documentElement.style.getPropertyValue('--background')
    await waitFor(() => expect(background()).not.toBe(''))
    const light = background()
    act(() => screen.getByTitle(/^Theme:/).click())
    await waitFor(() => expect(background()).not.toBe(light))
    expect(fetchAppearance).not.toHaveBeenCalled()
  })
})

describe('PublicDocsPage on the docs host', () => {
  function renderRootAt(path: string) {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="*" element={<PublicDocsPage atRoot />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
  }

  it('serves the help at the root, without /docs', async () => {
    renderRootAt('/')
    await screen.findByTestId('docs-home')
    const card = screen.getAllByRole('link').find(a => a.textContent?.includes('The CAT editor'))
    expect(card).toHaveAttribute('href', '/guides/cat-editor')
  })

  it('keeps help links on the docs host and sends app links to the app', async () => {
    api.getDocPage.mockResolvedValue({
      slug: 'guides/cat-editor', title: 'The CAT editor', summary: 'Translate segments.', section: 'guides',
      section_title: 'Guides', order: 20, updated_at: '2026-09-08', stub: false, related: [],
      body: 'See [roles](/docs/reference/roles) and [your projects](/projects).',
    })
    renderRootAt('/guides/cat-editor')
    const roles = await screen.findByRole('link', { name: 'roles' })
    expect(roles).toHaveAttribute('href', '/reference/roles')
    await waitFor(() =>
      expect(screen.getByRole('link', { name: 'your projects' })).toHaveAttribute('href', 'https://app.example.com/projects'),
    )
    expect(screen.getByRole('link', { name: 'Docs' })).toHaveAttribute('href', '/')
  })
})
