import { describe, it, expect, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SegmentSelectionBar } from './SegmentSelectionBar'

// Lock approves the selected segments, so only managers get it; anyone who
// may edit gets Unlock (the routes enforce the same: RequireManager on lock,
// no role gate on unlock). Each shows only when a selected row needs it.

type Gates = { canLock: boolean; canUnlock: boolean; onJoin?: () => void; joinRefusal?: string | null }

function renderBar(gates: Gates, locked: boolean[], picked = locked.map((_, i) => i + 1)) {
  const translationsById = new Map(locked.map((l, i) => [i + 1, { locked: l, hasText: true, status: 'approved' as const }]))
  render(
    <QueryClientProvider client={new QueryClient()}>
      <SegmentSelectionBar
        jobId={1}
        projectId={1}
        picked={picked}
        selectedCount={locked.length}
        translationsById={translationsById}
        canConfirm
        canClear={false}
        canPretranslate={false}
        beforeRun={async () => {}}
        onSpellCheck={() => {}}
        onFindReplace={() => {}}
        onClearSelection={() => {}}
        {...gates}
      />
    </QueryClientProvider>,
  )
}

const manager = { canLock: true, canUnlock: true }
const translator = { canLock: false, canUnlock: true }
const shown = () => ({
  lock: !!screen.queryByTestId('segment-bulk-lock'),
  unlock: !!screen.queryByTestId('segment-bulk-unlock'),
})

describe('SegmentSelectionBar lock and unlock', () => {
  it('shows only Lock when every selected row is unlocked', () => {
    renderBar(manager, [false, false])
    expect(shown()).toEqual({ lock: true, unlock: false })
  })

  it('shows only Unlock when every selected row is locked', () => {
    renderBar(manager, [true, true])
    expect(shown()).toEqual({ lock: false, unlock: true })
  })

  it('shows both when the selection mixes locked and unlocked rows', () => {
    renderBar(manager, [true, false])
    expect(shown()).toEqual({ lock: true, unlock: true })
  })

  it('counts only the selected rows the filter shows', () => {
    renderBar(manager, [true, false], [1])
    expect(shown()).toEqual({ lock: false, unlock: true })
  })

  it('a translator never gets Lock', () => {
    renderBar(translator, [true, false])
    expect(shown()).toEqual({ lock: false, unlock: true })
  })

  it('a translator gets nothing to lock or unlock on unlocked rows', () => {
    renderBar(translator, [false])
    expect(shown()).toEqual({ lock: false, unlock: false })
  })

  it('keeps both, disabled, when the filter hides every selected row', () => {
    renderBar(manager, [true, false], [])
    expect(shown()).toEqual({ lock: true, unlock: true })
    expect(screen.getByTestId('segment-bulk-lock').getAttribute('aria-disabled')).toBe('true')
  })

  it.each(['Lock', 'Unlock'])('%s carries the padlock a locked row shows, and keeps its name', name => {
    renderBar(manager, [true, false])
    const button = screen.getByRole('button', { name })
    expect(button.querySelector('[data-status-icon="locked"]')).not.toBeNull()
  })
})

// Join on the bar joins the selected rows, or says why it cannot.
describe('SegmentSelectionBar join', () => {
  it('joins the selected rows', () => {
    const onJoin = vi.fn()
    renderBar({ ...translator, onJoin, joinRefusal: null }, [false, false])
    fireEvent.click(screen.getByTestId('segment-bulk-join'))
    expect(onJoin).toHaveBeenCalledTimes(1)
  })

  it('says why the selected rows cannot be joined, and does nothing', async () => {
    const onJoin = vi.fn()
    renderBar({ ...translator, onJoin, joinRefusal: 'Select rows that follow each other.' }, [false, false])
    const join = screen.getByTestId('segment-bulk-join')
    expect(join.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(join)
    expect(onJoin).not.toHaveBeenCalled()
    fireEvent.focus(join)
    expect(await screen.findByText('Select rows that follow each other.')).toBeTruthy()
  })

  it('is not offered when the task takes no edits', () => {
    renderBar(translator, [false, false])
    expect(screen.queryByTestId('segment-bulk-join')).toBeNull()
  })
})
