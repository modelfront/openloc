import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { AnalysisExportMenu } from './AnalysisExportMenu'
import type { Analysis } from '../api/analyses'
import { menuItem } from '@/test/menu'

const exportAnalysis = vi.fn().mockResolvedValue(undefined)

vi.mock('../api/analyses', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/analyses')>()
  return { ...actual, exportAnalysis: (...args: unknown[]) => exportAnalysis(...args) }
})

const analysis = (status: Analysis['status']): Analysis => ({
  id: 7,
  tenant_id: 1,
  project_id: 3,
  name: 'Quote batch',
  status,
  error_message: null,
  source_language: 'en',
  job_ids: [1],
  created_by: null,
  created_at: '2026-10-01T09:00:00Z',
  completed_at: status === 'completed' ? '2026-10-01T09:01:00Z' : null,
  settings_snapshot: null,
  is_auto: false,
  deleted_at: null,
} as unknown as Analysis)

function renderMenu(a: Analysis) {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <AnalysisExportMenu analysis={a} />
    </QueryClientProvider>,
  )
}

afterEach(() => {
  cleanup()
  exportAnalysis.mockClear()
})

describe('AnalysisExportMenu', () => {
  it.each([
    ['analysis-export-7-csv', 'csv'],
    ['analysis-export-7-xlsx', 'xlsx'],
  ])('%s downloads the analysis as %s', async (testId, format) => {
    renderMenu(analysis('completed'))
    await userEvent.click(screen.getByTestId('analysis-export-7'))
    await userEvent.click(await menuItem(testId))
    await waitFor(() => expect(exportAnalysis).toHaveBeenCalledWith(7, format))
  })

  it('offers Excel first, then CSV, with no descriptions', async () => {
    renderMenu(analysis('completed'))
    await userEvent.click(screen.getByTestId('analysis-export-7'))
    await menuItem('analysis-export-7-csv')
    const items = screen.getAllByRole('menuitem').map(i => i.textContent)
    expect(items).toEqual(['Excel (XLSX)', 'CSV'])
  })

  it.each(['pending', 'running', 'failed'] as const)('is disabled for a %s analysis', status => {
    renderMenu(analysis(status))
    expect(screen.getByTestId('analysis-export-7')).toBeDisabled()
  })

  it('names the analysis for screen readers', () => {
    renderMenu(analysis('completed'))
    expect(screen.getByTestId('analysis-export-7')).toHaveAccessibleName('Download Quote batch')
  })
})
