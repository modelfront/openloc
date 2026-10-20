import { render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DocPage, DocSection } from '@/types/productdocs'

const api = vi.hoisted(() => ({
  getDocsTree: vi.fn(),
  getDocPage: vi.fn(),
  searchDocs: vi.fn(),
}))

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, ...api }
})

import DocsPage from './DocsPage'
import { formatDocDate } from './DocsArticle'

const tree: DocSection[] = [
  {
    slug: 'guides', title: 'Guides', summary: 'How-to.', order: 20, body: '',
    pages: [
      { slug: 'guides/cat-editor', title: 'The CAT editor', summary: 'Translate segments.', section: 'guides', order: 20, updated_at: '2026-09-08', stub: false },
      { slug: 'guides/templates', title: 'Templates', summary: 'Reuse setups.', section: 'guides', order: 80, updated_at: '2026-09-08', stub: true },
    ],
  },
]

const page: DocPage = {
  slug: 'guides/cat-editor', title: 'The CAT editor', summary: 'Translate segments.', section: 'guides',
  section_title: 'Guides', order: 20, updated_at: '2026-09-08', stub: false,
  body: '## Confirm a segment\n\nPress **Ctrl+Enter**.\n\n![Editor](assets/cat-editor.png)\n\n## Tags\n\nChips.',
  related: [{ slug: 'guides/templates', title: 'Templates', summary: 'Reuse setups.', section: 'guides', order: 80, updated_at: '2026-09-08', stub: true }],
}

function renderAt(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/docs/*" element={<DocsPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  api.getDocsTree.mockResolvedValue(tree)
  api.getDocPage.mockResolvedValue(page)
})

describe('DocsPage', () => {
  it('scrolls to the heading named in the URL hash once the article has loaded', async () => {
    const scrolled: string[] = []
    const spy = vi.spyOn(Element.prototype, 'scrollIntoView').mockImplementation(function (this: Element) {
      scrolled.push(this.id)
    })
    renderAt('/docs/guides/cat-editor#tags')
    // Two awaits deep — the page query resolves, THEN the markdown renders the
    // heading — so the default 1s budget is the one thing here that a loaded
    // 8-worker run can miss. The assertion below is what the test is about.
    await screen.findByRole('heading', { name: /^Tags/ }, { timeout: 3000 })
    // The browser's own hash jump ran before the article existed; the page
    // must repeat it against the now-rendered heading.
    await waitFor(() => expect(scrolled).toContain('tags'))
    spy.mockRestore()
  })

  it('shows the home view with every section and page card', async () => {
    renderAt('/docs')
    expect(await screen.findByTestId('docs-home')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Docs' })).toBeInTheDocument()
    // Sidebar and cards both list the page; the stub is marked "Soon".
    expect(screen.getAllByRole('link', { name: /The CAT editor/ }).length).toBeGreaterThan(0)
    expect(screen.getAllByText('Soon').length).toBeGreaterThan(0)
    expect(api.getDocsTree).toHaveBeenCalledWith('/docs')
  })

  it('lists the home page sections under "On this page"', async () => {
    renderAt('/docs')
    const toc = await screen.findByRole('navigation', { name: 'On this page' })
    const link = within(toc).getByRole('link', { name: 'Guides' })
    expect(link).toHaveAttribute('href', '#docs-section-guides')
    expect(document.getElementById('docs-section-guides')).not.toBeNull()
  })

  it('renders a section landing page for a one-segment slug', async () => {
    renderAt('/docs/guides')
    expect(await screen.findByTestId('docs-section')).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Docs/Guides')
    expect(api.getDocPage).not.toHaveBeenCalled()
  })

  it('names the section in the browser tab', async () => {
    document.title = 'OpenLoc'
    renderAt('/docs/guides')
    await screen.findByTestId('docs-section')
    expect(document.title).toBe('Guides · Docs · OpenLoc')
  })

  it('renders an article with breadcrumb, title, updated date, body, image and related links', async () => {
    renderAt('/docs/guides/cat-editor')
    expect(await screen.findByTestId('docs-article')).toBeInTheDocument()
    expect(api.getDocPage).toHaveBeenCalledWith('guides/cat-editor', '/docs')
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Docs/Guides')
    expect(screen.getByRole('heading', { level: 1, name: 'The CAT editor' })).toBeInTheDocument()
    expect(screen.getByTestId('docs-updated')).toHaveTextContent(`Updated ${formatDocDate('2026-09-08')}`)
    expect(screen.getByRole('heading', { level: 2, name: /Confirm a segment/ })).toHaveAttribute('id', 'confirm-a-segment')
    expect(screen.getByRole('img', { name: 'Editor' })).toHaveAttribute('src', '/api/docs/assets/cat-editor.png')
    expect(screen.getByRole('link', { name: 'Templates' })).toHaveAttribute('href', '/docs/guides/templates')
    // "On this page" lists both h2s.
    const toc = screen.getByTestId('docs-toc')
    expect(toc).toHaveTextContent('Confirm a segment')
    expect(toc).toHaveTextContent('Tags')
  })

  it('tells the reader when a page is not written yet', async () => {
    api.getDocPage.mockResolvedValue({ ...page, slug: 'guides/templates', title: 'Templates', stub: true, body: '', related: [] })
    renderAt('/docs/guides/templates')
    expect(await screen.findByText(/This page is being written/)).toBeInTheDocument()
    expect(screen.queryByTestId('docs-toc')).not.toBeInTheDocument()
  })

  it('shows a not-found message for a 404 without retrying', async () => {
    const { ApiError } = await vi.importActual<typeof import('@/api')>('@/api')
    api.getDocPage.mockRejectedValue(new ApiError(404, 'page not found'))
    renderAt('/docs/guides/nope')
    expect(await screen.findByText(/does not exist or is not available for your role/)).toBeInTheDocument()
    await waitFor(() => expect(api.getDocPage).toHaveBeenCalledTimes(1))
  })

  it('shows a not-found message for an unknown section', async () => {
    renderAt('/docs/nope')
    expect(await screen.findByText('This docs section does not exist.')).toBeInTheDocument()
  })
})

describe('formatDocDate', () => {
  it('formats YYYY-MM-DD from its parts so the day never shifts with the timezone', () => {
    expect(formatDocDate('2026-09-08')).toMatch(/8/)
    expect(formatDocDate('2026-09-08')).toMatch(/2026/)
    expect(formatDocDate('garbage')).toBe('garbage')
  })
})
