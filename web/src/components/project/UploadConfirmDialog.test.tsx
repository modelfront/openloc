import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError, type XLSXInspection } from '@/api'

const api = vi.hoisted(() => ({ inspectXLSX: vi.fn() }))
vi.mock('@/api', async importOriginal => ({ ...(await importOriginal<object>()), inspectXLSX: api.inspectXLSX }))

import { UploadConfirmDialog } from './UploadConfirmDialog'

afterEach(() => cleanup())
beforeEach(() => vi.resetAllMocks())

function setup(onConfirm = vi.fn(), files = [new File(['x'], 'doc.txt')]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <UploadConfirmDialog projectId={7} files={files} onOpenChange={() => {}} onConfirm={onConfirm} />
    </QueryClientProvider>,
  )
  return onConfirm
}

const workbook: XLSXInspection = {
  bilingual: false,
  sheets: [
    { name: 'Products', columns: [{ letter: 'A', title: 'entry id' }, { letter: 'B', title: 'English' }, { letter: 'C', title: 'Note' }] },
    { name: 'Notes', columns: [{ letter: 'A', title: 'Thanks' }] },
  ],
  suggested_layout: { sheets: [{ name: 'Products', header_row: true, columns: ['B'] }, { name: 'Notes', header_row: false, columns: ['A'] }] },
}

