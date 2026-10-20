import { describe, it, expect } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useCollapsibleOpen } from './useCollapsibleOpen'

describe('useCollapsibleOpen', () => {
  it('starts closed, or open when defaultOpen is set', () => {
    expect(renderHook(() => useCollapsibleOpen()).result.current.open).toBe(false)
    expect(renderHook(() => useCollapsibleOpen(true)).result.current.open).toBe(true)
  })

  it('toggle flips the state', () => {
    const { result } = renderHook(() => useCollapsibleOpen())
    act(() => result.current.toggle())
    expect(result.current.open).toBe(true)
    act(() => result.current.toggle())
    expect(result.current.open).toBe(false)
  })

  // The async case: a list query resolves empty after mount, and the section
  // should reveal itself so the user sees the "add" affordance.
  it('openWhen turning true opens the section', () => {
    const { result, rerender } = renderHook(
      ({ when }) => useCollapsibleOpen(false, when),
      { initialProps: { when: false } },
    )
    expect(result.current.open).toBe(false)
    rerender({ when: true })
    expect(result.current.open).toBe(true)
  })

  // The rule that makes auto-open tolerable: it fires once per mount, so a user
  // who closes the section doesn't have it spring back open on the next render
  // where the condition still holds.
  it('a manual close after auto-open is not undone', () => {
    const { result, rerender } = renderHook(
      ({ when }) => useCollapsibleOpen(false, when),
      { initialProps: { when: false } },
    )
    rerender({ when: true })
    expect(result.current.open).toBe(true)

    act(() => result.current.toggle())
    expect(result.current.open).toBe(false)

    // Same value on a later render.
    rerender({ when: true })
    expect(result.current.open).toBe(false)

    // The case the guard exists for: the condition flickers (a refetch flips
    // the empty-list check off and back on). Without
    // `autoOpened`, this springs the section back open under the user.
    rerender({ when: false })
    rerender({ when: true })
    expect(result.current.open).toBe(false)
  })
})
