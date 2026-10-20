import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import FindReplaceModal from './FindReplaceModal'
import { ApiError } from '../api/request'
import { TooltipProvider } from '@/components/ui/tooltip'

// Mock sonner
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

// Mock API functions
const mockFindMatches = vi.fn()
const mockFindReplace = vi.fn()
const mockReplaceOne = vi.fn()
const NONE_SKIPPED = { locked: 0, held: 0, skipped_tags: 0, conflicts: 0, rejected: 0, failed: 0 }
vi.mock('../api', () => ({
  findMatches: (...args: unknown[]) => mockFindMatches(...args),
  findReplace: (...args: unknown[]) => mockFindReplace(...args),
  replaceOne: (...args: unknown[]) => mockReplaceOne(...args),
}))

const defaultProps = {
  open: true,
  onClose: vi.fn(),
  jobId: 42,
  onHighlight: vi.fn(),
  onScrollToTranslation: vi.fn(),
  onDeactivate: vi.fn(),
  onComplete: vi.fn(),
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('FindReplaceModal', () => {
  it('renders Find and Replace all buttons', () => {
    render(<FindReplaceModal {...defaultProps} />)
    expect(screen.getByRole('button', { name: /^find$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /replace all/i })).toBeInTheDocument()
  })

  it('renders Find input and Replace input', () => {
    render(<FindReplaceModal {...defaultProps} />)
    expect(screen.getByPlaceholderText('Find...')).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Replace...')).toBeInTheDocument()
  })

  it('Find button calls findMatches API and shows match count', async () => {
    mockFindMatches.mockResolvedValue({ matches: 3, segments: 2, translation_ids: [10, 20] })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    await user.type(screen.getByPlaceholderText('Find...'), 'hello')
    await user.click(screen.getByRole('button', { name: /^find$/i }))

    await waitFor(() => {
      expect(mockFindMatches).toHaveBeenCalledWith(42, 'hello', { caseSensitive: false, includeLocked: false }, undefined)
    })
    await waitFor(() => {
      expect(screen.getByTestId('match-count')).toHaveTextContent('1/2')
    })
  })

  it('Find button with 0 matches shows toast, no badge', async () => {
    const { toast } = await import('sonner')
    mockFindMatches.mockResolvedValue({ matches: 0, segments: 0, translation_ids: [] })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    await user.type(screen.getByPlaceholderText('Find...'), 'nonexistent')
    await user.click(screen.getByRole('button', { name: /^find$/i }))

    await waitFor(() => {
      expect(toast.info).toHaveBeenCalledWith('No matches found')
    })
    expect(screen.queryByTestId('match-count')).not.toBeInTheDocument()
  })

  it('match count clears when find text changes', async () => {
    mockFindMatches.mockResolvedValue({ matches: 3, segments: 2, translation_ids: [10, 20] })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    // First: find matches
    await user.type(screen.getByPlaceholderText('Find...'), 'hello')
    await user.click(screen.getByRole('button', { name: /^find$/i }))
    await waitFor(() => {
      expect(screen.getByTestId('match-count')).toBeInTheDocument()
    })

    // Type more text — badge should clear
    await user.type(screen.getByPlaceholderText('Find...'), 'x')
    await waitFor(() => {
      expect(screen.queryByTestId('match-count')).not.toBeInTheDocument()
    })
  })

  it('Replace all calls findReplace API and shows replaced count toast', async () => {
    const { toast } = await import('sonner')
    mockFindReplace.mockResolvedValue({ ...NONE_SKIPPED, replaced: 2, segments: 2 })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    await user.type(screen.getByPlaceholderText('Find...'), 'hello')
    await user.type(screen.getByPlaceholderText('Replace...'), 'world')
    await user.click(screen.getByRole('button', { name: /replace all/i }))

    await waitFor(() => {
      expect(mockFindReplace).toHaveBeenCalledWith(42, 'hello', 'world', { caseSensitive: false, includeLocked: false }, undefined)
    })
    // Audit M-8: assert observable DOM/UX state, not just the mock signature.
    // After Replace all, the user-visible signal is a success toast and a
    // completion callback firing.
    await waitFor(() => {
      expect(toast.success).toHaveBeenCalled()
    })
    expect(defaultProps.onComplete).toHaveBeenCalled()
  })

  it('case sensitive checkbox toggles checked state and is passed to Find API', async () => {
    mockFindMatches.mockResolvedValue({ matches: 1, segments: 1, translation_ids: [10] })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    const cs = screen.getByRole('checkbox', { name: /aa/i })
    // Audit M-8: verify the checkbox is observably toggled, not just that the
    // API was called with `true`. A regression that disconnects the checkbox
    // from the API call would still pass mock-only.
    expect(cs).not.toBeChecked()
    await user.click(cs)
    expect(cs).toBeChecked()

    await user.type(screen.getByPlaceholderText('Find...'), 'Hello')
    await user.click(screen.getByRole('button', { name: /^find$/i }))

    await waitFor(() => {
      expect(mockFindMatches).toHaveBeenCalledWith(42, 'Hello', { caseSensitive: true, includeLocked: false }, undefined)
    })
    // After Find, the match-count badge must appear (with 1 match).
    await waitFor(() => {
      expect(screen.getByTestId('match-count')).toHaveTextContent('1/1')
    })
  })

  it('Enter in Find field triggers Find, Enter in Replace field triggers Replace (single)', async () => {
    mockFindMatches.mockResolvedValue({ matches: 2, segments: 1, translation_ids: [10, 20] })
    mockReplaceOne.mockResolvedValue({ ...NONE_SKIPPED, replaced: 1, segments: 1 })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    // Type in Find, press Enter → Find runs + match-count appears.
    await user.type(screen.getByPlaceholderText('Find...'), 'hello')
    await user.keyboard('{Enter}')
    await waitFor(() => {
      expect(mockFindMatches).toHaveBeenCalledWith(42, 'hello', { caseSensitive: false, includeLocked: false }, undefined)
    })
    // Audit M-8: assert the match-count badge actually rendered. Mock-only
    // would pass even if badge logic broke.
    await waitFor(() => {
      expect(screen.getByTestId('match-count')).toHaveTextContent('1/2')
    })
    expect(mockReplaceOne).not.toHaveBeenCalled()

    // Now type in Replace, press Enter → Replace ONE current match.
    await user.type(screen.getByPlaceholderText('Replace...'), 'world')
    await user.keyboard('{Enter}')
    await waitFor(() => {
      // replaceOne called with the first match's translation ID (10)
      expect(mockReplaceOne).toHaveBeenCalledWith(42, 'hello', 'world', { caseSensitive: false, includeLocked: false }, 10)
    })
  })

  it('Enter key triggers Find, calls onHighlight, and renders match-count', async () => {
    mockFindMatches.mockResolvedValue({ matches: 3, segments: 2, translation_ids: [10, 20] })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    await user.type(screen.getByPlaceholderText('Find...'), 'password')
    await user.keyboard('{Enter}')

    await waitFor(() => {
      expect(defaultProps.onHighlight).toHaveBeenCalledWith('password')
    })
    // Audit M-8: pair the mock-call check with an observable DOM state — the
    // match-count badge appears with N/M format, proving the find result also
    // hit the UI, not just the highlight callback.
    await waitFor(() => {
      expect(screen.getByTestId('match-count')).toHaveTextContent('1/2')
    })
  })

  it('Replace all button works independently from Enter', async () => {
    const { toast } = await import('sonner')
    mockFindReplace.mockResolvedValue({ ...NONE_SKIPPED, replaced: 1, segments: 1 })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    await user.type(screen.getByPlaceholderText('Find...'), 'old')
    await user.type(screen.getByPlaceholderText('Replace...'), 'new')
    await user.click(screen.getByRole('button', { name: /replace all/i }))

    await waitFor(() => {
      expect(mockFindReplace).toHaveBeenCalledWith(42, 'old', 'new', { caseSensitive: false, includeLocked: false }, undefined)
    })
    // Audit M-8: assert the Replace all path actually produced its UX side
    // effects (toast + onComplete), not just that the mock was called.
    await waitFor(() => {
      expect(toast.success).toHaveBeenCalled()
    })
    expect(defaultProps.onComplete).toHaveBeenCalled()
    // Find should NOT have been called by Replace all
    expect(mockFindMatches).not.toHaveBeenCalled()
  })

  it('Find All deactivates active segment and renders match-count', async () => {
    mockFindMatches.mockResolvedValue({ matches: 5, segments: 3, translation_ids: [10, 20, 30] })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    await user.type(screen.getByPlaceholderText('Find...'), 'hello')
    await user.click(screen.getByRole('button', { name: /find all/i }))

    await waitFor(() => {
      expect(defaultProps.onDeactivate).toHaveBeenCalled()
    })
    // After Find All the badge counts the matched translation ids ("3 matches")
    // with no current index, because Find All doesn't navigate the way Find does.
    await waitFor(() => {
      expect(screen.getByTestId('match-count')).toHaveTextContent('3 matches')
    })
  })

  it('Replace button disabled after Find All (no active match)', async () => {
    mockFindMatches.mockResolvedValue({ matches: 3, segments: 2, translation_ids: [10, 20] })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    await user.type(screen.getByPlaceholderText('Find...'), 'hello')
    await user.click(screen.getByRole('button', { name: /find all/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^replace$/i })).toBeDisabled()
    })
  })

  it('Replace button enabled after Find navigates to a match', async () => {
    mockFindMatches.mockResolvedValue({ matches: 3, segments: 2, translation_ids: [10, 20] })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    // Find navigates to first match (currentIdx = 0)
    await user.type(screen.getByPlaceholderText('Find...'), 'hello')
    await user.click(screen.getByRole('button', { name: /^find$/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^replace$/i })).toBeEnabled()
    })
  })

  it('onHighlight cleared when closing', async () => {
    const user = userEvent.setup()
    const { rerender } = render(<FindReplaceModal {...defaultProps} />)

    // Sanity: modal IS in the DOM before close.
    expect(screen.getByPlaceholderText('Find...')).toBeInTheDocument()

    // Close via ✕ button
    await user.click(screen.getByRole('button', { name: /✕/ }))

    expect(defaultProps.onHighlight).toHaveBeenCalledWith(null)
    expect(defaultProps.onClose).toHaveBeenCalled()

    // Audit M-8: pair the callback check with an observable DOM transition.
    // After parent receives onClose and re-renders with open=false, the modal
    // contents must unmount.
    rerender(<FindReplaceModal {...defaultProps} open={false} />)
    expect(screen.queryByPlaceholderText('Find...')).not.toBeInTheDocument()
  })

  // A task that cannot be edited still searches, but offers no replace.
  it('readOnly: Find stays, the replace field and both replace buttons are gone', () => {
    render(<FindReplaceModal {...defaultProps} readOnly />)
    expect(screen.getByRole('button', { name: /^find$/i })).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Find...')).toBeInTheDocument()
    expect(screen.queryByPlaceholderText('Replace...')).toBeNull()
    expect(screen.queryByRole('button', { name: /^replace$/i })).toBeNull()
    expect(screen.queryByRole('button', { name: /replace all/i })).toBeNull()
  })

  it('Replace all hands the reopen flag to onComplete', async () => {
    mockFindReplace.mockResolvedValue({ ...NONE_SKIPPED, replaced: 1, segments: 1, taskReopened: true })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)
    await user.type(screen.getByPlaceholderText('Find...'), 'old')
    await user.click(screen.getByRole('button', { name: /replace all/i }))
    await waitFor(() => expect(defaultProps.onComplete).toHaveBeenCalledWith(expect.objectContaining({ taskReopened: true })))
  })

  it('without a selection there is no "In selection" option', () => {
    render(<FindReplaceModal {...defaultProps} />)
    expect(screen.queryByTestId('find-in-selection')).toBeNull()
  })

  it('"In selection" limits Find and Replace all to the selected rows', async () => {
    mockFindMatches.mockResolvedValue({ matches: 1, segments: 1, translation_ids: [20] })
    mockFindReplace.mockResolvedValue({ ...NONE_SKIPPED, replaced: 1, segments: 1 })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} selectedIds={[20, 30]} initialInSelection />)
    expect(screen.getByTestId('find-in-selection')).toBeChecked()
    await user.type(screen.getByPlaceholderText('Find...'), 'old')
    await user.click(screen.getByRole('button', { name: /^find$/i }))
    await waitFor(() => expect(mockFindMatches).toHaveBeenCalledWith(42, 'old', { caseSensitive: false, includeLocked: false }, [20, 30]))
    await user.type(screen.getByPlaceholderText('Replace...'), 'new')
    await user.click(screen.getByRole('button', { name: /replace all/i }))
    await waitFor(() => expect(mockFindReplace).toHaveBeenCalledWith(42, 'old', 'new', { caseSensitive: false, includeLocked: false }, [20, 30]))
  })

  it('unticking "In selection" searches the whole job again', async () => {
    mockFindMatches.mockResolvedValue({ matches: 1, segments: 1, translation_ids: [20] })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} selectedIds={[20]} initialInSelection />)
    await user.click(screen.getByTestId('find-in-selection'))
    await user.type(screen.getByPlaceholderText('Find...'), 'old')
    await user.click(screen.getByRole('button', { name: /^find$/i }))
    await waitFor(() => expect(mockFindMatches).toHaveBeenCalledWith(42, 'old', { caseSensitive: false, includeLocked: false }, undefined))
  })

  it('"In selection" never widens to the whole job when the selection empties', async () => {
    const { toast } = await import('sonner')
    const user = userEvent.setup()
    const { rerender } = render(<FindReplaceModal {...defaultProps} selectedIds={[20]} initialInSelection />)
    rerender(<FindReplaceModal {...defaultProps} selectedIds={[]} initialInSelection />)
    // Still ticked and still visible, so it can be unticked.
    expect(screen.getByTestId('find-in-selection')).toBeChecked()
    await user.type(screen.getByPlaceholderText('Find...'), 'old')
    await user.click(screen.getByRole('button', { name: /^find$/i }))
    await user.type(screen.getByPlaceholderText('Replace...'), 'new')
    await user.click(screen.getByRole('button', { name: /replace all/i }))
    expect(mockFindMatches).not.toHaveBeenCalled()
    expect(mockFindReplace).not.toHaveBeenCalled()
    expect(toast.warning).toHaveBeenCalled()
  })

  it('Replace all that writes nothing warns with the reasons', async () => {
    const { toast } = await import('sonner')
    mockFindReplace.mockResolvedValue({ ...NONE_SKIPPED, replaced: 0, segments: 0, locked: 2 })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)
    await user.type(screen.getByPlaceholderText('Find...'), 'old')
    await user.click(screen.getByRole('button', { name: /replace all/i }))
    await waitFor(() => expect(toast.warning).toHaveBeenCalledWith('Replaced 0 occurrences in 0 segments · 2 locked'))
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('a task-lock refusal goes to onTaskLocked, not a generic error toast', async () => {
    const { toast } = await import('sonner')
    const err = new ApiError(409, 'This task has an error.', { error: { code: 'task_errored', message: 'This task has an error.' } })
    mockFindReplace.mockRejectedValue(err)
    const onTaskLocked = vi.fn()
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} onTaskLocked={onTaskLocked} />)
    await user.type(screen.getByPlaceholderText('Find...'), 'old')
    await user.click(screen.getByRole('button', { name: /replace all/i }))
    await waitFor(() => expect(onTaskLocked).toHaveBeenCalledWith(err))
    expect(toast.error).not.toHaveBeenCalled()
    expect(defaultProps.onComplete).not.toHaveBeenCalled()
  })
})

