import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useStableArray } from './useStableArray'

describe('useStableArray', () => {
  it('keeps the first array while new ones hold the same items in the same order', () => {
    const first = [3, 1, 2]
    const { result, rerender } = renderHook(({ arr }) => useStableArray(arr), { initialProps: { arr: first } })
    rerender({ arr: [3, 1, 2] })
    expect(result.current).toBe(first)
  })

  it('returns the new array when the items or their order change', () => {
    const { result, rerender } = renderHook(({ arr }) => useStableArray(arr), { initialProps: { arr: [1, 2, 3] } })
    const reordered = [1, 3, 2]
    rerender({ arr: reordered })
    expect(result.current).toBe(reordered)
    const shorter = [1, 3]
    rerender({ arr: shorter })
    expect(result.current).toBe(shorter)
    // And the changed array is the one kept from then on.
    rerender({ arr: [1, 3] })
    expect(result.current).toBe(shorter)
  })
})
