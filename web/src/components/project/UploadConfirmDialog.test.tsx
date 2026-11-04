import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError, type FilterProfile, type FilterProfileChoice, type XLSXInspection } from '@/api'

const api = vi.hoisted(() => ({ inspectXLSX: vi.fn(), listFilterProfiles: vi.fn() }))
vi.mock('@/api', async importOriginal => ({
  ...(await importOriginal<object>()),
  inspectXLSX: api.inspectXLSX,
  listFilterProfiles: api.listFilterProfiles,
}))

import { UploadConfirmDialog } from './UploadConfirmDialog'
import { makeFilterProfile } from '@/test/fixtures/filter-profile'

afterEach(() => cleanup())
beforeEach(() => {
  vi.resetAllMocks()
  api.listFilterProfiles.mockResolvedValue(profiles)
})

const profile = makeFilterProfile
const profiles: FilterProfile[] = [
  profile(1, 'txt', 'Default', 'builtin:txt-default@1'),
  profile(2, 'txt', 'Plain text strings', 'builtin:txt-plain@1'),
  profile(3, 'xlsx', 'Default', 'builtin:xlsx-default@1'),
  profile(4, 'xlsx', 'Plain text strings', 'builtin:xlsx-plain@1'),
  profile(50, 'txt', 'Subtitles'),
  profile(60, 'xlsx', 'Catalog columns'),
]

