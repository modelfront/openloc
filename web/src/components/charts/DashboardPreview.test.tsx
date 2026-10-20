// The list card's chart previews. recharts measures 0×0 in jsdom, so the
// charts are asserted through their data requests, not pixels.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, getChartData: vi.fn() }
})

import { getChartData, type DashboardPreviewChart } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { DashboardPreview } from './DashboardPreview'

const getChartDataMock = vi.mocked(getChartData)

const chart = (id: number): DashboardPreviewChart => ({
  id, title: `Chart ${id}`, viz: { type: 'bar', x_key: 'week', series: ['words'] },
})

function renderPreview(charts: DashboardPreviewChart[]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <DashboardPreview charts={charts} />
    </QueryClientProvider>,
  )
  return qc
}

// An observer that reports every element as on screen the moment it is watched.
function everythingInView() {
  vi.stubGlobal('IntersectionObserver', class {
    cb: IntersectionObserverCallback
    constructor(cb: IntersectionObserverCallback) { this.cb = cb }
    observe(el: Element) {
      this.cb([{ isIntersecting: true, target: el } as IntersectionObserverEntry], this as unknown as IntersectionObserver)
    }
    unobserve() {}
    disconnect() {}
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  getChartDataMock.mockResolvedValue({
    measure: 'words_processed', definition: '', columns: ['week', 'words'],
    rows: [{ week: '2026-09-01', words: 10 }], x_key: 'week',
  })
})
afterEach(() => vi.unstubAllGlobals())

describe('DashboardPreview', () => {
  // A long list must not fire four requests per card on open.
  it('fetches nothing until the card comes into view', async () => {
    renderPreview([chart(11), chart(12)])
    expect(screen.getAllByTestId('dashboard-preview-tile')).toHaveLength(2)
    await new Promise(r => setTimeout(r, 20))
    expect(getChartDataMock).not.toHaveBeenCalled()
  })

  // The same question the dashboard page asks when opened without filters, so
  // opening a previewed dashboard is served from the cache.
  it('asks each chart under the dashboard page default selection', async () => {
    everythingInView()
    const qc = renderPreview([chart(11), chart(12), chart(13)])
    await waitFor(() => expect(getChartDataMock).toHaveBeenCalledTimes(3))
    for (const id of [11, 12, 13]) {
      expect(getChartDataMock).toHaveBeenCalledWith(id, expect.objectContaining({ last_days: 90, grain: 'week' }))
      await waitFor(() => expect(qc.getQueryData(queryKeys.dashboards.chartData(id, '?last_days=90&grain=week'))).toBeDefined())
    }
    // Three charts, three tiles — the fourth slot stays empty, as on the page.
    expect(screen.getAllByTestId('dashboard-preview-tile')).toHaveLength(3)
    expect(screen.getByText('Chart 13')).toBeInTheDocument()
  })

  it('says a dashboard has no charts rather than drawing an empty frame', () => {
    renderPreview([])
    expect(screen.queryByTestId('dashboard-preview-tile')).not.toBeInTheDocument()
    expect(screen.getByText('No charts yet')).toBeInTheDocument()
  })

  it('marks a chart with nothing in the period', async () => {
    everythingInView()
    getChartDataMock.mockResolvedValue({ measure: 'm', definition: '', columns: [], rows: [], x_key: 'week' })
    renderPreview([chart(11)])
    await waitFor(() => expect(screen.getByText('No data')).toBeInTheDocument())
  })
})
