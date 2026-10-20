import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useDebounce } from './useDebounce'

describe('useDebounce', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('returns the initial value synchronously on first render', () => {
    const { result } = renderHook(() => useDebounce('hello', 300))
    expect(result.current).toBe('hello')
  })

  it('does not update the returned value until the delay has elapsed', () => {
    const { result, rerender } = renderHook(({ value }) => useDebounce(value, 300), {
      initialProps: { value: 'a' },
    })
    expect(result.current).toBe('a')

    rerender({ value: 'ab' })
    rerender({ value: 'abc' })

    // Still 'a' because only 299ms have passed
    act(() => { vi.advanceTimersByTime(299) })
    expect(result.current).toBe('a')

    // After the full delay, the latest value ('abc') is emitted once
    act(() => { vi.advanceTimersByTime(1) })
    expect(result.current).toBe('abc')
  })

  it('resets the timer on every change (intermediate values are dropped)', () => {
    const { result, rerender } = renderHook(({ value }) => useDebounce(value, 300), {
      initialProps: { value: 'first' },
    })

    rerender({ value: 'second' })
    act(() => { vi.advanceTimersByTime(200) })
    rerender({ value: 'third' })
    act(() => { vi.advanceTimersByTime(200) })

    // "second" never had a full 300ms window — only "third" will be emitted
    expect(result.current).toBe('first')
    act(() => { vi.advanceTimersByTime(100) })
    expect(result.current).toBe('third')
  })

  it('supports a custom delay', () => {
    const { result, rerender } = renderHook(({ value }) => useDebounce(value, 50), {
      initialProps: { value: 'a' },
    })
    rerender({ value: 'b' })
    act(() => { vi.advanceTimersByTime(49) })
    expect(result.current).toBe('a')
    act(() => { vi.advanceTimersByTime(1) })
    expect(result.current).toBe('b')
  })
})
