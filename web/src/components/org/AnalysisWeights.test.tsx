import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import AnalysisWeights from './AnalysisWeights'
import type { WeightEntry } from '../../api'
import { queryKeys } from '@/lib/query-keys'

const api = vi.hoisted(() => ({ getAnalyticsWeights: vi.fn(), updateAnalyticsWeights: vi.fn() }))
vi.mock('../../api', async (importOriginal) => ({ ...(await importOriginal<object>()), ...api }))

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast }))

const serverWeights: WeightEntry[] = [
  { band: 'tm_exact', weight_pct: 10 },
  { band: 'new', weight_pct: 100 },
]

function renderWeights() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <AnalysisWeights />
    </QueryClientProvider>,
  )
  return client
}

beforeEach(() => {
  api.getAnalyticsWeights.mockReset()
  api.updateAnalyticsWeights.mockReset()
  toast.success.mockReset()
  toast.error.mockReset()
})
afterEach(cleanup)

describe('AnalysisWeightsSection', () => {
  it('a failed load shows Retry and no inputs or Save', async () => {
    api.getAnalyticsWeights.mockRejectedValueOnce(new Error('server unavailable'))
    renderWeights()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the analysis weights: server unavailable")
    expect(screen.queryAllByRole('spinbutton')).toHaveLength(0)
    expect(screen.queryByRole('button', { name: 'Save weights' })).not.toBeInTheDocument()

    api.getAnalyticsWeights.mockResolvedValueOnce(serverWeights)
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByDisplayValue('10')).toBeInTheDocument()
  })

  it('Save sends the edited weights, shows what the server saved, and marks every project analysis stale', async () => {
    const edited = [{ band: 'tm_exact', weight_pct: 25 }, { band: 'new', weight_pct: 100 }]
    const saved = [{ band: 'tm_exact', weight_pct: 30 }, { band: 'new', weight_pct: 100 }]
    // The refetch after the save never lands, so the input can only show the
    // saved weights if the save's own response is put in the cache.
    api.getAnalyticsWeights.mockResolvedValueOnce(serverWeights).mockReturnValue(new Promise(() => {}))
    api.updateAnalyticsWeights.mockResolvedValue(saved)
    const client = renderWeights()
    client.setQueryData(queryKeys.projectAnalysis.v2(42), { project_id: 42 })

    fireEvent.change(await screen.findByDisplayValue('10'), { target: { value: '25' } })
    await userEvent.click(screen.getByRole('button', { name: 'Save weights' }))

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Analysis weights saved'))
    expect(api.updateAnalyticsWeights.mock.calls[0][0]).toEqual(edited)
    expect(screen.getByDisplayValue('30')).toBeInTheDocument()
    expect(screen.queryByDisplayValue('25')).not.toBeInTheDocument()
    expect(client.getQueryState(queryKeys.projectAnalysis.v2(42))?.isInvalidated).toBe(true)
  })
})