function setup(onConfirm = vi.fn(), files = [new File(['x'], 'doc.txt')], filterChoices: FilterProfileChoice[] = []) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <UploadConfirmDialog projectId={7} files={files} filterChoices={filterChoices} onOpenChange={() => {}} onConfirm={onConfirm} />
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
        <UploadConfirmDialog projectId={7} files={null} filterChoices={[]} onOpenChange={() => {}} onConfirm={vi.fn()} />
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
      expect(api.inspectXLSX).toHaveBeenCalledWith(7, xlsx, undefined)
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

    it('keeps a multilingual profile\'s target columns, and holds the upload until one source column is left', async () => {
      const user = userEvent.setup()
      const target_columns = [{ language: 'fr', column: 'C' }]
      api.inspectXLSX.mockResolvedValueOnce({
        ...workbook, suggested_layout: { sheets: [{ name: 'Products', header_row: true, columns: ['B'], target_columns }] },
      })
      const xlsx = new File(['x'], 'catalog.xlsx')
      const onConfirm = setup(vi.fn(), [xlsx])

      const toggle = await screen.findByTestId('upload-xlsx-catalog.xlsx-sheet-Products-toggle')
      expect(toggle).toHaveTextContent('Column B · translations in FR C')
      await user.click(toggle)
      expect(screen.getByTestId('upload-xlsx-catalog.xlsx-sheet-Products-targets')).toHaveTextContent('FR in C')
      await user.click(screen.getByTestId('upload-xlsx-catalog.xlsx-sheet-Products-columns'))
      await user.click(screen.getByTestId('upload-xlsx-catalog.xlsx-sheet-Products-columns-option-A'))
      await user.keyboard('{Escape}')
      expect(screen.getByTestId('upload-xlsx-problem'))
        .toHaveTextContent('catalog.xlsx: Sheet Products: Choose exactly one source column for the translations to go next to.')
      await user.click(screen.getByTestId('upload-confirm'))
      expect(onConfirm).not.toHaveBeenCalled()

      await user.click(screen.getByTestId('upload-xlsx-catalog.xlsx-sheet-Products-columns'))
      await user.click(screen.getByTestId('upload-xlsx-catalog.xlsx-sheet-Products-columns-option-A'))
      await user.keyboard('{Escape}')
      await user.click(screen.getByTestId('upload-confirm'))
      const layouts = onConfirm.mock.calls[0][1] as Map<File, unknown>
      expect(layouts.get(xlsx)).toEqual({ sheets: [{ name: 'Products', header_row: true, columns: ['B'], target_columns }] })
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

  describe('filter profiles', () => {
    it('asks one per file that has a filter format, starting from the project\'s choice', async () => {
      setup(vi.fn(), [new File(['x'], 'doc.txt'), new File(['y'], 'readme.md')], [{ format: 'txt', id: 50, name: 'Subtitles' }])

      const picker = await screen.findByTestId('upload-filter-doc.txt')
      await waitFor(() => expect(picker).toHaveTextContent('Subtitles'))
      // Markdown has no filter profiles: nothing to ask.
      expect(screen.queryByTestId('upload-filter-readme.md')).toBeNull()
    })

    it('starts from the Default when the project names none for the format', async () => {
      setup()
      await waitFor(() => expect(screen.getByTestId('upload-filter-doc.txt')).toHaveTextContent('Default'))
    })

    it('sends nothing when the pick is left alone, and the pick when it changed', async () => {
      const user = userEvent.setup()
      const doc = new File(['x'], 'doc.txt')
      const notes = new File(['y'], 'notes.txt')
      const onConfirm = setup(vi.fn(), [doc, notes], [{ format: 'txt', id: 50, name: 'Subtitles' }])

      await waitFor(() => expect(screen.getByTestId('upload-filter-notes.txt')).toHaveTextContent('Subtitles'))
      await user.click(screen.getByTestId('upload-filter-notes.txt'))
      await user.click(screen.getByTestId('upload-filter-notes.txt-option-2'))
      await user.click(screen.getByTestId('upload-confirm'))

      const picked = onConfirm.mock.calls[0][2] as Map<File, number>
      expect([...picked]).toEqual([[notes, 2]])
    })

    it('sends nothing when a file is picked back to the project\'s profile', async () => {
      const user = userEvent.setup()
      const onConfirm = setup()

      await waitFor(() => expect(screen.getByTestId('upload-filter-doc.txt')).toHaveTextContent('Default'))
      await user.click(screen.getByTestId('upload-filter-doc.txt'))
      await user.click(screen.getByTestId('upload-filter-doc.txt-option-50'))
      await user.click(screen.getByTestId('upload-filter-doc.txt'))
      await user.click(screen.getByTestId('upload-filter-doc.txt-option-1'))
      await user.click(screen.getByTestId('upload-confirm'))

      expect((onConfirm.mock.calls[0][2] as Map<File, number>).size).toBe(0)
    })

    it('reads a workbook again with a new pick, and starts its columns from that reading', async () => {
      const user = userEvent.setup()
      const fromProfile: XLSXInspection = {
        ...workbook,
        suggested_layout: { sheets: [{ name: 'Products', header_row: true, columns: ['C'] }] },
      }
      api.inspectXLSX.mockResolvedValueOnce(workbook).mockResolvedValueOnce(fromProfile)
      const xlsx = new File(['x'], 'catalog.xlsx')
      const onConfirm = setup(vi.fn(), [xlsx])

      const toggle = await screen.findByTestId('upload-xlsx-catalog.xlsx-sheet-Products-toggle')
      expect(toggle).toHaveTextContent('Column B')
      expect(api.inspectXLSX).toHaveBeenLastCalledWith(7, xlsx, undefined)

      await waitFor(() => expect(screen.getByTestId('upload-filter-catalog.xlsx')).toHaveTextContent('Default'))
      await user.click(screen.getByTestId('upload-filter-catalog.xlsx'))
      await user.click(screen.getByTestId('upload-filter-catalog.xlsx-option-60'))

      await waitFor(() => expect(api.inspectXLSX).toHaveBeenCalledTimes(2))
      expect(api.inspectXLSX).toHaveBeenLastCalledWith(7, xlsx, 60)
      await waitFor(() => expect(screen.getByTestId('upload-xlsx-catalog.xlsx-sheet-Products-toggle')).toHaveTextContent('Column C'))

      await user.click(screen.getByTestId('upload-confirm'))
      expect((onConfirm.mock.calls[0][1] as Map<File, unknown>).get(xlsx)).toEqual(fromProfile.suggested_layout)
      expect((onConfirm.mock.calls[0][2] as Map<File, number>).get(xlsx)).toBe(60)
    })
  })
})
