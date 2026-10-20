import { describe, it, expect, vi, afterEach } from 'vitest'
import type { ComponentProps } from 'react'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import SpellCheckDialog from './SpellCheckDialog'

const mockCheck = vi.fn()
vi.mock('../api', () => ({ checkJobSpelling: (...args: unknown[]) => mockCheck(...args) }))

function renderDialog(extra: Partial<ComponentProps<typeof SpellCheckDialog>> = {}) {
  return render(
    <SpellCheckDialog
      open
      onClose={vi.fn()}
      jobId={1}
      segmentIds={[10]}
      filteredIndices={[0]}
      translationBySegment={new Map([[10, { id: 100, status: 'draft' }]])}
      textFor={() => 'Guten Morgan'}
      sourceTextFor={() => 'Good morning'}
      filterActive={false}
      beforeRun={vi.fn()}
      onNavigate={vi.fn()}
      onApply={vi.fn()}
      onApplyAll={vi.fn()}
      {...extra}
    />,
  )
}

afterEach(() => {
  cleanup()
  mockCheck.mockReset()
})

describe('SpellCheckDialog', () => {
  // A work modal the user reads the grid behind, so it opts into dragging.
  // The drag itself is covered in ui/dialog.test.tsx.
  it('can be moved by its header', async () => {
    renderDialog()
    const dialog = await screen.findByTestId('spellcheck-dialog')
    expect(dialog.querySelector('[data-slot="dialog-header"]')).toHaveAttribute('data-draggable', 'true')
  })

  // Review only: a task that cannot be edited still walks its findings, but
  // offers no fix. The editable run is the control.
  const finding = {
    results: [{
      translation_id: 100,
      matches: [{
        message: 'Possible spelling mistake', offset: 6, length: 6, replacements: [{ value: 'Morgen' }],
        rule: { id: 'SPELL', description: '', category: { id: 'TYPOS', name: 'Typos' } },
      }],
    }],
    failed: 0,
  }

  it.each([
    [false, true],
    [true, false],
  ])('readOnly=%s → fix buttons offered: %s', async (readOnly, fixable) => {
    mockCheck.mockResolvedValue(finding)
    renderDialog({ readOnly })
    await userEvent.click(await screen.findByTestId('spellcheck-start'))
    expect(await screen.findByText('Error 1 of 1')).toBeInTheDocument()

    expect(screen.queryByTestId('spellcheck-change') !== null).toBe(fixable)
    expect(screen.queryByTestId('spellcheck-change-all') !== null).toBe(fixable)
    expect(screen.getByRole('button', { name: /ignore once/i })).toBeInTheDocument()
  })

  // Locked segments are skipped unless the user picks "All segments
  // (includes locked)". Segment 20 is locked.
  describe('locked segments', () => {
    const actions = (canWriteLocked: boolean) => ({
      canWriteLocked, counts: () => ({ all: 0, nonlocked: 0, locked: 0 }), applyScope: vi.fn(), ignoreInProject: vi.fn(),
    })
    const withLocked = {
      segmentIds: [10, 20],
      filteredIndices: [0, 1],
      translationBySegment: new Map([
        [10, { id: 100, status: 'draft' }],
        [20, { id: 200, status: 'draft', locked: true }],
      ]),
      spellFixActions: actions(true),
    }

    it('opens on "Non-locked segments" and offers "All segments (includes locked)"', async () => {
      renderDialog(withLocked)
      expect(await screen.findByRole('radio', { name: /non-locked segments/i })).toBeChecked()
      const all = screen.getByRole('radio', { name: /all segments \(includes locked\)/i })
      expect(all).not.toBeChecked()
      expect(all).toBeEnabled()
    })

    it('counts the filtered segments, not all of them, in the "Matches current filter" hint', async () => {
      renderDialog({ ...withLocked, filterActive: true, filteredIndices: [1] })
      expect(await screen.findByText('1 segment visible · locked ones skipped')).toBeInTheDocument()
    })

    // Writing through a lock is for owners and admins, the roles that may
    // unlock. Everyone else sees the scope in place, disabled, with the reason.
    it('offers "All segments (includes locked)" only to owners and admins', async () => {
      renderDialog({ ...withLocked, spellFixActions: actions(false) })
      expect(await screen.findByRole('radio', { name: /all segments \(includes locked\)/i })).toBeDisabled()
      expect(screen.getByText('Owners and admins only')).toBeInTheDocument()
    })

    it('opened on "All segments" by someone who may not write locked rows, it falls back to non-locked', async () => {
      mockCheck.mockResolvedValue({ results: [], failed: 0 })
      renderDialog({ ...withLocked, spellFixActions: actions(false), initialScope: 'all' })
      expect(await screen.findByRole('radio', { name: /non-locked segments/i })).toBeChecked()
      await userEvent.click(screen.getByTestId('spellcheck-start'))
      await screen.findByText('Nothing left to review.')
      expect(mockCheck.mock.calls[0][1]).toEqual([100])
      expect(mockCheck.mock.calls[0][3]).toEqual({ includeLocked: false })
    })

    it('the default run checks no locked segment and does not ask for them', async () => {
      mockCheck.mockResolvedValue({ results: [], failed: 0 })
      renderDialog(withLocked)
      await userEvent.click(await screen.findByTestId('spellcheck-start'))
      await screen.findByText('Nothing left to review.')
      expect(mockCheck).toHaveBeenCalledTimes(1)
      expect(mockCheck.mock.calls[0][1]).toEqual([100])
      expect(mockCheck.mock.calls[0][3]).toEqual({ includeLocked: false })
    })

    it('"All segments" checks the locked segment and fixes it with include_locked', async () => {
      mockCheck.mockResolvedValue({ ...finding, results: [{ ...finding.results[0], translation_id: 200 }] })
      const onApply = vi.fn().mockResolvedValue(true)
      renderDialog({ ...withLocked, onApply })
      await userEvent.click(await screen.findByRole('radio', { name: /all segments \(includes locked\)/i }))
      await userEvent.click(screen.getByTestId('spellcheck-start'))
      expect(await screen.findByText('Error 1 of 1')).toBeInTheDocument()
      expect(mockCheck.mock.calls[0][1]).toEqual([100, 200])
      expect(mockCheck.mock.calls[0][3]).toEqual({ includeLocked: true })

      await userEvent.click(screen.getByTestId('spellcheck-change'))
      expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ translationId: 200 }), 'Morgen', true)
    })
  })
})
