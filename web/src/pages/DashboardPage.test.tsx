// Dashboard page: cards fetch their own data; a failing
// chart degrades to an error card without touching its neighbors. recharts'
// ResponsiveContainer measures 0×0 in jsdom, so these tests pin the
// data→card plumbing; pixel truth lives in the E2E suite.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getMe: vi.fn(),
    getDashboard: vi.fn(),
    getChartData: vi.fn(),
    deleteChart: vi.fn(),
    updateChart: vi.fn(),
    renameDashboard: vi.fn(),
    getMetricsCatalog: vi.fn(),
    createChart: vi.fn(),
    getFilterOptions: vi.fn(),
    removeDashboardFilter: vi.fn(),
    removeChartFilter: vi.fn(),
    updateDashboardFilters: vi.fn(),
    updateChartControls: vi.fn(),
    getPinState: vi.fn(),
    pinChart: vi.fn(),
    unpinChart: vi.fn(),
  }
})

import { getMe, getDashboard, getChartData, getMetricsCatalog, createChart, updateChart, renameDashboard, getFilterOptions, removeDashboardFilter, removeChartFilter, updateDashboardFilters, updateChartControls, getPinState, pinChart, unpinChart, deleteChart } from '../api'

const pinStateMock = vi.mocked(getPinState)
const pinChartMock = vi.mocked(pinChart)
const unpinChartMock = vi.mocked(unpinChart)
import { DashboardPage } from './DashboardPage'

const getMeMock = vi.mocked(getMe)
const getDashboardMock = vi.mocked(getDashboard)
const getChartDataMock = vi.mocked(getChartData)
const catalogMock = vi.mocked(getMetricsCatalog)
const createChartMock = vi.mocked(createChart)
const updateChartMock = vi.mocked(updateChart)
const renameDashboardMock = vi.mocked(renameDashboard)
const optionsMock = vi.mocked(getFilterOptions)
const removeDashFilterMock = vi.mocked(removeDashboardFilter)
const removeChartFilterMock = vi.mocked(removeChartFilter)
const updateDashboardFiltersMock = vi.mocked(updateDashboardFilters)
const updateChartControlsMock = vi.mocked(updateChartControls)
const deleteChartMock = vi.mocked(deleteChart)

// A slice of what the server generates from the metrics registry — a few
// measures with disjoint dimensions, which is what the builder has to keep
// apart.
const CATALOG = {
  grains: ['day', 'week', 'month'],
  measures: [
    {
      name: 'qa_issues',
      definition: 'QA findings raised in the period.',
      dimensions: ['check_type', 'severity', 'customer'],
      values: [{ name: 'issues', unit: 'issues' }],
      filters: ['date_range', 'granularity', 'customer'],
    },
    {
      name: 'words_processed',
      definition: 'Source words a human confirmed or edited.',
      dimensions: ['target_language', 'user', 'customer'],
      values: [
        { name: 'words', unit: 'words' },
        { name: 'segments', unit: 'segments' },
        { name: 'characters', unit: 'characters' },
      ],
      filters: ['date_range', 'granularity', 'unit', 'target_language', 'customer'],
    },
    {
      name: 'mt_engine_usage',
      definition: 'Segments machine-translated, by MT engine.',
      dimensions: ['mt_engine', 'target_language', 'customer'],
      values: [
        { name: 'segments', unit: 'segments' },
        { name: 'words', unit: 'words' },
        { name: 'characters', unit: 'characters' },
      ],
      filters: ['date_range', 'granularity', 'unit', 'target_language', 'customer'],
    },
    {
      name: 'ai_token_usage',
      definition: 'Tokens spent per AI engine.',
      dimensions: ['provider', 'model', 'customer'],
      values: [
        { name: 'input_tokens', unit: 'tokens' },
        { name: 'output_tokens', unit: 'tokens' },
        { name: 'calls', unit: 'calls' },
      ],
      filters: ['date_range', 'granularity', 'customer'],
    },
  ],
}