// Locked segments are left out of find and replace unless the user ticks
// "Include locked"; the box starts unticked every time the bar opens.
describe('FindReplaceModal include locked', () => {
  it('starts unticked and sends includeLocked false to find and replace', async () => {
    mockFindMatches.mockResolvedValue({ matches: 1, segments: 1, translation_ids: [10] })
    mockFindReplace.mockResolvedValue({ ...NONE_SKIPPED, replaced: 1, segments: 1 })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} />)

    expect(screen.getByRole('checkbox', { name: /include locked/i })).not.toBeChecked()
    await user.type(screen.getByPlaceholderText('Find...'), 'old')
    await user.click(screen.getByRole('button', { name: /^find$/i }))
    await waitFor(() => expect(mockFindMatches).toHaveBeenCalledWith(42, 'old', { caseSensitive: false, includeLocked: false }, undefined))
    await user.type(screen.getByPlaceholderText('Replace...'), 'new')
    await user.click(screen.getByRole('button', { name: /replace all/i }))
    await waitFor(() => expect(mockFindReplace).toHaveBeenCalledWith(42, 'old', 'new', { caseSensitive: false, includeLocked: false }, undefined))
  })

  it('ticked, sends includeLocked true to find, replace and replace all', async () => {
    mockFindMatches.mockResolvedValue({ matches: 2, segments: 2, translation_ids: [10, 20] })
    mockReplaceOne.mockResolvedValue({ ...NONE_SKIPPED, replaced: 1, segments: 1 })
    mockFindReplace.mockResolvedValue({ ...NONE_SKIPPED, replaced: 1, segments: 1 })
    const user = userEvent.setup()
    render(<FindReplaceModal {...defaultProps} canReplaceLocked />)

    const box = screen.getByRole('checkbox', { name: /include locked/i })
    await user.click(box)
    expect(box).toBeChecked()
    const opts = { caseSensitive: false, includeLocked: true }

    await user.type(screen.getByPlaceholderText('Find...'), 'old')
    await user.click(screen.getByRole('button', { name: /^find$/i }))
    await waitFor(() => expect(mockFindMatches).toHaveBeenCalledWith(42, 'old', opts, undefined))
    await waitFor(() => expect(screen.getByTestId('match-count')).toHaveTextContent('1/2'))

    await user.type(screen.getByPlaceholderText('Replace...'), 'new')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(mockReplaceOne).toHaveBeenCalledWith(42, 'old', 'new', opts, 10))
    await user.click(screen.getByRole('button', { name: /replace all/i }))
    await waitFor(() => expect(mockFindReplace).toHaveBeenCalledWith(42, 'old', 'new', opts, undefined))
  })

  it('resets to unticked when the bar opens again', async () => {
    const user = userEvent.setup()
    const { rerender } = render(<FindReplaceModal {...defaultProps} />)
    await user.click(screen.getByRole('checkbox', { name: /include locked/i }))
    rerender(<FindReplaceModal {...defaultProps} open={false} />)
    rerender(<FindReplaceModal {...defaultProps} open />)
    expect(screen.getByRole('checkbox', { name: /include locked/i })).not.toBeChecked()
  })

  it('tells a user who may not replace in locked segments that only owners and admins can', async () => {
    const user = userEvent.setup()
    render(<TooltipProvider><FindReplaceModal {...defaultProps} /></TooltipProvider>)
    await user.hover(screen.getByRole('checkbox', { name: /include locked/i }))
    expect(await screen.findByText(/only an owner or admin can replace text in them/i)).toBeInTheDocument()
  })

  it('tells an owner or admin that replace writes locked segments too', async () => {
    const user = userEvent.setup()
    render(<TooltipProvider><FindReplaceModal {...defaultProps} canReplaceLocked /></TooltipProvider>)
    await user.hover(screen.getByRole('checkbox', { name: /include locked/i }))
    expect(await screen.findByText(/replace changes them too/i)).toBeInTheDocument()
  })
})
