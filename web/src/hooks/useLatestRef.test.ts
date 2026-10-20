import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useLatestRef } from './useLatestRef'

describe('useLatestRef', () => {
  it('holds the first value after mount', () => {
    const { result } = renderHook(({ value }) => useLatestRef(value), { initialProps: { value: 'a' } })
    expect(result.current.current).toBe('a')
  })

  it('a handler that captured the ref on mount reads the newest value after rerender', () => {
    const { result, rerender } = renderHook(({ value }) => useLatestRef(value), { initialProps: { value: 'a' } })
    const ref = result.current
    const handler = () => ref.current

    rerender({ value: 'b' })
    expect(handler()).toBe('b')
    rerender({ value: 'c' })
    expect(handler()).toBe('c')
    // The same object on every render, so a subscriber never re-captures it.
    expect(result.current).toBe(ref)
  })
})
