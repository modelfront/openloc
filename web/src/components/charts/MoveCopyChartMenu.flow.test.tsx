// The override confirmation: a destination whose own controls would take over
// a filter the chart fixes answers 409, the user is asked, and only a yes
// sends the confirmed request.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { menuItem } from '@/test/menu'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, getMe: vi.fn(), listDashboards: vi.fn(), copyChart: vi.fn(), moveChart: vi.fn() }
})

import { getMe, listDashboards, copyChart, type DashboardSummary } from '@/api'
import { ApiError } from '@/api/request'
import { MoveCopyChartMenu } from './MoveCopyChartMenu'

const TARGET: DashboardSummary = { id: 2, name: 'Filtered', personal: true, chart_count: 0, created_at: '', preview_charts: [] }
const OVERRIDE = new ApiError(409, 'conflict', {
  error: { code: 'filter_override', message: '"Filtered" controls Customer, which this chart fixes.' },
})

function renderMenu() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MoveCopyChartMenu chartId={11} chartTitle="Words" dashboardId={1} canWriteCurrent />
    </QueryClientProvider>,
  )
}

async function copyToTarget() {
  fireEvent.click(await menuItem('chart-folder-menu'))
  fireEvent.click(await menuItem('chart-copy'))
  fireEvent.click(await menuItem('chart-copy-to-2'))
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getMe).mockResolvedValue({ role: 'owner' } as Awaited<ReturnType<typeof getMe>>)
  vi.mocked(listDashboards).mockResolvedValue([TARGET])
})

describe('MoveCopyChartMenu override', () => {
  it('asks before copying over a filter, and sends the confirmed copy on yes', async () => {
    vi.mocked(copyChart).mockRejectedValueOnce(OVERRIDE).mockResolvedValueOnce(undefined as never)
    renderMenu()
    await copyToTarget()
    expect(await screen.findByText(/controls Customer, which this chart fixes/)).toBeInTheDocument()
    expect(vi.mocked(copyChart)).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: 'Copy anyway' }))
    await waitFor(() => expect(vi.mocked(copyChart)).toHaveBeenLastCalledWith(11, 2, true))
  })

  it('sends nothing more when the user backs out', async () => {
    vi.mocked(copyChart).mockRejectedValueOnce(OVERRIDE)
    renderMenu()
    await copyToTarget()
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByText(/controls Customer/)).not.toBeInTheDocument())
    expect(vi.mocked(copyChart)).toHaveBeenCalledTimes(1)
  })
})