const DASHBOARD = {
  id: 1,
  name: 'Operations',
  created_at: '2026-08-18T00:00:00Z',
  filters: { controls: [] as string[] },
  available_controls: ['date_range', 'unit', 'granularity', 'customer'],
  personal: false,
  charts: [
    {
      id: 11,
      title: 'MT by engine',
      definition: { measure: 'mt_engine_usage', dimensions: ['mt_engine'], last_days: 60 },
      viz: { type: 'bar' as const, x_key: 'mt_engine', series: ['words'] },
      controls: { controls: [] },
      available_controls: ['date_range', 'unit', 'granularity', 'customer'],
      position: 1,
    },
    {
      id: 12,
      title: 'Broken chart',
      definition: { measure: 'retired_measure', last_days: 30 },
      viz: { type: 'bar' as const, x_key: 'x', series: ['y'] },
      controls: { controls: [] },
      available_controls: ['date_range', 'unit', 'granularity', 'customer'],
      position: 2,
    },
  ],
}

function renderPage(path = '/dashboards/1') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/dashboards/:dashboardId" element={<DashboardPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  getMeMock.mockResolvedValue({ role: 'owner' } as Awaited<ReturnType<typeof getMe>>)
  getDashboardMock.mockResolvedValue(DASHBOARD)
  renameDashboardMock.mockResolvedValue()
  optionsMock.mockResolvedValue({ customer: ['Acme', 'Globex'] })
  removeDashFilterMock.mockResolvedValue()
  removeChartFilterMock.mockResolvedValue()
  updateDashboardFiltersMock.mockResolvedValue()
  updateChartControlsMock.mockResolvedValue()
  catalogMock.mockResolvedValue(CATALOG)
  pinStateMock.mockResolvedValue({ chart_ids: [], dashboard_ids: [] })
  pinChartMock.mockResolvedValue()
  unpinChartMock.mockResolvedValue()
})

