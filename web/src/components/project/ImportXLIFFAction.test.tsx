// Import XLIFF on a task: the trigger opens the file picker, the picked file
// is applied at once, and overwriting approved segments needs a confirmation.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { toast } from 'sonner'
import { ApiError } from '@/api'
import type { ReimportXLIFFResult } from '@/api/files'
import { ImportXLIFFAction } from './ImportXLIFFAction'

const mockReimportXLIFF = vi.fn()
vi.mock('@/api/files', () => ({
  reimportXLIFF: (...args: unknown[]) => mockReimportXLIFF(...args),
}))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() } }))

const result = (over: Partial<ReimportXLIFFResult> = {}): ReimportXLIFFResult => ({
  updated: 0, skipped_locked: 0, skipped_held: 0, skipped_approved: 0, untouched_no_change: 0,
  extra_in_upload: 0, missing_from_upload: 0, force_required: false, applied: true, ...over,
})
const xliff = (name = 'demo_de.xlf') => new File(['<xliff/>'], name, { type: 'application/xml' })

function renderAction() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const invalidate = vi.spyOn(qc, 'invalidateQueries')
  render(
    <QueryClientProvider client={qc}>
      <ImportXLIFFAction projectId={7} job={{ id: 9, file_id: 42, target_language: 'de' }}>
        {(pick, pending) => <button onClick={pick}>{pending ? 'Importing…' : 'Import XLIFF'}</button>}
      </ImportXLIFFAction>
    </QueryClientProvider>,
  )
  const pickFile = (file: File) =>
    fireEvent.change(screen.getByTestId('import-xliff-input'), { target: { files: [file] } })
  return { invalidate, pickFile }
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('ImportXLIFFAction', () => {
  it('the trigger opens the file picker', async () => {
    renderAction()
    const click = vi.spyOn(screen.getByTestId('import-xliff-input'), 'click')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Import XLIFF' }))
    expect(click).toHaveBeenCalled()
  })

  it("imports the picked file into the task's language, toasts the count and refreshes the task", async () => {
    mockReimportXLIFF.mockResolvedValueOnce(result({ updated: 2 }))
    const { invalidate, pickFile } = renderAction()

    pickFile(xliff())

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Import applied: 2 segments updated'))
    expect(mockReimportXLIFF).toHaveBeenCalledWith(42, expect.any(File), { lang: 'de', force: false })
    const keys = invalidate.mock.calls.map(c => (c[0] as { queryKey: unknown[] }).queryKey)
    expect(keys).toContainEqual(['job-translations', 9])
    expect(keys).toContainEqual(['segments', 42])
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('says how many segments it skipped because someone else has them open', async () => {
    mockReimportXLIFF.mockResolvedValueOnce(result({ updated: 2, skipped_held: 1 }))
    const { pickFile } = renderAction()

    pickFile(xliff())

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Import applied: 2 segments updated'))
    expect(toast.warning).toHaveBeenCalledWith('1 segment was skipped because someone else is editing it.')
  })

  it('asks before overwriting approved segments, and imports with force on Overwrite', async () => {
    mockReimportXLIFF
      .mockResolvedValueOnce(result({ applied: false, force_required: true, skipped_approved: 1 }))
      .mockResolvedValueOnce(result({ updated: 3 }))
    const { pickFile } = renderAction()

    pickFile(xliff())

    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('The file changes 1 approved segment, so nothing was imported.')
    expect(toast.success).not.toHaveBeenCalled()

    await userEvent.setup().click(screen.getByRole('button', { name: 'Overwrite' }))

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Import applied: 3 segments updated'))
    expect(mockReimportXLIFF).toHaveBeenLastCalledWith(42, expect.any(File), { lang: 'de', force: true })
  })

  it('imports nothing when the overwrite is cancelled', async () => {
    mockReimportXLIFF.mockResolvedValueOnce(result({ applied: false, force_required: true, skipped_approved: 2 }))
    const { pickFile } = renderAction()

    pickFile(xliff())
    await screen.findByRole('alertdialog')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(mockReimportXLIFF).toHaveBeenCalledTimes(1)
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('refuses a file that is not XLIFF without calling the server', () => {
    const { pickFile } = renderAction()
    pickFile(new File(['{}'], 'demo.json', { type: 'application/json' }))
    expect(toast.error).toHaveBeenCalledWith('Pick an .xliff, .xlf, .sdlxliff, .mxliff or .mqxliff file')
    expect(mockReimportXLIFF).not.toHaveBeenCalled()
  })

  it("shows the server's reason when the import fails", async () => {
    mockReimportXLIFF.mockRejectedValueOnce(new ApiError(409, 'This task has an error.'))
    const { pickFile } = renderAction()

    pickFile(xliff())

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Import failed: This task has an error.'))
  })
})
