// Single-chart permalink page. The page has no
// endpoint of its own — it reads the dashboard and picks one chart out — so
// these tests pin the two things that are genuinely new: the selection
// surviving the trip from the dashboard, and the link you hand to a colleague
// spelling that selection out.
//
// recharts measures 0×0 in jsdom, so the chart itself is asserted through the
// data request, not pixels.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getDashboard: vi.fn(),
    getChartData: vi.fn(),
    getFilterOptions: vi.fn(),
    getMe: vi.fn(),
    renameChart: vi.fn(),
    updateChartControls: vi.fn(),
    removeChartFilter: vi.fn(),
    shareChart: vi.fn(),
    revokeChartShare: vi.fn(),
    getPinState: vi.fn(),
    pinChart: vi.fn(),
    unpinChart: vi.fn(),
  }
})

import { getDashboard, getChartData, getFilterOptions, getMe, renameChart, shareChart, updateChartControls, getPinState, pinChart, unpinChart } from '../api'

const pinStateMock = vi.mocked(getPinState)
const pinChartMock = vi.mocked(pinChart)
const unpinChartMock = vi.mocked(unpinChart)
import { ChartPage } from './ChartPage'

const getDashboardMock = vi.mocked(getDashboard)
const getChartDataMock = vi.mocked(getChartData)
const getFilterOptionsMock = vi.mocked(getFilterOptions)
const getMeMock = vi.mocked(getMe)
const renameChartMock = vi.mocked(renameChart)
const shareChartMock = vi.mocked(shareChart)
const updateChartControlsMock = vi.mocked(updateChartControls)

const DASHBOARD = {
  id: 1,
  name: 'Operations',
  created_at: '2026-08-18T00:00:00Z',
  personal: false,
  filters: { controls: ['date_range', 'unit', 'granularity', 'customer'] },
  available_controls: ['date_range', 'unit', 'granularity', 'customer'],
  charts: [
    {
      id: 11,
      title: 'Words processed per week',
      definition: { measure: 'words_processed', dimensions: ['week'], last_days: 90 },
      viz: { type: 'area' as const, x_key: 'week', series: ['words'] },
      controls: { controls: [] },
      available_controls: ['date_range', 'unit', 'granularity', 'customer'],
      position: 1,
    },
  ],
}

function renderPage(path = '/dashboards/1/charts/11') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/dashboards/:dashboardId/charts/:chartId" element={<ChartPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  getDashboardMock.mockResolvedValue(DASHBOARD)
  getMeMock.mockResolvedValue({ role: 'owner' } as Awaited<ReturnType<typeof getMe>>)
  renameChartMock.mockResolvedValue()
  shareChartMock.mockResolvedValue('TOKEN123')
  updateChartControlsMock.mockResolvedValue()
  getFilterOptionsMock.mockResolvedValue({ customer: ['Acme'] })
  pinStateMock.mockResolvedValue({ chart_ids: [], dashboard_ids: [] })
  pinChartMock.mockResolvedValue()
  unpinChartMock.mockResolvedValue()
  getChartDataMock.mockResolvedValue({
    measure: 'words_processed',
    definition: 'Source words in segments that reached a translated state',
    columns: ['week', 'words', 'characters'],
    rows: [{ week: '2026-09-01', words: 120, characters: 700 }],
    x_key: 'week',
  })
})