describe('UploadConfirmDialog', () => {
  // The page renders the dialog closed all the time; a reset keyed on a fresh
  // empty file list would re-render forever.
  it('renders closed without looping', () => {
    const qc = new QueryClient()
    const closed = () => (
      <QueryClientProvider client={qc}>
        <UploadConfirmDialog projectId={7} files={null} onOpenChange={() => {}} onConfirm={vi.fn()} />
      </QueryClientProvider>
    )
    const { rerender } = render(closed())
    // The page re-renders for its own reasons while the dialog stays closed.
    rerender(closed())
    expect(screen.queryByTestId('upload-confirm')).toBeNull()
  })

  it('names the files it is about to upload', () => {
    setup(vi.fn(), [new File(['x'], 'one.txt'), new File(['y'], 'two.txt')])
    expect(screen.getByText('Upload 2 files')).toBeTruthy()
    expect(screen.getByText('one.txt, two.txt')).toBeTruthy()
  })

  it('asks nothing about deadlines — the project workflow owns those', () => {
    setup()
    expect(screen.queryByTestId('workflow-deadlines')).toBeNull()
    expect(screen.queryByTestId('workflow-deadline-input-0')).toBeNull()
    expect(screen.queryByText(/deadline/i)).toBeNull()
  })

  it('defaults auto-analyze on and passes it to onConfirm', () => {
    const onConfirm = setup()
    const checkbox = screen.getByTestId('upload-analyze-checkbox') as HTMLInputElement
    expect(checkbox.checked).toBe(true)
    fireEvent.click(screen.getByTestId('upload-confirm'))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onConfirm.mock.calls[0][0]).toBe(true)
  })

  it('passes analyze=false when the checkbox is unchecked', () => {
    const onConfirm = setup()
    fireEvent.click(screen.getByTestId('upload-analyze-checkbox'))
    fireEvent.click(screen.getByTestId('upload-confirm'))
    expect(onConfirm.mock.calls[0][0]).toBe(false)
  })

  describe('an Excel workbook without language codes', () => {
    it('asks what to translate in the same dialog, and passes the answer on', async () => {
      const user = userEvent.setup()
      api.inspectXLSX.mockResolvedValueOnce(workbook)
      const xlsx = new File(['x'], 'catalog.xlsx')
      const onConfirm = setup(vi.fn(), [xlsx, new File(['y'], 'notes.txt')])

      // Sheets start collapsed, showing the suggestion.
      const toggle = await screen.findByTestId('upload-xlsx-catalog.xlsx-sheet-Products-toggle')
      expect(api.inspectXLSX).toHaveBeenCalledWith(7, xlsx)
      expect(toggle).toHaveTextContent('Column B')
      expect(toggle).toHaveAttribute('aria-expanded', 'false')
      // How the columns work sits in a tooltip by the file name, not on the dialog.
      expect(screen.getByLabelText('About Excel columns')).toBeInTheDocument()
      expect(screen.queryByText(/Choose the columns to translate on each sheet/)).toBeNull()

      await user.click(toggle)
      await user.click(screen.getByTestId('upload-xlsx-catalog.xlsx-sheet-Products-columns'))
      await user.click(screen.getByTestId('upload-xlsx-catalog.xlsx-sheet-Products-columns-option-C'))
      await user.keyboard('{Escape}')
      await user.click(screen.getByTestId('upload-xlsx-catalog.xlsx-sheet-Notes-include'))
      await user.click(screen.getByTestId('upload-confirm'))

      const layouts = onConfirm.mock.calls[0][1] as Map<File, unknown>
      expect(layouts.get(xlsx)).toEqual({ sheets: [{ name: 'Products', header_row: true, columns: ['B', 'C'] }] })
      expect(layouts.size).toBe(1)
    })

    it('holds the upload while the workbook is read, and while no sheet is chosen', async () => {
      const user = userEvent.setup()
      let resolve!: (v: XLSXInspection) => void
      api.inspectXLSX.mockReturnValueOnce(new Promise(r => { resolve = r }))
      const onConfirm = setup(vi.fn(), [new File(['x'], 'catalog.xlsx')])

      expect(await screen.findByText('Reading the workbook…')).toBeTruthy()
      await user.click(screen.getByTestId('upload-confirm'))
      expect(onConfirm).not.toHaveBeenCalled()

      resolve(workbook)
      await user.click(await screen.findByTestId('upload-xlsx-catalog.xlsx-sheet-Products-include'))
      await user.click(screen.getByTestId('upload-xlsx-catalog.xlsx-sheet-Notes-include'))
      expect(screen.getByTestId('upload-xlsx-problem')).toHaveTextContent('Choose at least one sheet to translate in catalog.xlsx.')
      await user.click(screen.getByTestId('upload-confirm'))
      expect(onConfirm).not.toHaveBeenCalled()
    })

    it('asks nothing for a bilingual workbook, and says so to the queue', async () => {
      api.inspectXLSX.mockResolvedValueOnce({ ...workbook, bilingual: true, suggested_layout: undefined })
      const xlsx = new File(['x'], 'bilingual.xlsx')
      const onConfirm = setup(vi.fn(), [xlsx])

      await waitFor(() => expect(api.inspectXLSX).toHaveBeenCalled())
      await waitFor(() => expect(screen.queryByTestId('upload-xlsx-columns')).toBeNull())
      fireEvent.click(screen.getByTestId('upload-confirm'))
      expect((onConfirm.mock.calls[0][1] as Map<File, unknown>).get(xlsx)).toBeNull()
    })

    it('reads the workbooks one at a time', async () => {
      let first!: (v: XLSXInspection) => void
      api.inspectXLSX
        .mockReturnValueOnce(new Promise(r => { first = r }))
        .mockResolvedValueOnce(workbook)
      setup(vi.fn(), [new File(['x'], 'one.xlsx'), new File(['y'], 'two.xlsx')])

      await waitFor(() => expect(api.inspectXLSX).toHaveBeenCalledTimes(1))
      expect((api.inspectXLSX.mock.calls[0][1] as File).name).toBe('one.xlsx')
      first(workbook)
      await waitFor(() => expect(api.inspectXLSX).toHaveBeenCalledTimes(2))
      expect((api.inspectXLSX.mock.calls[1][1] as File).name).toBe('two.xlsx')
    })

    it('holds the upload when a workbook could not be read, until a retry succeeds', async () => {
      const user = userEvent.setup()
      api.inspectXLSX.mockRejectedValueOnce(new ApiError(503, 'Service unavailable')).mockResolvedValueOnce(workbook)
      const xlsx = new File(['x'], 'catalog.xlsx')
      const onConfirm = setup(vi.fn(), [xlsx])

      // Said once, in the file's own block, with its Retry.
      expect(await screen.findByText("Couldn't load the workbook: Service unavailable")).toBeInTheDocument()
      expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(1)
      expect(screen.queryByTestId('upload-xlsx-problem')).toBeNull()
      await user.click(screen.getByTestId('upload-confirm'))
      expect(onConfirm).not.toHaveBeenCalled()

      await user.click(screen.getByRole('button', { name: 'Retry' }))
      await screen.findByTestId('upload-xlsx-catalog.xlsx-sheet-Products-toggle')
      expect(screen.queryByText(/Couldn't load the workbook/)).toBeNull()
      await user.click(screen.getByTestId('upload-confirm'))
      expect((onConfirm.mock.calls[0][1] as Map<File, unknown>).get(xlsx)).toEqual(workbook.suggested_layout)
    })

    it('lets a workbook the server refuses through, for its upload to report', async () => {
      api.inspectXLSX.mockRejectedValueOnce(new ApiError(400, 'could not read the xlsx file'))
      const xlsx = new File(['x'], 'broken.xlsx')
      const onConfirm = setup(vi.fn(), [xlsx])

      await waitFor(() => expect(api.inspectXLSX).toHaveBeenCalled())
      await waitFor(() => expect(screen.queryByTestId('upload-xlsx-columns')).toBeNull())
      expect(screen.queryByTestId('upload-xlsx-problem')).toBeNull()
      fireEvent.click(screen.getByTestId('upload-confirm'))
      expect((onConfirm.mock.calls[0][1] as Map<File, unknown>).get(xlsx)).toBeNull()
    })
  })
})
