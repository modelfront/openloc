import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { toast } from 'sonner'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

vi.mock('../api/analyses', async () => {
  const actual = await vi.importActual<typeof import('../api/analyses')>('../api/analyses')
  return { ...actual, listAnalyses: vi.fn(), getAnalysis: vi.fn(), deleteAnalysis: vi.fn() }
})

import { listAnalyses, getAnalysis, deleteAnalysis, type Analysis } from '../api/analyses'
import ProjectAnalysesTab from './ProjectAnalysesTab'

const analysis = (id: number, name: string, status: Analysis['status'] = 'completed'): Analysis => ({
  id, name, status, tenant_id: 1, project_id: 1, source_language: 'en', job_ids: [1, 2],
  created_at: '2026-10-01T10:00:00Z', completed_at: '2026-10-01T10:01:00Z',
  settings_snapshot: '', is_auto: false, deleted_at: '',
})

function renderTab() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ProjectAnalysesTab projectId={1} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(listAnalyses).mockResolvedValue([
    analysis(1, 'First run'), analysis(2, 'Second run'), analysis(3, 'Still running', 'running'),
  ])
  vi.mocked(getAnalysis).mockImplementation(async id => ({ analysis: analysis(id, `Analysis ${id}`), breakdowns: [] }))
})

describe('ProjectAnalysesTab accordion', () => {
  it('each completed row has a collapsed disclosure arrow', async () => {
    renderTab()

    await screen.findByTestId('analysis-row-1')
    expect(within(screen.getByTestId('analysis-row-1')).getByRole('button', { name: 'First run' }))
      .toHaveAttribute('aria-expanded', 'false')
    // The toggle is the row's one tab stop: the name inside it takes no focus.
    expect(within(screen.getByTestId('analysis-row-1')).getByText('First run')).not.toHaveAttribute('tabindex')
    expect(screen.queryByTestId('analysis-detail-1')).not.toBeInTheDocument()
  })

  it('opens the details directly under the clicked row', async () => {
    renderTab()

    fireEvent.click(await screen.findByTestId('analysis-row-2'))

    const detail = await screen.findByTestId('analysis-detail-2')
    // The detail row is the very next row after its analysis, not under the table.
    expect(screen.getByTestId('analysis-row-2').nextElementSibling).toBe(detail.closest('tr'))
    const toggle = within(screen.getByTestId('analysis-row-2')).getByRole('button', { name: 'Second run' })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(toggle).toHaveAttribute('aria-controls', detail.id)
  })

  it('the arrow toggles too, once per click', async () => {
    renderTab()

    const toggle = within(await screen.findByTestId('analysis-row-1')).getByRole('button', { name: 'First run' })
    fireEvent.click(toggle)
    expect(await screen.findByTestId('analysis-detail-1')).toBeInTheDocument()
    fireEvent.click(toggle)
    expect(screen.queryByTestId('analysis-detail-1')).not.toBeInTheDocument()
  })

  it('one open at a time: opening another closes the first', async () => {
    renderTab()

    fireEvent.click(await screen.findByTestId('analysis-row-1'))
    await screen.findByTestId('analysis-detail-1')
    fireEvent.click(screen.getByTestId('analysis-row-2'))

    expect(await screen.findByTestId('analysis-detail-2')).toBeInTheDocument()
    expect(screen.queryByTestId('analysis-detail-1')).not.toBeInTheDocument()
  })

  it('a row still running cannot be opened', async () => {
    renderTab()

    const row = await screen.findByTestId('analysis-row-3')
    expect(within(row).getByRole('button', { name: 'Still running' })).toBeDisabled()
    fireEvent.click(row)
    expect(screen.queryByTestId('analysis-detail-3')).not.toBeInTheDocument()
  })

  it('Delete does not open the row', async () => {
    renderTab()

    fireEvent.click(within(await screen.findByTestId('analysis-row-1')).getByRole('button', { name: 'Delete First run' }))
    expect(screen.queryByTestId('analysis-detail-1')).not.toBeInTheDocument()
  })
})

// A failed load must say so: "No analyses yet" for a server error reads as
// "the analyses are gone".
describe('ProjectAnalysesTab when loading fails', () => {
  it('the list shows the error with a Retry, not "No analyses yet"', async () => {
    vi.mocked(listAnalyses)
      .mockReset()
      .mockRejectedValueOnce(new Error('server unavailable'))
      .mockResolvedValueOnce([analysis(1, 'First run')])

    renderTab()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the analyses: server unavailable")
    expect(screen.queryByText(/No analyses yet/)).not.toBeInTheDocument()
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('analysis-row-1')).toBeInTheDocument()
  })

  it('opened details that fail to load show the error with a Retry', async () => {
    vi.mocked(getAnalysis)
      .mockReset()
      .mockRejectedValueOnce(new Error('timeout'))
      .mockImplementation(async id => ({ analysis: analysis(id, `Analysis ${id}`), breakdowns: [] }))

    renderTab()
    fireEvent.click(await screen.findByTestId('analysis-row-1'))

    const detail = await screen.findByTestId('analysis-detail-1')
    const alert = await within(detail).findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the analysis: timeout")
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(within(detail).queryByRole('alert')).not.toBeInTheDocument())
  })
})

describe('ProjectAnalysesTab delete', () => {
  it('a refused delete tells the user and keeps the row', async () => {
    vi.mocked(deleteAnalysis).mockRejectedValueOnce(new Error('analysis is in use'))

    renderTab()
    fireEvent.click(within(await screen.findByTestId('analysis-row-1')).getByRole('button', { name: 'Delete First run' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(deleteAnalysis).toHaveBeenCalledWith(1))
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(expect.stringContaining('analysis is in use')))
    expect(screen.getByTestId('analysis-row-1')).toBeInTheDocument()
  })
})
