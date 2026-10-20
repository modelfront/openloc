// The Files tab as a user drives it: one row per file with its readiness, a
// click that opens the file's tasks, and file actions — Delete only for the
// roles the handler lets through.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'

import { ProjectFilesTab } from './ProjectFilesTab'
import { makeJob } from '@/test/fixtures/job'

const mockDeleteFile = vi.fn()

vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    deleteFile: (...a: unknown[]) => mockDeleteFile(...a),
  }
})

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

const jobs = [
  // guide.docx: fr done, de still in review → in progress, 1 of 2 ready.
  makeJob({ id: 1, file_id: 10, file_name: 'guide.docx', target_language: 'fr', pipeline_status: 'completed' }),
  makeJob({ id: 2, file_id: 10, file_name: 'guide.docx', target_language: 'de' }),
  // api.md: done everywhere.
  makeJob({ id: 3, file_id: 20, file_name: 'api.md', target_language: 'de', pipeline_status: 'completed' }),
]

beforeEach(() => {
  mockDeleteFile.mockReset().mockResolvedValue(undefined)
})
afterEach(cleanup)

function Where() {
  const l = useLocation()
  return <span data-testid="where">{l.pathname + l.search}</span>
}

function renderTab({ isAdmin = true, url = '/projects/42/files', tabJobs = jobs } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[url]}>
        <ProjectFilesTab
          projectId={42}
          jobs={tabJobs}
          isAdmin={isAdmin}
          uploadLocked={false}
          uploading={false}
          uploadLabel="Upload files"
          onPickFiles={() => {}}
        />
        <Routes><Route path="*" element={<Where />} /></Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const rowOf = (name: string) => screen.getByText(name).closest('tr')!

describe('ProjectFilesTab', () => {
  it('lists one row per file with how ready it is', () => {
    renderTab()
    expect(screen.getAllByRole('row')).toHaveLength(3) // header + 2 files
    expect(within(rowOf('guide.docx')).getByText('In progress')).toBeInTheDocument()
    expect(within(rowOf('guide.docx')).getByText('1 of 2 ready')).toBeInTheDocument()
    expect(within(rowOf('api.md')).getByText('Ready')).toBeInTheDocument()
  })

  it('names the languages behind the count on hover', async () => {
    renderTab()
    await userEvent.hover(screen.getByTestId('file-readiness-10'))
    // Base UI opens a tooltip after its hover delay; allow for it.
    const detail = await screen.findByTestId('file-readiness-detail-10', {}, { timeout: 2000 })
    expect(detail).toHaveTextContent('Ready:fr')
    expect(detail).toHaveTextContent('In progress:de')
  })

  it('opens a file\'s tasks, filtered to that file, when its row is clicked', async () => {
    renderTab()
    await userEvent.click(screen.getByText('api.md'))
    expect(screen.getByTestId('where')).toHaveTextContent('/projects/42?file=20')
  })

  it('selecting files does not open them, and offers bulk actions', async () => {
    renderTab()
    fireEvent.click(screen.getByTestId('select-file-10'))
    fireEvent.click(screen.getByTestId('select-file-20'))
    expect(screen.getByTestId('where')).toHaveTextContent('/projects/42/files')
    expect(screen.getByTestId('file-selected-count')).toHaveTextContent('2 files selected')

    await userEvent.click(screen.getByTestId('file-bulk-delete-btn'))
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(mockDeleteFile).toHaveBeenCalledTimes(2))
    expect(mockDeleteFile).toHaveBeenCalledWith(42, 10)
    expect(mockDeleteFile).toHaveBeenCalledWith(42, 20)
  })

  it('a click anywhere in the checkbox cell toggles it, and never opens the file', () => {
    renderTab()
    const box = screen.getByTestId('select-file-10')
    const cell = box.closest('td')!
    fireEvent.click(cell)
    expect(box).toBeChecked()
    expect(screen.getByTestId('where')).toHaveTextContent('/projects/42/files')
    // On the box itself it toggles once, not twice.
    fireEvent.click(box)
    expect(box).not.toBeChecked()
    expect(screen.getByTestId('where')).toHaveTextContent('/projects/42/files')
  })

  it('a click anywhere in the checkbox header selects all, then clears all', () => {
    renderTab()
    const header = screen.getByTestId('select-all-files').closest('th')!
    fireEvent.click(header)
    expect(screen.getByTestId('file-selected-count')).toHaveTextContent('2 files selected')
    fireEvent.click(header)
    expect(screen.queryByTestId('file-actions-bar')).not.toBeInTheDocument()
  })

  // Re-import acts on one task (a file in one language), so it lives on the
  // Tasks tab and in the editor, not on the file row.
  it('file rows offer no Re-import, even to admins', () => {
    renderTab()
    expect(within(rowOf('guide.docx')).queryByRole('button', { name: /re-import/i })).toBeNull()
  })

  it('shows Delete only to admins — a live project\'s files are admin-only to delete', () => {
    renderTab({ isAdmin: false })
    expect(screen.queryByTestId('delete-file-10')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('select-file-10'))
    expect(screen.queryByTestId('file-bulk-delete-btn')).not.toBeInTheDocument()
  })

  it('filters by readiness from the URL, and says when nothing matches', () => {
    renderTab({ url: '/projects/42/files?file_status=ready' })
    expect(screen.queryByText('guide.docx')).not.toBeInTheDocument()
    expect(screen.getByText('api.md')).toBeInTheDocument()
    cleanup()
    renderTab({ url: '/projects/42/files?file_status=failed' })
    expect(screen.getByText('No files match the current filters.')).toBeInTheDocument()
  })
})
