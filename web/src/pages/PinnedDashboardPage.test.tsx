// A pinned dashboard holds references: every edit here goes to the person's
// own pinned endpoints, and every chart opens under its original dashboard.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { menuItem } from '@/test/menu'

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getPinnedDashboard: vi.fn(),
    listPinnedDashboards: vi.fn(),
    getPinnedFilterOptions: vi.fn(),
    getFilterOptions: vi.fn(),
    getChartData: vi.fn(),
    updatePinnedFilters: vi.fn(),
    updateDashboardFilters: vi.fn(),
    removeDashboardFilter: vi.fn(),
    renamePinnedDashboard: vi.fn(),
    unpinChart: vi.fn(),
    movePinnedChart: vi.fn(),
  }
})

import {
  getPinnedDashboard, listPinnedDashboards, getPinnedFilterOptions, getFilterOptions, getChartData,
  updatePinnedFilters, updateDashboardFilters, removeDashboardFilter, renamePinnedDashboard, unpinChart, movePinnedChart,
} from '../api'
import { PinnedDashboardPage } from './PinnedDashboardPage'

const detailMock = vi.mocked(getPinnedDashboard)
const listMock = vi.mocked(listPinnedDashboards)
const pinnedOptionsMock = vi.mocked(getPinnedFilterOptions)
const optionsMock = vi.mocked(getFilterOptions)
const chartDataMock = vi.mocked(getChartData)
const filtersMock = vi.mocked(updatePinnedFilters)
const renameMock = vi.mocked(renamePinnedDashboard)
const unpinMock = vi.mocked(unpinChart)
const moveMock = vi.mocked(movePinnedChart)

const chart = (id: number, title: string, dashboardId: number, dashboardName: string) => ({
  id,
  title,
  definition: { measure: 'words_processed', last_days: 30 },
  viz: { type: 'bar' as const, x_key: 'week', series: ['words'] },
  controls: { controls: [] },
  available_controls: ['date_range', 'customer'],
  position: 1,
  dashboard_id: dashboardId,
  dashboard_name: dashboardName,
  dashboard_personal: false,
})

const PINNED = {
  id: 7,
  name: 'My morning',
  created_at: '2026-10-15T00:00:00Z',
  filters: { controls: ['customer'] },
  available_controls: ['date_range', 'customer'],
  charts: [chart(11, 'Weekly words', 1, 'Operations'), chart(21, 'QA issues', 2, 'Quality')],
}

function Where() {
  const { pathname, search } = useLocation()
  return <p data-testid="where">{pathname}{search}</p>
}

function renderPage(path = '/dashboards/pinned/7') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/dashboards/pinned/:pinnedId" element={<><PinnedDashboardPage /><Where /></>} />
          <Route path="/dashboards/pinned" element={<Where />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  detailMock.mockResolvedValue(PINNED)
  listMock.mockResolvedValue([
    { id: 7, name: 'My morning', chart_count: 2, preview_charts: [] },
    { id: 8, name: 'Quality', chart_count: 1, preview_charts: [] },
  ])
  pinnedOptionsMock.mockResolvedValue({ customer: ['Acme', 'Globex'] })
  chartDataMock.mockResolvedValue({
    measure: 'words_processed', definition: 'Words', columns: ['week', 'words'],
    rows: [{ week: '2026-10-01', words: 10 }], x_key: 'week',
  })
  filtersMock.mockResolvedValue()
  renameMock.mockResolvedValue()
  unpinMock.mockResolvedValue()
  moveMock.mockResolvedValue()
})

const card = async (title: string) =>
  (await screen.findAllByTestId('dashboard-chart-card')).find(c => within(c).queryByText(title))!

