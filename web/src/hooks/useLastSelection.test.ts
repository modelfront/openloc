import { describe, it, expect, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { createRef } from 'react'
import { nextSelectionValue, useLastSelection, MAX_SELECTION_LEN, type SelectionLike } from './useLastSelection'

/** Minimal fake Selection — jsdom's own Selection can't be driven deterministically. */
function fakeSelection(text: string, anchorNode: Node | null): SelectionLike {
  return { isCollapsed: text.length === 0, anchorNode, toString: () => text }
}

describe('nextSelectionValue', () => {
  const container = document.createElement('div')
  const inner = document.createElement('span')
  container.appendChild(inner)
  const outside = document.createElement('div')

  it('remembers a selection made inside the container', () => {
    expect(nextSelectionValue('', fakeSelection('two-factor authentication', inner), container))
      .toBe('two-factor authentication')
  })

  it('trims surrounding whitespace', () => {
    expect(nextSelectionValue('', fakeSelection('  login \n', inner), container)).toBe('login')
  })

  it('ignores selections made outside the container', () => {
    // e.g. the linguist highlights a sidebar TM match or a page heading —
    // that must not become the concordance query.
    expect(nextSelectionValue('prev', fakeSelection('sidebar text', outside), container)).toBe('prev')
  })

  it('ignores selections longer than the substring-search limit', () => {
    const wall = 'a'.repeat(MAX_SELECTION_LEN + 1)
    expect(nextSelectionValue('prev', fakeSelection(wall, inner), container)).toBe('prev')
  })

  it('accepts a selection exactly at the limit', () => {
    const edge = 'a'.repeat(MAX_SELECTION_LEN)
    expect(nextSelectionValue('prev', fakeSelection(edge, inner), container)).toBe(edge)
  })

  // The whole reason this module exists: clicking a toolbar button collapses the
  // document selection on mousedown, outside the segment container. If that wiped
  // the remembered phrase, the button would open an empty concordance modal.
  it('KEEPS the remembered phrase when the selection collapses outside the container', () => {
    expect(nextSelectionValue('password', fakeSelection('', outside), container)).toBe('password')
  })

  // ...but a caret placed back inside the segment area means the user moved on,
  // so a later lookup must not resurrect a phrase from a segment they've left.
  it('DROPS the remembered phrase when the selection collapses inside the container', () => {
    expect(nextSelectionValue('password', fakeSelection('', inner), container)).toBe('')
  })

  it('is a no-op when there is no Selection object at all', () => {
    expect(nextSelectionValue('password', null, container)).toBe('password')
  })

  it('ignores everything while the container ref is unset', () => {
    expect(nextSelectionValue('prev', fakeSelection('anything', inner), null)).toBe('prev')
  })
})

describe('useLastSelection', () => {
  afterEach(() => vi.restoreAllMocks())

  function setup() {
    const container = document.createElement('div')
    const inner = document.createElement('span')
    container.appendChild(inner)
    document.body.appendChild(container)
    const ref = createRef<HTMLElement>() as React.RefObject<HTMLElement | null>
    ;(ref as { current: HTMLElement | null }).current = container
    return { container, inner, ref }
  }

  function driveSelection(sel: SelectionLike | null) {
    vi.spyOn(document, 'getSelection').mockReturnValue(sel as unknown as Selection)
    document.dispatchEvent(new Event('selectionchange'))
  }

  it('returns the live selection when one exists', () => {
    const { inner, ref } = setup()
    const { result } = renderHook(() => useLastSelection(ref))
    vi.spyOn(document, 'getSelection').mockReturnValue(fakeSelection('Passwort', inner) as unknown as Selection)
    expect(result.current.getSelectionText()).toBe('Passwort')
  })

  it('falls back to the phrase captured before the selection was collapsed', () => {
    const { container, inner, ref } = setup()
    const { result } = renderHook(() => useLastSelection(ref))

    driveSelection(fakeSelection('two-factor authentication', inner))
    // Toolbar button mousedown: selection collapses onto an element outside the
    // segment container. The live read is now empty; the remembered one isn't.
    driveSelection(fakeSelection('', document.body))
    expect(container.contains(document.body)).toBe(false)

    expect(result.current.getSelectionText()).toBe('two-factor authentication')
  })

  // An over-long live selection means "the user selected something unsearchable",
  // not "the user selected nothing". Deferring to the remembered phrase would
  // auto-search a word they'd visibly moved on from.
  it('returns empty for an over-long live selection rather than an older phrase', () => {
    const { inner, ref } = setup()
    const { result } = renderHook(() => useLastSelection(ref))

    driveSelection(fakeSelection('password', inner))
    expect(result.current.getSelectionText()).toBe('password')

    const wall = 'a'.repeat(MAX_SELECTION_LEN + 1)
    vi.spyOn(document, 'getSelection').mockReturnValue(fakeSelection(wall, inner) as unknown as Selection)
    expect(result.current.getSelectionText()).toBe('')
  })

  it('forgets the phrase once the caret lands back inside the segment area', () => {
    const { inner, ref } = setup()
    const { result } = renderHook(() => useLastSelection(ref))

    driveSelection(fakeSelection('two-factor authentication', inner))
    driveSelection(fakeSelection('', inner))

    expect(result.current.getSelectionText()).toBe('')
  })

  it('stops listening after unmount', () => {
    const { inner, ref } = setup()
    const { result, unmount } = renderHook(() => useLastSelection(ref))
    driveSelection(fakeSelection('first', inner))
    unmount()
    driveSelection(fakeSelection('second', inner))
    // getSelectionText still reads live (mock returns 'second'), so assert on the
    // captured value by clearing the live read first.
    vi.spyOn(document, 'getSelection').mockReturnValue(null)
    expect(result.current.getSelectionText()).toBe('first')
  })
})