describe('DashboardPage', () => {
  it('renders each chart card from its own data fetch; one failure stays on its card', async () => {
    getChartDataMock.mockImplementation(async chartId => {
      if (chartId === 12) throw new Error('unknown measure "retired_measure"; available: …')
      return {
        measure: 'mt_engine_usage',
        definition: 'Segments machine-translated…',
        columns: ['mt_engine', 'words'],
        rows: [{ mt_engine: 'DeepL', words: 120 }],
      }
    })
    renderPage()

    await waitFor(() => expect(screen.getByText('Operations')).toBeInTheDocument())
    // Healthy card: chart block mounted with its title + definition line.
    await waitFor(() => expect(screen.getByTestId('dashboard-chart')).toBeInTheDocument())
    expect(screen.getByText('MT by engine')).toBeInTheDocument()
    // Broken card: the 422 vocabulary message renders ON the card; the
    // healthy neighbor is untouched.
    await waitFor(() => expect(screen.getByTestId('chart-error')).toHaveTextContent('retired_measure'))
    expect(screen.getByText('Broken chart')).toBeInTheDocument()
    expect(screen.getByTestId('dashboard-chart')).toBeInTheDocument()
  })

  // Opening one chart must not silently re-ask the question: whatever the
  // dashboard is currently showing rides along in the link.
  it('links each card to its own page, carrying the current selection', async () => {
    renderPage('/dashboards/1?last_days=30&unit=characters&customer=Acme')
    await waitFor(() => expect(screen.getAllByTestId('chart-open-link').length).toBeGreaterThan(0))
    const href = screen.getAllByTestId('chart-open-link')[0]!.getAttribute('href')!
    const url = new URL(href, 'http://localhost')
    expect(url.pathname).toBe('/dashboards/1/charts/11')
    expect(url.searchParams.get('last_days')).toBe('30')
    expect(url.searchParams.get('unit')).toBe('characters')
    expect(url.searchParams.get('customer')).toBe('Acme')
  })

  // The title is the one way in, so there is no separate expand icon beside
  // it; the actions run pin, download, edit, move/copy, delete.
  it('makes the title the way in and orders the card actions', async () => {
    renderPage()
    const card = (await screen.findAllByTestId('dashboard-chart-card'))[0]!
    expect(card.querySelector('[data-testid="chart-expand-link"]')).toBeNull()
    expect(card.querySelector('[data-testid="chart-open-link"]')!.getAttribute('href')).toMatch(/^\/dashboards\/1\/charts\/11\?/)

    const order = ['pin-button', 'chart-export', 'chart-edit']
      .map(id => card.querySelector(`[data-testid="${id}"]`)!)
    const del = card.querySelector('[aria-label="Delete chart"]')!
    const before = (a: Element, b: Element) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)
    expect(before(order[0]!, order[1]!)).toBe(true)
    expect(before(order[1]!, order[2]!)).toBe(true)
    expect(before(order[2]!, del)).toBe(true)
  })

  // A pinned chart keeps its pin in view; an unpinned one shows it on hover.
  // The pin sits before the title, always shown; the title stays the link.
  it('pins a chart from the pin before its title', async () => {
    pinStateMock.mockResolvedValue({ chart_ids: [11], dashboard_ids: [] })
    renderPage()
    const pinned = await screen.findByRole('button', { name: 'Unpin MT by engine' })
    expect(pinned).toHaveAttribute('data-pinned', 'true')
    expect(pinned.nextElementSibling).toHaveAttribute('data-testid', 'chart-open-link')
    expect(pinned.nextElementSibling).toHaveAttribute('href', expect.stringMatching(/^\/dashboards\/1\/charts\/11\?/))
    const unpinned = screen.getByRole('button', { name: 'Pin Broken chart' })
    expect(unpinned).not.toHaveAttribute('data-pinned')
    for (const b of [pinned, unpinned]) expect(b.className).not.toMatch(/opacity-0/)

    fireEvent.click(pinned)
    await waitFor(() => expect(unpinChartMock).toHaveBeenCalledWith(11))
    fireEvent.click(unpinned)
    await waitFor(() => expect(pinChartMock).toHaveBeenCalledWith(12))
  })

  it('pins the dashboard from the pin before its name', async () => {
    renderPage()
    const pin = await screen.findByRole('button', { name: 'Pin Operations' })
    expect(pin.nextElementSibling).toHaveTextContent('Operations')
  })

  // The pencil re-opens the form that made the chart, seeded with what the
  // chart IS — including the title, which the builder edits too.
  // Anything less and the dialog would quietly reset a field it displayed.
  it('opens the builder on a chart with its own selections in place', async () => {
    renderPage()
    fireEvent.click((await screen.findAllByTestId('chart-edit'))[0]!)

    expect(await screen.findByText('Edit chart')).toBeInTheDocument()
    // The catalog lands before the form can show anything: every field is
    // drawn from what that measure supports.
    expect(await screen.findByTestId('chart-title')).toHaveValue('MT by engine')
    expect(screen.getByTestId('measure-mt_engine_usage')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('chart-type-bar')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('chart-x-key')).toHaveTextContent('MT engine')
    expect(screen.getByTestId('chart-value-words')).toBeChecked()
  })

  it('saves an edited chart as a whole', async () => {
    updateChartMock.mockResolvedValue()
    renderPage()
    fireEvent.click((await screen.findAllByTestId('chart-edit'))[0]!)
    fireEvent.change(await screen.findByTestId('chart-title'), { target: { value: 'Volume by engine' } })
    fireEvent.click(screen.getByTestId('create-chart-submit'))

    await waitFor(() => expect(updateChartMock).toHaveBeenCalledWith(11, expect.objectContaining({
      title: 'Volume by engine',
      measure: 'mt_engine_usage',
      x_key: 'mt_engine',
      series: ['words'],
    })))
    // Nothing is created on the way past.
    expect(createChartMock).not.toHaveBeenCalled()
  })

  // Retitling a chart on a shared dashboard changes what the org reads, so the
  // affordance follows the same gate as deleting one.
  it('hides chart editing from a non-admin on a shared dashboard', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    renderPage()
    await waitFor(() => expect(screen.getByText('MT by engine')).toBeInTheDocument())
    expect(screen.queryByTestId('chart-edit')).not.toBeInTheDocument()
  })

  // Adding a chart to a shared dashboard is a write, so the button follows the
  // same gate as rename and delete instead of failing with a 403 on save.
  it('hides New chart from a non-admin on a shared dashboard', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    renderPage()
    await waitFor(() => expect(screen.getByText('MT by engine')).toBeInTheDocument())
    expect(screen.queryByTestId('new-chart')).not.toBeInTheDocument()
  })

  it('shows New chart to a non-admin on their own dashboard', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, personal: true })
    renderPage()
    expect(await screen.findByTestId('new-chart')).toBeInTheDocument()
  })

  it('does not point a non-admin at New chart on an empty shared dashboard', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, charts: [] })
    renderPage()
    expect(await screen.findByText('No charts yet.')).toBeInTheDocument()
    expect(screen.queryByText(/New chart/)).not.toBeInTheDocument()
  })

  it('renames the dashboard from its title', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('rename-dashboard')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('rename-dashboard'))
    fireEvent.click(await screen.findByTestId('rename-dashboard-rename'))
    const field = screen.getByLabelText('Rename')
    fireEvent.change(field, { target: { value: 'Operations EU' } })
    fireEvent.keyDown(field, { key: 'Enter' })
    await waitFor(() => expect(renameDashboardMock).toHaveBeenCalledWith(1, 'Operations EU'))
  })

  it('hides the dashboard rename pencil from a non-admin on a shared dashboard', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    renderPage()
    await waitFor(() => expect(screen.getByText('MT by engine')).toBeInTheDocument())
    expect(screen.queryByTestId('rename-dashboard')).not.toBeInTheDocument()
  })

  // A chart can narrow itself by something the dashboard does not control.
  it('renders a chart-only filter row and sends its selection', async () => {
    getDashboardMock.mockResolvedValue({
      ...DASHBOARD,
      filters: { controls: ['date_range'] },
      available_controls: ['date_range', 'unit', 'granularity', 'customer'],
      charts: [{ ...DASHBOARD.charts[0]!, controls: { controls: ['customer'] } }],
    })
    renderPage()
    await waitFor(() => expect(screen.getByTestId('chart-filter-bar')).toBeInTheDocument())
    expect(screen.getByTestId('chart-filter-customer')).toBeInTheDocument()

    // The selection reaches THIS chart's request; it is not a dashboard filter.
    renderPage('/dashboards/1?c11.customer=Globex')
    await waitFor(() =>
      expect(getChartDataMock).toHaveBeenCalledWith(11, expect.objectContaining({ customer: 'Globex' })),
    )
  })

  // The more specific selection wins, which is what makes overlap safe.
  it('lets a chart-level pick override the dashboard-level one', async () => {
    getDashboardMock.mockResolvedValue({
      ...DASHBOARD,
      filters: { controls: ['customer'] },
      charts: [{ ...DASHBOARD.charts[0]!, controls: { controls: ['customer'] } }],
    })
    renderPage('/dashboards/1?customer=Acme&c11.customer=Globex')
    await waitFor(() =>
      expect(getChartDataMock).toHaveBeenCalledWith(11, expect.objectContaining({ customer: 'Globex' })),
    )
  })

  // Without its own pick, a chart inherits the dashboard's.
  it('inherits the dashboard selection when the chart has none', async () => {
    getDashboardMock.mockResolvedValue({
      ...DASHBOARD,
      filters: { controls: ['customer'] },
      charts: [{ ...DASHBOARD.charts[0]!, controls: { controls: ['customer'] } }],
    })
    renderPage('/dashboards/1?customer=Acme')
    await waitFor(() =>
      expect(getChartDataMock).toHaveBeenCalledWith(11, expect.objectContaining({ customer: 'Acme' })),
    )
  })

  // One chart monthly, the rest of the dashboard weekly. The inline row is what
  // makes the divergence visible.
  it('lets one chart use its own granularity', async () => {
    getDashboardMock.mockResolvedValue({
      ...DASHBOARD,
      filters: { controls: ['granularity'] },
      charts: [{ ...DASHBOARD.charts[0]!, controls: { controls: ['granularity'] } }],
    })
    renderPage('/dashboards/1?grain=week&c11.grain=month')
    await waitFor(() =>
      expect(getChartDataMock).toHaveBeenCalledWith(11, expect.objectContaining({ grain: 'month' })),
    )
  })

  // An untouched chart control must show what the card is ACTUALLY doing, or it
  // reads as an override that is not in effect.
  it('shows the inherited value until the chart control is touched', async () => {
    getDashboardMock.mockResolvedValue({
      ...DASHBOARD,
      filters: { controls: ['granularity'] },
      charts: [{ ...DASHBOARD.charts[0]!, controls: { controls: ['granularity'] } }],
    })
    renderPage('/dashboards/1?grain=month')
    await waitFor(() => expect(screen.getByTestId('chart-filter-granularity')).toBeInTheDocument())
    expect(screen.getByTestId('chart-filter-granularity')).toHaveTextContent('Monthly')
    // …and the request follows the dashboard, since the chart set nothing.
    expect(getChartDataMock).toHaveBeenCalledWith(11, expect.objectContaining({ grain: 'month' }))
  })

  // Removing a control sends the value showing right now, because that value is
  // what gets frozen into the charts — the server cannot see the URL.
  it('removes a dashboard filter from the edit menu, freezing its current value', async () => {
    getDashboardMock.mockResolvedValue({
      ...DASHBOARD, filters: { controls: ['customer'] },
      available_controls: ['date_range', 'customer'],
    })
    renderPage('/dashboards/1?customer=Globex')
    await waitFor(() => expect(screen.getByTestId('rename-dashboard')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('rename-dashboard'))
    // An active filter is a TICKED item; unticking removes it.
    await waitFor(() => expect(screen.getByTestId('rename-dashboard-filter-customer')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('rename-dashboard-filter-customer'))
    await waitFor(() => expect(screen.getByTestId('rename-dashboard-remove-confirm')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('rename-dashboard-remove-confirm'))
    await waitFor(() => expect(removeDashFilterMock).toHaveBeenCalledWith(1, 'customer', 'Globex'))
  })

  // "Remove filter" reads like "show me everything", and the opposite happens —
  // so the dialog has to say the value stays fixed.
  it('warns that the value stays fixed rather than widening', async () => {
    getDashboardMock.mockResolvedValue({
      ...DASHBOARD, filters: { controls: ['customer'] },
      available_controls: ['customer'],
    })
    renderPage('/dashboards/1?customer=Globex')
    await waitFor(() => expect(screen.getByTestId('rename-dashboard')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('rename-dashboard'))
    fireEvent.click(await screen.findByTestId('rename-dashboard-filter-customer'))
    await waitFor(() => expect(screen.getByText(/stays fixed at "Globex"/)).toBeInTheDocument())
  })

  // A chart's own filters are part of what the builder edits, so they arrive
  // ticked rather than needing a second menu.
  it('seeds the builder with the chart own filters', async () => {
    getDashboardMock.mockResolvedValue({
      ...DASHBOARD,
      charts: [{
        ...DASHBOARD.charts[0]!,
        definition: { measure: 'qa_issues', dimensions: ['week'], last_days: 60 },
        viz: { type: 'line' as const, x_key: 'week', series: ['issues'] },
        controls: { controls: ['customer'] },
        available_controls: ['customer'],
      }],
    })
    renderPage('/dashboards/1')
    fireEvent.click(await screen.findByTestId('chart-edit'))
    await waitFor(() => expect(screen.getByTestId('builder-filter-customer')).toBeChecked())
  })

  it('hides the edit menu from someone who cannot write', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, filters: { controls: ['customer'] } })
    renderPage()
    await waitFor(() => expect(screen.getByTestId('dashboard-filter-bar')).toBeInTheDocument())
    expect(screen.queryByTestId('rename-dashboard')).not.toBeInTheDocument()
    expect(screen.queryByTestId('chart-edit')).not.toBeInTheDocument()
  })

  // The bars carry only dropdowns; adding and removing live in the menu.
  it('shows no filter row on a chart with no filters of its own', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByText('MT by engine')).toBeInTheDocument())
    expect(screen.queryByTestId('chart-filter-bar')).not.toBeInTheDocument()
  })

  // A filter row on ONE card pushes its chart down; without reserving the row
  // on its neighbours the two charts start at different heights and read as
  // misaligned even though the cards are the same size.
  it('reserves the filter row on cards without filters, so the charts line up', async () => {
    getDashboardMock.mockResolvedValue({
      ...DASHBOARD,
      charts: [
        { ...DASHBOARD.charts[0]!, controls: { controls: ['customer'] } },
        DASHBOARD.charts[1]!,
      ],
    })
    renderPage()
    await waitFor(() => expect(screen.getByTestId('chart-filter-bar')).toBeInTheDocument())
    // One real row, one spacer of the same height.
    expect(document.querySelectorAll('[aria-hidden="true"].mb-2.h-7').length).toBe(1)
  })

  // No card has its own filters, so nothing is reserved and no space is wasted.
  it('reserves nothing when no card has filters', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByText('MT by engine')).toBeInTheDocument())
    expect(document.querySelectorAll('[aria-hidden="true"].mb-2.h-7').length).toBe(0)
  })

  // The menu offers what the server says the measure supports — a control that
  // changes nothing must never appear. Every available control is listed; the
  // active ones are ticked. Ticking an
  // unticked one adds it.
  it('lists every available filter and adds an unticked one', async () => {
    getDashboardMock.mockResolvedValue({
      ...DASHBOARD,
      filters: { controls: ['date_range'] },
      available_controls: ['date_range', 'customer'],
    })
    renderPage()
    await waitFor(() => expect(screen.getByTestId('rename-dashboard')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('rename-dashboard'))
    await waitFor(() => expect(screen.getByTestId('rename-dashboard-filter-customer')).toBeInTheDocument())
    // Both appear — one ticked, one not; actives are never hidden.
    expect(screen.getByTestId('rename-dashboard-filter-date-range')).toBeInTheDocument()

    fireEvent.click(screen.getByTestId('rename-dashboard-filter-customer'))
    // The endpoint REPLACES the list, so the existing controls travel with it.
    await waitFor(() =>
      expect(updateDashboardFiltersMock).toHaveBeenCalledWith(1, { controls: ['date_range', 'customer'] }),
    )
  })

  // The menu mixes an action with a set of toggles; without a heading the ticks
  // read as options for "Rename".
  it('labels the filter group in the menu', async () => {
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, available_controls: ['customer'] })
    renderPage()
    await waitFor(() => expect(screen.getByTestId('rename-dashboard')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('rename-dashboard'))
    await waitFor(() => expect(screen.getByText('Filters')).toBeInTheDocument())
  })

  it('offers Rename from the same menu', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('rename-dashboard')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('rename-dashboard'))
    fireEvent.click(await screen.findByTestId('rename-dashboard-rename'))
    await waitFor(() => expect(screen.getByLabelText('Rename')).toBeInTheDocument())
  })



  it('does not restate the window when the dashboard controls it', async () => {
    getDashboardMock.mockResolvedValue({
      ...DASHBOARD,
      filters: { controls: ['date_range'] },
      charts: [DASHBOARD.charts[0]],
    })
    getChartDataMock.mockResolvedValue({
      measure: 'mt_engine_usage',
      definition: 'Segments machine-translated in the period and their source words, by MT engine.',
      columns: ['mt_engine', 'words'],
      rows: [{ mt_engine: 'DeepL', words: 120 }],
    })
    renderPage()

    // The measure slug is a raw API value that restates the title. Its human
    // definition carries the same job better —
    // and arrives with the DATA, so wait for the attribute rather than the
    // heading, which renders before the fetch resolves.
    await waitFor(() =>
      expect(screen.getByText('MT by engine')).toHaveAttribute(
        'title', expect.stringContaining('Segments machine-translated'),
      ),
    )
    expect(screen.queryByText(/mt_engine_usage/)).not.toBeInTheDocument()
    expect(screen.queryByText(/last 60 days/i)).not.toBeInTheDocument()
  })

  // No window note under the title, even with no date control to state it: a
  // line under every card is noise, and the filter says it where it exists.
  it('states no window on the card, even when the dashboard has no date control', async () => {
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, charts: [DASHBOARD.charts[0]] })
    getChartDataMock.mockResolvedValue({
      measure: 'mt_engine_usage', definition: 'd', columns: ['mt_engine', 'words'],
      rows: [{ mt_engine: 'DeepL', words: 120 }],
    })
    renderPage()
    await waitFor(() => expect(screen.getByTestId('dashboard-chart')).toBeInTheDocument())
    expect(screen.queryByText(/Last \d+ days/)).not.toBeInTheDocument()
    expect(screen.queryByText(/mt_engine_usage/)).not.toBeInTheDocument()
  })

  // "All dashboards" must return to the tab the dashboard lives on.
  it('links back to My dashboards from a personal dashboard', async () => {
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, personal: true, charts: [] })
    renderPage()
    await waitFor(() =>
      expect(screen.getByRole('link', { name: 'My dashboards' })).toHaveAttribute('href', '/dashboards/mine'),
    )
  })

  it('links back to Organization dashboards from a shared dashboard', async () => {
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, charts: [] })
    renderPage()
    await waitFor(() =>
      expect(screen.getByRole('link', { name: 'Organization dashboards' })).toHaveAttribute('href', '/dashboards/organization'),
    )
  })

  // A shared dashboard's charts belong to the org, so the confirmation has to
  // say that rather than read like a private cleanup.
  it('warns that deleting from a shared dashboard affects everyone', async () => {
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, charts: [DASHBOARD.charts[0]] })
    getChartDataMock.mockResolvedValue({
      measure: 'mt_engine_usage', definition: 'd', columns: ['mt_engine', 'words'],
      rows: [{ mt_engine: 'DeepL', words: 120 }],
    })
    deleteChartMock.mockResolvedValue()
    renderPage()

    await waitFor(() => expect(screen.getByLabelText('Delete chart')).toBeInTheDocument())
    fireEvent.click(screen.getByLabelText('Delete chart'))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent(/everyone in the organization/i)
    expect(deleteChartMock).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('chart-delete-confirm'))
    await waitFor(() => expect(deleteChartMock).toHaveBeenCalledWith(DASHBOARD.charts[0]!.id))
  })

  // Pins that the owner of a personal dashboard may delete its charts without
  // an admin role.
  it('lets a non-admin delete charts on their own dashboard', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, personal: true, charts: [DASHBOARD.charts[0]] })
    getChartDataMock.mockResolvedValue({
      measure: 'mt_engine_usage', definition: 'd', columns: ['mt_engine', 'words'],
      rows: [{ mt_engine: 'DeepL', words: 120 }],
    })
    renderPage()
    await waitFor(() => expect(screen.getByLabelText('Delete chart')).toBeInTheDocument())
  })

  it('hides chart delete from a non-admin on a shared dashboard', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, charts: [DASHBOARD.charts[0]] })
    getChartDataMock.mockResolvedValue({
      measure: 'mt_engine_usage', definition: 'd', columns: ['mt_engine', 'words'],
      rows: [{ mt_engine: 'DeepL', words: 120 }],
    })
    renderPage()
    await waitFor(() => expect(screen.getByText('MT by engine')).toBeInTheDocument())
    expect(screen.queryByLabelText('Delete chart')).not.toBeInTheDocument()
  })

  it('shows the empty-period state when a chart has no rows', async () => {
    getDashboardMock.mockResolvedValue({ ...DASHBOARD, charts: [DASHBOARD.charts[0]] })
    getChartDataMock.mockResolvedValue({
      measure: 'mt_engine_usage', definition: 'd', columns: ['mt_engine', 'words'], rows: [],
    })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('chart-empty')).toBeInTheDocument())
    expect(screen.queryByTestId('dashboard-chart')).not.toBeInTheDocument()
  })

  // The builder: a form over the metrics catalog. Nothing
  // can be built before a measure is chosen, because every other field is a
  // property of it.
  it('New chart builds a chart from the catalog', async () => {
    createChartMock.mockResolvedValue({ id: 77 })
    renderPage()
    fireEvent.click(await screen.findByTestId('new-chart'))

    fireEvent.click(await screen.findByTestId('measure-qa_issues'))
    // Choosing the measure seeds a chart that is ready to save.
    expect(await screen.findByTestId('measure-definition')).toHaveTextContent('QA findings')
    expect(screen.getByTestId('chart-title')).toHaveValue('QA issues')

    fireEvent.click(screen.getByTestId('create-chart-submit'))
    await waitFor(() => expect(createChartMock).toHaveBeenCalledWith(1, {
      title: 'QA issues',
      measure: 'qa_issues',
      dimensions: ['week'],
      chart_type: 'line',
      x_key: 'week',
      series: ['issues'],
      series_key: undefined,
      controls: [],
    }))
  })

  // Filters chosen in the builder are stored on the card, so it arrives with
  // its own dropdown rather than needing a second trip through the edit menu.
  it('carries the chosen filters onto the new chart', async () => {
    createChartMock.mockResolvedValue({ id: 78 })
    renderPage()
    fireEvent.click(await screen.findByTestId('new-chart'))
    fireEvent.click(await screen.findByTestId('measure-qa_issues'))
    fireEvent.click(await screen.findByTestId('builder-filter-customer'))
    fireEvent.click(screen.getByTestId('create-chart-submit'))
    await waitFor(() =>
      expect(createChartMock.mock.calls[0]![1].controls).toEqual(['customer']))
  })

  // A pie is one ring of slices, so there is no axis to split along and no
  // time bucket to put on it.
  it('offers no split and no dates on a pie chart', async () => {
    renderPage()
    fireEvent.click(await screen.findByTestId('new-chart'))
    fireEvent.click(await screen.findByTestId('measure-qa_issues'))
    fireEvent.click(screen.getByTestId('chart-type-pie'))
    await waitFor(() => expect(screen.getByTestId('chart-series-key')).toBeDisabled())
    // The weekly axis it was seeded with cannot survive the switch.
    expect(screen.getByTestId('chart-x-key')).toHaveTextContent('Check type')
  })

  // A dimension belongs to one measure, so switching measures must not carry
  // the previous one's selection into a definition the compiler would reject.
  it('resets the chart form when the measure changes', async () => {
    renderPage()
    fireEvent.click(await screen.findByTestId('new-chart'))
    fireEvent.click(await screen.findByTestId('measure-qa_issues'))
    await waitFor(() => expect(screen.getByTestId('chart-value-issues')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('measure-words_processed'))
    await waitFor(() => expect(screen.getByTestId('chart-value-words')).toBeInTheDocument())
    expect(screen.queryByTestId('chart-value-issues')).not.toBeInTheDocument()
  })
})