describe('PinnedDashboardPage', () => {
  it('sits under Pinned in the trail', async () => {
    renderPage()
    expect(await screen.findByRole('link', { name: 'Pinned' })).toHaveAttribute('href', '/dashboards/pinned')
    expect(screen.getByRole('link', { name: 'Dashboards' })).toHaveAttribute('href', '/dashboards')
  })

  // A chart opens on its original page, so an edit there changes the original.
  it('opens each chart under the dashboard it lives on, and names that dashboard', async () => {
    renderPage('/dashboards/pinned/7?customer=Acme')
    const words = await card('Weekly words')
    expect(within(words).getByTestId('chart-open-link').getAttribute('href')).toMatch(/^\/dashboards\/1\/charts\/11\?.*customer=Acme/)
    expect(within(words).getByRole('link', { name: 'Operations' })).toHaveAttribute('href', '/dashboards/1')
    const qa = await card('QA issues')
    expect(within(qa).getByTestId('chart-open-link').getAttribute('href')).toMatch(/^\/dashboards\/2\/charts\/21\?/)
  })

  // The charts are not this dashboard's: no edit or delete on them here.
  it('offers pin, download and move, but no chart edit or delete', async () => {
    renderPage()
    const words = await card('Weekly words')
    expect(within(words).getByRole('button', { name: 'Unpin Weekly words' })).toBeInTheDocument()
    expect(within(words).getByTestId('chart-export')).toBeInTheDocument()
    await waitFor(() => expect(within(words).getByTestId('pinned-chart-move')).toBeInTheDocument())
    expect(within(words).queryByTestId('chart-edit')).not.toBeInTheDocument()
    expect(within(words).queryByLabelText('Delete chart')).not.toBeInTheDocument()
  })

  it('unpins a chart and stays while others are left', async () => {
    renderPage()
    fireEvent.click(within(await card('Weekly words')).getByRole('button', { name: 'Unpin Weekly words' }))
    await waitFor(() => expect(unpinMock).toHaveBeenCalledWith(11))
    expect(screen.getByTestId('where')).toHaveTextContent('/dashboards/pinned/7')
  })

  // The server removes a pinned dashboard once its last chart leaves, so the
  // page goes where that chart went rather than to a page that is gone.
  it('returns to Pinned after unpinning the last chart', async () => {
    detailMock.mockResolvedValue({ ...PINNED, charts: [PINNED.charts[0]!] })
    renderPage()
    fireEvent.click(within(await card('Weekly words')).getByRole('button', { name: 'Unpin Weekly words' }))
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/dashboards\/pinned$/))
  })

  it('follows the last chart to the pinned dashboard it moved to', async () => {
    detailMock.mockResolvedValue({ ...PINNED, charts: [PINNED.charts[0]!] })
    renderPage()
    const words = await card('Weekly words')
    await waitFor(() => expect(within(words).getByTestId('pinned-chart-move')).toBeInTheDocument())
    fireEvent.click(within(words).getByTestId('pinned-chart-move'))
    fireEvent.click(await menuItem('pinned-chart-move-to-8'))
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/dashboards\/pinned\/8/))
  })

  it('moves a chart to another pinned dashboard, never offering this one', async () => {
    renderPage()
    const words = await card('Weekly words')
    await waitFor(() => expect(within(words).getByTestId('pinned-chart-move')).toBeInTheDocument())
    fireEvent.click(within(words).getByTestId('pinned-chart-move'))
    expect(await menuItem('pinned-chart-move-to-8')).toBeInTheDocument()
    expect(screen.queryByTestId('pinned-chart-move-to-7')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('pinned-chart-move-to-8'))
    await waitFor(() => expect(moveMock).toHaveBeenCalledWith(11, 8))
  })

  // Removing a control freezes nothing — the charts are someone else's — so it
  // goes at once, and its selection leaves the URL with it.
  it('removes a filter without freezing it into the originals', async () => {
    renderPage('/dashboards/pinned/7?customer=Globex')
    fireEvent.click(await screen.findByTestId('rename-dashboard'))
    fireEvent.click(await menuItem('rename-dashboard-filter-customer'))
    await waitFor(() => expect(filtersMock).toHaveBeenCalledWith(7, { controls: [] }))
    expect(screen.queryByTestId('rename-dashboard-remove-confirm')).not.toBeInTheDocument()
    expect(screen.getByTestId('where').textContent).not.toMatch(/customer=/)
    expect(vi.mocked(removeDashboardFilter)).not.toHaveBeenCalled()
  })

  it('adds a filter to the pinned dashboard only', async () => {
    detailMock.mockResolvedValue({ ...PINNED, filters: { controls: [] } })
    renderPage()
    fireEvent.click(await screen.findByTestId('rename-dashboard'))
    fireEvent.click(await menuItem('rename-dashboard-filter-date-range'))
    await waitFor(() => expect(filtersMock).toHaveBeenCalledWith(7, { controls: ['date_range'] }))
    expect(vi.mocked(updateDashboardFilters)).not.toHaveBeenCalled()
  })

  it('reads its dropdown options from the pinned dashboard', async () => {
    renderPage()
    await waitFor(() => expect(pinnedOptionsMock).toHaveBeenCalledWith(7, expect.any(Number)))
    expect(optionsMock).not.toHaveBeenCalled()
  })

  it('renames through the pinned endpoint', async () => {
    renderPage()
    fireEvent.click(await screen.findByTestId('rename-dashboard'))
    fireEvent.click(await menuItem('rename-dashboard-rename'))
    const field = await screen.findByDisplayValue('My morning')
    fireEvent.change(field, { target: { value: 'Mornings' } })
    fireEvent.keyDown(field, { key: 'Enter' })
    await waitFor(() => expect(renameMock).toHaveBeenCalledWith(7, 'Mornings'))
  })

  it('says so when the pinned dashboard is gone', async () => {
    detailMock.mockRejectedValue(new Error('not found'))
    renderPage()
    expect(await screen.findByText('Pinned dashboard not found.')).toBeInTheDocument()
  })
})