describe('ChartPage', () => {
  it('shows the chart with its title, dashboard and measure definition', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByText('Words processed per week')).toBeInTheDocument())
    // The dashboard name appears in the breadcrumb trail.
    expect(screen.getAllByText('Operations').length).toBeGreaterThan(0)
    // A reader who arrived by link has none of the dashboard's context, so the
    // definition is on the page rather than in a tooltip. It rides the chart
    // DATA response, one query later than the title — so wait on it directly.
    await waitFor(() => expect(screen.getByText(/Source words in segments/)).toBeInTheDocument())
  })

  // The point of the whole feature: the selection the sender was looking at
  // must drive the request, not the chart's stored 90-day default.
  it('applies the filter selection from the URL', async () => {
    renderPage('/dashboards/1/charts/11?last_days=30&grain=month&unit=characters&customer=Acme')
    await waitFor(() => expect(getChartDataMock).toHaveBeenCalled())
    expect(getChartDataMock).toHaveBeenCalledWith(11, expect.objectContaining({
      last_days: 30, grain: 'month', unit: 'characters', customer: 'Acme',
    }))
  })

  it('falls back to the dashboard defaults when the URL carries no selection', async () => {
    renderPage()
    await waitFor(() => expect(getChartDataMock).toHaveBeenCalled())
    expect(getChartDataMock).toHaveBeenCalledWith(11, expect.objectContaining({
      last_days: 90, grain: 'week', unit: 'words',
    }))
  })


  // Share is the only way out of this page, so the link it produces has to
  // carry the selection — otherwise the recipient opens a different window than
  // the one the sender was looking at.
  it('share link carries the current filter selection', async () => {
    renderPage('/dashboards/1/charts/11?last_days=30&unit=characters')
    await waitFor(() => expect(screen.getByTestId('chart-share')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('chart-share'))

    await waitFor(() => expect(screen.getByTestId('share-link')).toBeInTheDocument())
    const shown = new URL(screen.getByTestId('share-link').textContent!)
    expect(shown.pathname).toBe('/shared/charts/TOKEN123')
    expect(shown.searchParams.get('last_days')).toBe('30')
    expect(shown.searchParams.get('unit')).toBe('characters')
  })

  // One way to hand this chart to someone, not two that behave differently.
  it('offers Share instead of a second copy-the-URL button', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('chart-share')).toBeInTheDocument())
    expect(screen.queryByTestId('chart-copy-link')).not.toBeInTheDocument()
  })


  // The chart's actions sit in one row, in the same order as on a card.
  it('puts download, share and edit in one row for a writer, and the pin before the title', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('chart-edit')).toBeInTheDocument())
    const ids = ['chart-export', 'chart-share', 'chart-edit']
    const [exp, share, edit] = ids.map(id => screen.getByTestId(id))
    // The pin belongs to the name, not to the actions.
    const pin = screen.getByTestId('pin-button')
    expect(pin.parentElement).not.toBe(exp.parentElement)
    expect(pin.nextElementSibling).toHaveTextContent('Words processed per week')
    expect(exp.parentElement).toBe(share.parentElement)
    expect(share.parentElement).toBe(edit.parentElement)
    expect(exp.compareDocumentPosition(share) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(share.compareDocumentPosition(edit) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // Nothing left inside the chart box: the row is the only place for them.
    expect(screen.getByTestId('chart-detail')).not.toContainElement(exp)
  })

  it('pins the chart, and unpins it once pinned', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Pin Words processed per week' }))
    await waitFor(() => expect(pinChartMock).toHaveBeenCalledWith(11))

    pinStateMock.mockResolvedValue({ chart_ids: [11], dashboard_ids: [] })
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Unpin Words processed per week' }))
    await waitFor(() => expect(unpinChartMock).toHaveBeenCalledWith(11))
  })

  // A reader who may not change the chart can still pass it on — as the page's
  // own URL, which opens only for people who can already reach it. No token is
  // created on their behalf.
  it('lets a reader copy the page link instead of creating a share link', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    renderPage('/dashboards/1/charts/11?last_days=30')
    await waitFor(() => expect(screen.getByTestId('chart-share')).toBeInTheDocument())
    expect(screen.queryByTestId('chart-edit')).not.toBeInTheDocument()
    expect(screen.getByTestId('chart-export')).toBeInTheDocument()

    fireEvent.click(screen.getByTestId('chart-share'))
    await waitFor(() => expect(writeText).toHaveBeenCalled())
    const copied = new URL(writeText.mock.calls[0][0])
    expect(copied.pathname).toBe('/dashboards/1/charts/11')
    expect(copied.searchParams.get('last_days')).toBe('30')
    expect(shareChartMock).not.toHaveBeenCalled()
    expect(screen.queryByTestId('share-link')).not.toBeInTheDocument()
  })

  // A colleague's personal dashboard 404s exactly like a deleted chart — the
  // read cannot tell them apart, on purpose. The message still has to explain
  // why a link someone sent might not open.
  it('explains a chart it cannot show instead of blanking', async () => {
    getDashboardMock.mockRejectedValue(new Error('not found'))
    renderPage()
    await waitFor(() => expect(screen.getByText(/Chart not found/)).toBeInTheDocument())
    expect(screen.getByText(/personal dashboard that only its owner can open/)).toBeInTheDocument()
  })

  // A permalink to a chart on someone's PERSONAL dashboard is a dead end for
  // everyone else — and must not become a map of it. The trail names the
  // dashboard and links into it, so it has to render AFTER the read succeeds,
  // never above the guard as a "show the crumbs while loading" convenience.
  it('renders no breadcrumbs at all when the chart cannot be read', async () => {
    getDashboardMock.mockRejectedValue(new Error('not found'))
    renderPage()
    await waitFor(() => expect(screen.getByText(/Chart not found/)).toBeInTheDocument())
    expect(screen.queryByRole('navigation', { name: 'Breadcrumb' })).not.toBeInTheDocument()
    // Nothing to climb: no link out of the page, and no dashboard name leaked.
    expect(screen.queryAllByRole('link')).toHaveLength(0)
    expect(screen.queryByText('Operations')).not.toBeInTheDocument()
  })

  it('says the same for a chart id that is not on this dashboard', async () => {
    renderPage('/dashboards/1/charts/999')
    await waitFor(() => expect(screen.getByText(/Chart not found/)).toBeInTheDocument())
    expect(getChartDataMock).not.toHaveBeenCalled()
  })

  it('renames the chart from its title', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('rename-chart')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('rename-chart'))
    const field = screen.getByLabelText('Rename')
    fireEvent.change(field, { target: { value: 'Weekly volume' } })
    fireEvent.keyDown(field, { key: 'Enter' })
    await waitFor(() => expect(renameChartMock).toHaveBeenCalledWith(11, 'Weekly volume'))
  })

  // The chart's gate is its dashboard's — this one is shared.
  it('hides the rename pencil from a non-admin', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    renderPage()
    await waitFor(() => expect(screen.getByText('Words processed per week')).toBeInTheDocument())
    expect(screen.queryByTestId('rename-chart')).not.toBeInTheDocument()
  })

  // Three crumbs: the list, the dashboard, this chart. The top one lands on
  // the tab the dashboard lives on.
  it('shows the full trail above the chart', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByRole('link', { name: 'Dashboards' })).toBeInTheDocument())
    expect(screen.getByRole('link', { name: 'Dashboards' })).toHaveAttribute('href', '/dashboards')
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Operations')
  })

  // The trail names the SPACE, so a reader can tell the org's dashboard from
  // someone's scratch copy at a glance.
  it('names the space in the trail', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByRole('link', { name: 'Organization dashboards' })).toBeInTheDocument())
    expect(screen.queryByRole('link', { name: 'My dashboards' })).not.toBeInTheDocument()
  })

  it('names My dashboards for a personal one, and links to that tab', async () => {
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, personal: true })
    renderPage()
    await waitFor(() =>
      expect(screen.getByRole('link', { name: 'My dashboards' })).toHaveAttribute('href', '/dashboards/mine'),
    )
  })

  // Filters are NOT editable here: this page merges the dashboard's controls
  // with the chart's, so a menu could not say which it was editing.
  it('offers rename only, with no filter editing', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('rename-chart')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('rename-chart'))
    expect(screen.getByLabelText('Rename')).toBeInTheDocument()
    expect(screen.queryByTestId('rename-chart-filter-customer')).not.toBeInTheDocument()
  })

  it('links back to the dashboard carrying the same selection', async () => {
    renderPage('/dashboards/1/charts/11?last_days=30&customer=Acme')
    await waitFor(() => expect(screen.getByRole('link', { name: /Operations/ })).toBeInTheDocument())
    const href = screen.getByRole('link', { name: /Operations/ }).getAttribute('href')!
    const back = new URL(href, 'http://localhost')
    expect(back.pathname).toBe('/dashboards/1')
    expect(back.searchParams.get('last_days')).toBe('30')
    expect(back.searchParams.get('customer')).toBe('Acme')
  })
})
