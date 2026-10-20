import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useLastNonNull } from './useLastNonNull'

describe('useLastNonNull', () => {
  it('returns the current value while it is set', () => {
    const { result, rerender } = renderHook(({ v }) => useLastNonNull(v), {
      initialProps: { v: 'a' as string | null },
    })
    expect(result.current).toBe('a')
    rerender({ v: 'b' })
    expect(result.current).toBe('b')
  })

  it('keeps the last value once it goes null', () => {
    const { result, rerender } = renderHook(({ v }) => useLastNonNull(v), {
      initialProps: { v: 'a' as string | null },
    })
    rerender({ v: null })
    expect(result.current).toBe('a')
  })

  it('is null until it has held a value', () => {
    const { result } = renderHook(() => useLastNonNull<string>(null))
    expect(result.current).toBeNull()
  })
})
