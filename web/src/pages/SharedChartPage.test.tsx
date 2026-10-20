// A chart opened by share token. The reader may have no
// access to the dashboard behind it, so these tests pin what the page must NOT
// offer as much as what it shows.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return { ...actual, getSharedChart: vi.fn(), getSharedChartData: vi.fn() }
})

import { getSharedChart, getSharedChartData } from '../api'
import { SharedChartPage } from './SharedChartPage'

const chartMock = vi.mocked(getSharedChart)
const dataMock = vi.mocked(getSharedChartData)

const TOKEN = 'LB7KzXqiK0VQSDaHrBahQ8UIUHu1_80Eauhe2WqPyFQ' // gitleaks:allow

function renderPage(path = `/shared/charts/${TOKEN}`) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/shared/charts/:token" element={<SharedChartPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  chartMock.mockResolvedValue({
    title: 'Words processed per week',
    definition: { measure: 'words_processed', dimensions: ['week'], last_days: 90 },
    viz: { type: 'area', x_key: 'week', series: ['words'] },
    dashboard: 'My experiments',
    filters: { controls: ['date_range', 'unit', 'granularity', 'customer'] },
    available_controls: ['date_range', 'granularity', 'unit', 'customer'],
  })
  dataMock.mockResolvedValue({
    measure: 'words_processed',
    definition: 'Source words a human confirmed or edited',
    columns: ['week', 'words'],
    rows: [{ week: '2026-09-14', words: 120 }],
    x_key: 'week',
  })
})

describe('SharedChartPage', () => {
  it('shows the chart and names where it came from', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByText('Words processed per week')).toBeInTheDocument())
    expect(screen.getByText(/Shared from My experiments/)).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('shared-chart')).toBeInTheDocument())
  })

  it('reads the chart by token, not by id', async () => {
    renderPage()
    await waitFor(() => expect(chartMock).toHaveBeenCalledWith(TOKEN))
    // The data query is gated on the chart read, so it lands one tick later.
    await waitFor(() =>
      expect(dataMock).toHaveBeenCalledWith(TOKEN, expect.objectContaining({ last_days: 90 })),
    )
  })

  // The sender's selection travels in the link, exactly as on the owner's page.
  it('applies the selection carried by the link', async () => {
    renderPage(`/shared/charts/${TOKEN}?last_days=30&grain=month&unit=characters`)
    await waitFor(() => expect(dataMock).toHaveBeenCalled())
    expect(dataMock).toHaveBeenCalledWith(TOKEN, expect.objectContaining({
      last_days: 30, grain: 'month', unit: 'characters',
    }))
  })

  // The page must not become a way INTO a dashboard the reader cannot browse:
  // no trail, no link to the dashboard, no rename, no delete.
  it('offers no navigation into the dashboard and no edit affordances', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByText('Words processed per week')).toBeInTheDocument())
    expect(screen.queryByRole('navigation', { name: 'Breadcrumb' })).not.toBeInTheDocument()
    expect(screen.queryAllByRole('link')).toHaveLength(0)
    expect(screen.queryByTestId('rename-chart')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Delete chart')).not.toBeInTheDocument()
    expect(screen.queryByTestId('chart-edit')).not.toBeInTheDocument()
  })

  // Download and share sit above the chart, where the dashboard's chart page
  // puts them. Share hands on THIS link, selection included, and opens no
  // dialog — there is nothing for the reader to create or revoke.
  it('puts download and a copy-link share above the chart', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    renderPage(`/shared/charts/${TOKEN}?last_days=30`)
    await waitFor(() => expect(screen.getByTestId('chart-share')).toBeInTheDocument())
    const exp = screen.getByTestId('chart-export')
    const share = screen.getByTestId('chart-share')
    expect(exp.parentElement).toBe(share.parentElement)
    expect(exp.compareDocumentPosition(share) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByTestId('shared-chart')).not.toContainElement(exp)

    fireEvent.click(share)
    await waitFor(() => expect(writeText).toHaveBeenCalled())
    const copied = new URL(writeText.mock.calls[0][0])
    expect(copied.pathname).toBe(`/shared/charts/${TOKEN}`)
    expect(copied.searchParams.get('last_days')).toBe('30')
    expect(screen.queryByTestId('share-link')).not.toBeInTheDocument()
  })

  // Dimension options come from a dashboard-scoped endpoint the reader may not
  // be allowed to call, so those controls are left out rather than shown broken.
  it('shows only the controls that need no dashboard lookup', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('dashboard-filter-date-range')).toBeInTheDocument())
    expect(screen.getByTestId('dashboard-filter-unit')).toBeInTheDocument()
    expect(screen.getByTestId('dashboard-filter-granularity')).toBeInTheDocument()
    expect(screen.queryByTestId('dashboard-filter-customer')).not.toBeInTheDocument()
  })

  // Revoked, mistyped, or another tenant's — one message, so a token cannot be
  // probed for which kind of wrong it is.
  it('explains a dead link without hinting at what went wrong', async () => {
    chartMock.mockRejectedValue(new Error('not found'))
    renderPage()
    await waitFor(() => expect(screen.getByText(/no longer available/)).toBeInTheDocument())
    expect(screen.queryByText(/My experiments/)).not.toBeInTheDocument()
  })
})
