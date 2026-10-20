import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const mockToast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), info: vi.fn() }))
vi.mock('sonner', () => ({ toast: mockToast }))

const mockFind = vi.hoisted(() => vi.fn())
const mockApply = vi.hoisted(() => vi.fn())
vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  tmFindReplaceFind: (...args: unknown[]) => mockFind(...args),
  tmFindReplaceApply: (...args: unknown[]) => mockApply(...args),
}))

import TMBulkFindReplaceModal from './TMBulkFindReplaceModal'

function renderModal(props: Partial<React.ComponentProps<typeof TMBulkFindReplaceModal>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <TMBulkFindReplaceModal
        open={props.open ?? true}
        onClose={props.onClose ?? vi.fn()}
        tmId={props.tmId ?? 7}
        sourceLanguage={props.sourceLanguage}
        targetLanguage={props.targetLanguage}
      />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  mockFind.mockResolvedValue({ matches: [], total_entries: 0, total_replacements: 0, next_cursor: null })
  mockApply.mockResolvedValue({ entries_updated: 0, total_replacements: 0 })
})
afterEach(() => cleanup())

describe('TMBulkFindReplaceModal', () => {
  // A work modal the user reads the entry list behind, so it opts into dragging.
  // The drag itself is covered in ui/dialog.test.tsx.
  it('can be moved by its header', async () => {
    renderModal()
    const dialog = await screen.findByTestId('tm-find-replace-dialog')
    expect(dialog.querySelector('[data-slot="dialog-header"]')).toHaveAttribute('data-draggable', 'true')
  })

  it('does not render when closed', () => {
    renderModal({ open: false })
    expect(screen.queryByTestId('tm-find-replace-dialog')).toBeNull()
  })

  it('renders dialog with all controls when open', async () => {
    renderModal()
    expect(await screen.findByTestId('tm-find-replace-dialog')).toBeInTheDocument()
    expect(screen.getByTestId('tm-fr-find')).toBeInTheDocument()
    expect(screen.getByTestId('tm-fr-replace')).toBeInTheDocument()
    expect(screen.getByTestId('tm-fr-case')).toBeInTheDocument()
    expect(screen.getByTestId('tm-fr-scope-both')).toBeInTheDocument()
    expect(screen.getByTestId('tm-fr-scope-source')).toBeInTheDocument()
    expect(screen.getByTestId('tm-fr-scope-target')).toBeInTheDocument()
  })

  it('Find button is disabled when find input is empty', async () => {
    renderModal()
    expect(await screen.findByTestId('tm-fr-find-btn')).toBeDisabled()
  })

  it('Find button enables when find has text and fires the API with the right args', async () => {
    const user = userEvent.setup()
    renderModal({ tmId: 99, sourceLanguage: 'en', targetLanguage: 'es' })

    await user.type(screen.getByTestId('tm-fr-find'), 'Hello')
    await user.type(screen.getByTestId('tm-fr-replace'), 'Hi')
    await user.click(screen.getByTestId('tm-fr-find-btn'))

    await waitFor(() => {
      expect(mockFind).toHaveBeenCalledWith(99, expect.objectContaining({
        find: 'Hello',
        replace: 'Hi',
        caseSensitive: false,
        scope: 'both',
        sourceLanguage: 'en',
        targetLanguage: 'es',
      }))
    })
  })

  it('shows preview banner with match count after Find', async () => {
    mockFind.mockResolvedValue({
      matches: [{ entry_id: 1, source_text: '', target_text: '', new_source_text: '', new_target_text: '', source_match_count: 0, target_match_count: 1 }],
      total_entries: 1,
      total_replacements: 3,
    })
    const user = userEvent.setup()
    renderModal()

    await user.type(screen.getByTestId('tm-fr-find'), 'foo')
    await user.click(screen.getByTestId('tm-fr-find-btn'))

    const banner = await screen.findByTestId('tm-fr-preview')
    expect(banner).toHaveTextContent(/3 occurrences in 1 entry/i)
  })

  it('shows "No matches found" toast when Find returns empty', async () => {
    mockFind.mockResolvedValue({ matches: [], total_entries: 0, total_replacements: 0, next_cursor: null })
    const user = userEvent.setup()
    renderModal()

    await user.type(screen.getByTestId('tm-fr-find'), 'foo')
    await user.click(screen.getByTestId('tm-fr-find-btn'))

    await waitFor(() => expect(mockToast.info).toHaveBeenCalledWith('No matches found'))
  })

  it('clears preview when find input changes (prevents stale preview → wrong Replace all)', async () => {
    mockFind.mockResolvedValue({ matches: [{ entry_id: 1 } as never], total_entries: 1, total_replacements: 1 })
    const user = userEvent.setup()
    renderModal()

    await user.type(screen.getByTestId('tm-fr-find'), 'foo')
    await user.click(screen.getByTestId('tm-fr-find-btn'))
    await screen.findByTestId('tm-fr-preview')

    // Change find — preview must clear
    await user.type(screen.getByTestId('tm-fr-find'), 'bar')
    await waitFor(() => expect(screen.queryByTestId('tm-fr-preview')).toBeNull())
  })

  it('case-sensitive checkbox flips the API arg', async () => {
    const user = userEvent.setup()
    renderModal()
    await user.type(screen.getByTestId('tm-fr-find'), 'Hi')
    await user.click(screen.getByTestId('tm-fr-case'))
    await user.click(screen.getByTestId('tm-fr-find-btn'))
    await waitFor(() => {
      expect(mockFind).toHaveBeenCalledWith(expect.any(Number), expect.objectContaining({
        caseSensitive: true,
      }))
    })
  })

  it('scope buttons select source-only / target-only', async () => {
    const user = userEvent.setup()
    renderModal()
    await user.type(screen.getByTestId('tm-fr-find'), 'Hi')
    await user.click(screen.getByTestId('tm-fr-scope-source'))
    await user.click(screen.getByTestId('tm-fr-find-btn'))
    await waitFor(() => {
      expect(mockFind).toHaveBeenCalledWith(expect.any(Number), expect.objectContaining({ scope: 'source' }))
    })
  })

  it('Replace all without prior Find triggers a Find first; opens confirm if matches exist', async () => {
    mockFind.mockResolvedValue({ matches: [{ entry_id: 1 } as never], total_entries: 1, total_replacements: 2 })
    const user = userEvent.setup()
    renderModal()

    await user.type(screen.getByTestId('tm-fr-find'), 'foo')
    await user.click(screen.getByTestId('tm-fr-replace-all-btn'))

    await waitFor(() => expect(mockFind).toHaveBeenCalled())
    // Confirm dialog opens (Radix AlertDialog)
    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent(/Replace 2 occurrences/i)
  })

  it('Replace all on a populated preview opens confirm directly without re-finding', async () => {
    mockFind.mockResolvedValue({ matches: [{ entry_id: 1 } as never], total_entries: 1, total_replacements: 2 })
    const user = userEvent.setup()
    renderModal()

    await user.type(screen.getByTestId('tm-fr-find'), 'foo')
    await user.click(screen.getByTestId('tm-fr-find-btn'))
    await screen.findByTestId('tm-fr-preview')
    expect(mockFind).toHaveBeenCalledTimes(1)

    await user.click(screen.getByTestId('tm-fr-replace-all-btn'))
    await screen.findByRole('alertdialog')

    // Replace all button must NOT trigger a second Find when preview is current.
    expect(mockFind).toHaveBeenCalledTimes(1)
  })

  it('Replace all confirm → calls the apply API with the same args, shows success toast, calls onClose', async () => {
    mockFind.mockResolvedValue({ matches: [{ entry_id: 1 } as never], total_entries: 1, total_replacements: 2 })
    mockApply.mockResolvedValue({ entries_updated: 1, total_replacements: 2 })
    const onClose = vi.fn()
    const user = userEvent.setup()
    renderModal({ tmId: 99, onClose })

    await user.type(screen.getByTestId('tm-fr-find'), 'Hello')
    await user.type(screen.getByTestId('tm-fr-replace'), 'Hi')
    await user.click(screen.getByTestId('tm-fr-find-btn'))
    await screen.findByTestId('tm-fr-preview')

    await user.click(screen.getByTestId('tm-fr-replace-all-btn'))
    const confirm = await screen.findByRole('alertdialog')
    await user.click(confirm.querySelector('button[data-slot="alert-dialog-action"]') as HTMLElement)

    await waitFor(() => {
      expect(mockApply).toHaveBeenCalledWith(99, expect.objectContaining({
        find: 'Hello',
        replace: 'Hi',
        scope: 'both',
      }))
    })
    await waitFor(() => {
      expect(mockToast.success).toHaveBeenCalledWith(expect.stringMatching(/Replaced 2 occurrences in 1 entry/i))
    })
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('does NOT mutate when Cancel is clicked from the confirm dialog', async () => {
    mockFind.mockResolvedValue({ matches: [{ entry_id: 1 } as never], total_entries: 1, total_replacements: 1 })
    const user = userEvent.setup()
    renderModal()
    await user.type(screen.getByTestId('tm-fr-find'), 'x')
    await user.click(screen.getByTestId('tm-fr-find-btn'))
    await screen.findByTestId('tm-fr-preview')
    await user.click(screen.getByTestId('tm-fr-replace-all-btn'))
    const confirm = await screen.findByRole('alertdialog')
    await user.click(confirm.querySelector('button[data-slot="alert-dialog-cancel"]') as HTMLElement)

    expect(mockApply).not.toHaveBeenCalled()
  })

  it('shows error toast when apply API fails', async () => {
    mockFind.mockResolvedValue({ matches: [{ entry_id: 1 } as never], total_entries: 1, total_replacements: 1 })
    mockApply.mockRejectedValue(new Error('server boom'))
    const user = userEvent.setup()
    renderModal()
    await user.type(screen.getByTestId('tm-fr-find'), 'x')
    await user.click(screen.getByTestId('tm-fr-find-btn'))
    await screen.findByTestId('tm-fr-preview')
    await user.click(screen.getByTestId('tm-fr-replace-all-btn'))
    const confirm = await screen.findByRole('alertdialog')
    await user.click(confirm.querySelector('button[data-slot="alert-dialog-action"]') as HTMLElement)

    await waitFor(() => {
      expect(mockToast.error).toHaveBeenCalledWith(expect.stringMatching(/Replace failed/i))
    })
  })
})
