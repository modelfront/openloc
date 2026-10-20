import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { StoredXLSXLayout } from '@/api'
import { queryKeys } from '@/lib/query-keys'

const api = vi.hoisted(() => ({ getXLSXLayout: vi.fn(), setXLSXLayout: vi.fn() }))
vi.mock('@/api', async importOriginal => ({ ...(await importOriginal<object>()), ...api }))

import { EditXLSXColumnsDialog } from './EditXLSXColumnsDialog'

const inspection: StoredXLSXLayout['inspection'] = {
  bilingual: false,
  sheets: [
    { name: 'Products', columns: [{ letter: 'A', title: 'ID' }, { letter: 'B', title: 'English' }] },
    { name: 'Notes', columns: [{ letter: 'A', title: 'Thanks' }] },
  ],
  suggested_layout: { sheets: [{ name: 'Products', header_row: true, columns: ['B'] }, { name: 'Notes', columns: ['A'] }] },
}

function renderDialog(cached?: StoredXLSXLayout) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  if (cached) qc.setQueryData(queryKeys.files.xlsxLayout(7, 3), cached)
  render(
    <QueryClientProvider client={qc}>
      <EditXLSXColumnsDialog projectId={7} fileId={3} fileName="catalog.xlsx" onClose={vi.fn()} />
    </QueryClientProvider>,
  )
}

describe('EditXLSXColumnsDialog', () => {
  beforeEach(() => vi.resetAllMocks())

  it('opens on the stored layout', async () => {
    api.getXLSXLayout.mockResolvedValue({ layout: { sheets: [{ name: 'Products', header_row: true, columns: ['A'] }] }, inspection })
    renderDialog()
    expect(await screen.findByTestId('xlsx-sheet-Products-toggle')).toHaveTextContent('Column A')
    expect(screen.getByTestId('xlsx-sheet-Notes-toggle')).toHaveTextContent('Not translated')
  })

  // A file stored without a layout is read cell by cell: every text cell of
  // every sheet, row 1 included. The dialog shows that, not the suggestion.
  it('shows a file read cell by cell as whole sheets', async () => {
    api.getXLSXLayout.mockResolvedValue({ layout: null, inspection })
    renderDialog()
    expect(await screen.findByTestId('xlsx-sheet-Products-toggle')).toHaveTextContent('Whole sheet')
    expect(screen.getByTestId('xlsx-sheet-Notes-toggle')).toHaveTextContent('Whole sheet')
  })

  // Reopened right after a save, the cache still holds the layout from before
  // it. The form waits for the fresh read instead of starting from that.
  it('opens on the fresh layout, not the one cached before a save', async () => {
    api.getXLSXLayout.mockResolvedValue({ layout: { sheets: [{ name: 'Products', header_row: true, columns: ['B'] }] }, inspection })
    renderDialog({ layout: { sheets: [{ name: 'Products', header_row: true, columns: ['A'] }] }, inspection })
    await waitFor(() => expect(screen.getByTestId('xlsx-sheet-Products-toggle')).toHaveTextContent('Column B'))
  })
})
