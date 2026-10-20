import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useDebouncedCallback } from './useDebouncedCallback'

describe('useDebouncedCallback', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('delays the first call by the given delay', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 300))

    act(() => { result.current('a') })
    expect(fn).not.toHaveBeenCalled()

    act(() => { vi.advanceTimersByTime(299) })
    expect(fn).not.toHaveBeenCalled()

    act(() => { vi.advanceTimersByTime(1) })
    expect(fn).toHaveBeenCalledTimes(1)
    expect(fn).toHaveBeenCalledWith('a')
  })

  it('collapses rapid successive calls into a single invocation with the latest arguments', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 300))

    act(() => {
      result.current('first')
      result.current('second')
      result.current('third')
    })

    act(() => { vi.advanceTimersByTime(300) })

    expect(fn).toHaveBeenCalledTimes(1)
    expect(fn).toHaveBeenCalledWith('third')
  })

  it('resets the timer on every call (never fires if calls keep coming within the delay)', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 300))

    act(() => { result.current('a') })
    act(() => { vi.advanceTimersByTime(200) })
    act(() => { result.current('b') })
    act(() => { vi.advanceTimersByTime(200) })
    act(() => { result.current('c') })
    act(() => { vi.advanceTimersByTime(200) })

    // 600ms have passed total but no single gap ever reached 300ms — no fire yet.
    expect(fn).not.toHaveBeenCalled()

    // After the full delay from the last call, the LAST arguments fire.
    act(() => { vi.advanceTimersByTime(100) })
    expect(fn).toHaveBeenCalledTimes(1)
    expect(fn).toHaveBeenCalledWith('c')
  })

  // Contract: the hook does NOT auto-cancel on unmount. The caller decides
  // by attaching .flush() or .cancel() to their own cleanup — see the hook
  // docstring. This test exists to lock that contract in place; if someone
  // later adds auto-cancel back, they'll break AssignmentSection's
  // flush-on-unmount behavior (because React runs cleanups in an order that
  // can cause auto-cancel to run before the caller's flush).
  it('does NOT auto-cancel on unmount — the caller must flush or cancel explicitly', () => {
    const fn = vi.fn()
    const { result, unmount } = renderHook(() => useDebouncedCallback(fn, 300))

    act(() => { result.current('pending') })
    unmount()

    // Advancing time after unmount: the setTimeout still fires and calls fn.
    // This is intentional — if the caller wants to drop the pending call,
    // they can do `useEffect(() => () => debounced.cancel(), [debounced])`.
    act(() => { vi.advanceTimersByTime(500) })
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('always calls the LATEST version of fn, not a stale closure', () => {
    const { result, rerender } = renderHook(
      ({ fn }: { fn: (x: string) => void }) => useDebouncedCallback(fn, 300),
      { initialProps: { fn: vi.fn() } },
    )

    // Trigger the debounced call with the first fn…
    act(() => { result.current('x') })

    // …then swap the fn before the timer fires.
    const fnB = vi.fn()
    rerender({ fn: fnB })

    act(() => { vi.advanceTimersByTime(300) })

    // The NEWER fn must be the one invoked, not the old one.
    expect(fnB).toHaveBeenCalledTimes(1)
    expect(fnB).toHaveBeenCalledWith('x')
  })

  it('exposes a stable reference across re-renders (so useEffect deps do not thrash)', () => {
    const { result, rerender } = renderHook(
      ({ fn }: { fn: () => void }) => useDebouncedCallback(fn, 300),
      { initialProps: { fn: vi.fn() } },
    )
    const firstRef = result.current

    rerender({ fn: vi.fn() })

    expect(result.current).toBe(firstRef)
  })

  it('supports a custom delay', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 50))

    act(() => { result.current() })
    act(() => { vi.advanceTimersByTime(49) })
    expect(fn).not.toHaveBeenCalled()

    act(() => { vi.advanceTimersByTime(1) })
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('.flush() fires the pending call immediately with the most recent arguments', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 300))

    act(() => {
      result.current('a')
      result.current('b')
    })
    expect(fn).not.toHaveBeenCalled()

    act(() => { result.current.flush() })
    expect(fn).toHaveBeenCalledTimes(1)
    expect(fn).toHaveBeenCalledWith('b')

    // No second fire when the original timer would have elapsed
    act(() => { vi.advanceTimersByTime(500) })
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('.flush() is a no-op when nothing is pending', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 300))

    act(() => { result.current.flush() })
    expect(fn).not.toHaveBeenCalled()
  })

  it('.cancel() discards the pending call without firing', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 300))

    act(() => { result.current('a') })
    act(() => { result.current.cancel() })
    act(() => { vi.advanceTimersByTime(500) })

    expect(fn).not.toHaveBeenCalled()
  })

  it('.cancel() leaves the debounced callback reusable afterwards', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 300))

    act(() => { result.current('first') })
    act(() => { result.current.cancel() })
    act(() => { result.current('second') })
    act(() => { vi.advanceTimersByTime(300) })

    expect(fn).toHaveBeenCalledTimes(1)
    expect(fn).toHaveBeenCalledWith('second')
  })
})
