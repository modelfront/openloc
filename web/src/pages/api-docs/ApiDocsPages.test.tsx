import { fireEvent, render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DocSection } from '@/types/productdocs'
import type { OpenAPIDoc } from '@/lib/openapi'

const api = vi.hoisted(() => ({
  getDocsTree: vi.fn(),
  getDocPage: vi.fn(),
  searchDocs: vi.fn(),
  getAPISpec: vi.fn(),
  getAppConfig: vi.fn(),
}))

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, ...api }
})

import ApiDocsPage from './ApiDocsPage'
import PublicApiDocsPage from './PublicApiDocsPage'

const spec: OpenAPIDoc = {
  tags: [{ name: 'Jobs' }],
  paths: {
    '/jobs/{jobID}/export': {
      get: {
        operationId: 'exportJob',
        summary: 'Download the translated file',
        tags: ['Jobs'],
        'x-roles': ['owner'],
        'x-order': 2,
        responses: { '200': { description: 'The file.', content: { 'application/octet-stream': { schema: { type: 'string', format: 'binary' } } } } },
      },
    },
    '/jobs/{jobID}': {
      get: {
        operationId: 'getJob',
        summary: 'Get a job',
        description: 'A linguist may read jobs assigned to them.',
        tags: ['Jobs'],
        'x-roles': ['project_manager', 'linguist'],
        'x-order': 1,
        parameters: [{ name: 'jobID', in: 'path', required: true, schema: { type: 'integer' } }],
        responses: {
          '200': {
            description: 'The job.',
            content: { 'application/json': { schema: { type: 'object', properties: { data: { $ref: '#/components/schemas/Job' } } } } },
          },
          '404': { description: 'Not in your organization.' },
        },
      },
    },
  },
  components: {
    schemas: {
      Job: {
        type: 'object',
        required: ['id'],
        properties: {
          file_name: { type: 'string' },
          id: { type: 'integer' },
          step: { type: 'object', properties: { assignee_id: { type: 'integer' } } },
        },
      },
    },
  },
}

const guides: DocSection[] = [
  {
    slug: 'getting-started', title: 'Getting started', summary: 'First call.', order: 10, body: '',
    pages: [{ slug: 'getting-started/authentication', title: 'Authentication', summary: 'Keys.', section: 'getting-started', order: 10, updated_at: '2026-10-05', stub: false }],
  },
]

function renderAt(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/api-docs/*" element={<ApiDocsPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  api.getAPISpec.mockResolvedValue(spec)
  api.getDocsTree.mockResolvedValue(guides)
  api.getAppConfig.mockResolvedValue({ app_url: 'https://app.example.com', docs_url: '', docs_host: false, developer_url: '', developer_host: false })
})

describe('API endpoints page', () => {
  it('lands on an overview: one card per group, opening its first endpoint', async () => {
    renderAt('/api-docs/endpoints')
    const overview = await screen.findByTestId('api-endpoint-overview')
    expect(within(overview).getByRole('link', { name: /Jobs/ })).toHaveAttribute('href', '/api-docs/endpoints/getJob')
    expect(screen.queryByRole('heading', { name: 'Get a job', level: 1 })).not.toBeInTheDocument()
  })

  it('shows one endpoint: roles, parameters, response fields, errors and examples', async () => {
    renderAt('/api-docs/endpoints/getJob')
    const endpoint = await screen.findByTestId('api-endpoint')
    // Roles are shown as labels, never raw values.
    expect(within(endpoint).getByText('Project manager')).toBeInTheDocument()
    expect(within(endpoint).queryByText('project_manager')).not.toBeInTheDocument()
    expect(within(endpoint).getByRole('region', { name: 'Path parameters' })).toBeInTheDocument()
    expect(within(endpoint).getByTestId('api-request-bar').textContent).toContain('GET/api/v1/jobs/{jobID}')
    expect(within(endpoint).getByText('jobID')).toBeInTheDocument()
    expect(within(endpoint).getByText('Not in your organization.')).toBeInTheDocument()
    expect(screen.getByTestId('api-request-samples').textContent).toContain('--request GET')
    const responses = screen.getByTestId('api-response-samples')
    expect(within(responses).getByRole('tab', { name: '200' })).toBeInTheDocument()
    expect(responses.textContent).toContain('"file_name": "<string>"')
    // The open endpoint's group is unfolded, and its link is current.
    const sidebar = screen.getByTestId('docs-sidebar')
    expect(within(sidebar).getByRole('button', { name: 'Jobs' })).toHaveAttribute('aria-expanded', 'true')
    expect(within(sidebar).getByRole('link', { name: /Get a job/ })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: /Download the translated file →/ })).toHaveAttribute('href', '/api-docs/endpoints/exportJob')
  })

  it('folds nested fields until asked', async () => {
    renderAt('/api-docs/endpoints/getJob')
    await screen.findByTestId('api-endpoint')
    fireEvent.click(screen.getByRole('button', { name: 'Show 3 fields' }))
    // The jobID parameter, then data and its fields: required first, then the spec order.
    const rows = screen.getAllByTestId('api-field')
    const names = rows.map(li => li.querySelector('.font-mono')?.textContent)
    expect(names.slice(0, 5)).toEqual(['jobID', 'data', 'id', 'file_name', 'step'])
    // A request parameter the caller must send says Required; a response
    // field the server always sends has no label, and one it may leave out
    // says Optional.
    expect(within(rows[0]).getByText('Required')).toBeInTheDocument()
    expect(within(rows[2]).queryByText('Required')).not.toBeInTheDocument()
    expect(within(rows[2]).queryByText('Optional')).not.toBeInTheDocument()
    expect(within(rows[3]).getByText('Optional')).toBeInTheDocument()
  })

  it('says a file endpoint returns the file', async () => {
    renderAt('/api-docs/endpoints/exportJob')
    expect(await screen.findByText('The file itself (application/octet-stream).')).toBeInTheDocument()
  })

  it('says so when the spec cannot be loaded, or the endpoint does not exist', async () => {
    renderAt('/api-docs/endpoints/nope')
    expect(await screen.findByText('This endpoint does not exist.')).toBeInTheDocument()
    api.getAPISpec.mockRejectedValue(new Error('boom'))
    renderAt('/api-docs/endpoints')
    expect(await screen.findByText('The endpoint list could not be loaded. Try again in a moment.')).toBeInTheDocument()
  })
})

describe('one API docs page', () => {
  it('lists guide sections and endpoint groups in one sidebar', async () => {
    renderAt('/api-docs')
    expect(await screen.findByTestId('docs-home')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'API', level: 1 })).toBeInTheDocument()
    const sidebar = screen.getByTestId('docs-sidebar')
    expect(within(sidebar).getByRole('link', { name: 'Authentication' })).toHaveAttribute('href', '/api-docs/getting-started/authentication')
    expect(within(sidebar).getByText('Getting started')).toBeInTheDocument()
    // Endpoint groups sit under one section and start folded.
    const nav = await within(sidebar).findByTestId('api-endpoint-nav')
    expect(within(nav).getByRole('link', { name: 'API documentation' })).toHaveAttribute('href', '/api-docs/endpoints')
    const jobs = within(nav).getByRole('button', { name: 'Jobs' })
    expect(jobs).toHaveAttribute('aria-expanded', 'false')
    expect(within(nav).queryByRole('link', { name: /Get a job/ })).not.toBeInTheDocument()
    fireEvent.click(jobs)
    expect(within(nav).getByRole('link', { name: /Get a job/ })).toHaveAttribute('href', '/api-docs/endpoints/getJob')
    fireEvent.click(jobs)
    expect(within(nav).queryByRole('link', { name: /Get a job/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'Section' })).not.toBeInTheDocument()
    expect(api.getDocsTree).toHaveBeenCalledWith('/api-docs')
  })

  it('shows the endpoint groups on the home page', async () => {
    renderAt('/api-docs')
    const cards = await screen.findByRole('region', { name: 'API documentation' })
    expect(within(cards).getByRole('link', { name: /Jobs/ })).toHaveAttribute('href', '/api-docs/endpoints/getJob')
  })

  it('finds endpoints in the docs search', async () => {
    api.searchDocs.mockResolvedValue([])
    renderAt('/api-docs/endpoints/getJob')
    await screen.findByTestId('api-endpoint')
    fireEvent.click(screen.getByTestId('docs-search-trigger'))
    fireEvent.change(await screen.findByTestId('docs-search-input'), { target: { value: 'export' } })
    const results = screen.getByTestId('docs-search-results')
    expect(await within(results).findByText('Download the translated file')).toBeInTheDocument()
    expect(within(results).getByText('GET /api/v1/jobs/{jobID}/export')).toBeInTheDocument()
  })
})

describe('API docs at the root of the developer host', () => {
  function renderRootAt(path: string) {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="*" element={<PublicApiDocsPage atRoot />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
  }

  it('links endpoints without /api-docs and samples call the app address', async () => {
    renderRootAt('/endpoints/getJob')
    const endpoint = await screen.findByTestId('api-endpoint')
    expect(endpoint.textContent).toContain('https://app.example.com/api/v1/jobs/')
    expect(screen.getByRole('link', { name: 'Download openapi.json' })).toHaveAttribute('href', '/api/api-docs/openapi.json')
    const nav = screen.getByTestId('api-endpoint-nav')
    expect(within(nav).getByRole('link', { name: 'API documentation' })).toHaveAttribute('href', '/endpoints')
    expect(screen.getByText('Open app').closest('a')).toHaveAttribute('href', 'https://app.example.com/')
  })
})
